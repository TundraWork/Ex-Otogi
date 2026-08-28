package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gotd/td/telegram/updates"
)

func privateStoreDirectory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), Config{Path: filepath.Join(privateStoreDirectory(t), "telegram.db"), AccountKey: "deployment", AuthorizedUserID: 42, GotdVersion: "v0.161.0", TGLayer: 228, MaxReaders: 2, BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenBindingPragmasAndPathValidation(t *testing.T) {
	ctx := context.Background()
	dir := privateStoreDirectory(t)
	path := filepath.Join(dir, "db.sqlite")
	s, err := Open(ctx, Config{Path: path, AccountKey: "a", AuthorizedUserID: 7, GotdVersion: "v", TGLayer: 228})
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.SchemaVersion != CurrentSchemaVersion || info.JournalMode != "wal" || !info.ForeignKeys || info.BusyTimeoutMS <= 0 || info.MaxOpenConnections < 2 {
		t.Fatalf("bad info: %+v", info)
	}
	_ = s.Close()
	if _, err := Open(ctx, Config{Path: path, AccountKey: "other", AuthorizedUserID: 7}); !errors.Is(err, ErrAccountMismatch) {
		t.Fatalf("binding mismatch: %v", err)
	}
	if _, err := Open(ctx, Config{Path: ":memory:", AccountKey: "a", AuthorizedUserID: 7}); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("memory path: %v", err)
	}
	if _, err := Open(ctx, Config{Path: dir, AccountKey: "a", AuthorizedUserID: 7}); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("directory: %v", err)
	}
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, nil, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, Config{Path: link, AccountKey: "a", AuthorizedUserID: 7}); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("symlink: %v", err)
	}
}

func TestGotdStateAndHashesAreScopedAndMissingUpdatesFail(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	var _ updates.StateStorage = s
	var _ updates.ChannelAccessHasher = s
	var _ updates.UserAccessHasher = s
	if _, found, err := s.GetState(ctx, 42); err != nil || found {
		t.Fatalf("missing state: %v %v", found, err)
	}
	if err := s.SetPts(ctx, 42, 1); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("missing update: %v", err)
	}
	want := updates.State{Pts: 1, Qts: 2, Date: 3, Seq: 4}
	if err := s.SetState(ctx, 42, want); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPts(ctx, 42, 11); err != nil {
		t.Fatal(err)
	}
	if err := s.SetQts(ctx, 42, 12); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDate(ctx, 42, 13); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSeq(ctx, 42, 14); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDateSeq(ctx, 42, 15, 16); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.GetState(ctx, 42)
	if err != nil || !found || got != (updates.State{Pts: 11, Qts: 12, Date: 15, Seq: 16}) {
		t.Fatalf("state: %+v %v %v", got, found, err)
	}
	if err := s.SetChannelPts(ctx, 42, 9, 21); err != nil {
		t.Fatal(err)
	}
	pts, found, err := s.GetChannelPts(ctx, 42, 9)
	if err != nil || !found || pts != 21 {
		t.Fatalf("channel: %d %v %v", pts, found, err)
	}
	var seen []int64
	if err := s.ForEachChannels(ctx, 42, func(_ context.Context, id int64, pts int) error { seen = append(seen, id); return nil }); err != nil || len(seen) != 1 || seen[0] != 9 {
		t.Fatalf("foreach: %v %v", seen, err)
	}
	if err := s.SetChannelAccessHash(ctx, 42, 9, 99); err != nil {
		t.Fatal(err)
	}
	if h, ok, e := s.GetChannelAccessHash(ctx, 42, 9); e != nil || !ok || h != 99 {
		t.Fatalf("channel hash %d %v %v", h, ok, e)
	}
	if err := s.SetUserAccessHash(ctx, 42, 8, 88); err != nil {
		t.Fatal(err)
	}
	if h, ok, e := s.GetUserAccessHash(ctx, 42, 8); e != nil || !ok || h != 88 {
		t.Fatalf("user hash %d %v %v", h, ok, e)
	}
	if _, ok, _ := s.GetChannelPts(ctx, 99, 9); ok {
		t.Fatal("channel state leaked between accounts")
	}
}

func TestPeerJournalCaptureDedupeAndStartup(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	now := time.Unix(1000, 0).UTC()
	peer := Peer{Kind: "channel", ID: 7, AccessHash: 8, Payload: []byte("peer")}
	if err := s.PutPeer(ctx, peer); err != nil {
		t.Fatal(err)
	}
	if got, ok, e := s.GetPeer(ctx, "channel", 7); e != nil || !ok || string(got.Payload) != "peer" {
		t.Fatalf("peer %+v %v %v", got, ok, e)
	}
	j1, err := s.AppendJournal(ctx, []byte("raw1"), now)
	if err != nil {
		t.Fatal(err)
	}
	j2, _ := s.AppendJournal(ctx, []byte("raw2"), now.Add(time.Second))
	ids, err := s.StartupJournalIDs(ctx, 1)
	if !errors.Is(err, ErrStartupOverflow) || len(ids) != 1 || ids[0] != j1 {
		t.Fatalf("startup %v %v", ids, err)
	}
	in := InboxInput{ID: "event-1", SourceKey: "stable", Conversation: "chat:1", Sequence: 1, Payload: []byte("normalized"), AvailableAt: now}
	q := &QuarantineInput{SourceKey: "unknown", Kind: "new-layer", Payload: []byte("tl")}
	inserted, err := s.Capture(ctx, Capture{JournalID: j1, Inbox: &in, Quarantine: q, Locators: []Locator{{Namespace: "common", MessageID: 5, Conversation: "chat:1", Payload: []byte("loc")}}, Box: "common", Position: 10})
	if err != nil || !inserted {
		t.Fatalf("capture %v %v", inserted, err)
	}
	inserted, err = s.Capture(ctx, Capture{JournalID: j2, Inbox: &in})
	if err != nil || inserted {
		t.Fatalf("dedupe %v %v", inserted, err)
	}
	row, ok, err := s.Journal(ctx, j1)
	if err != nil || !ok || row.NormalizedAt == nil {
		t.Fatalf("journal %+v %v %v", row, ok, err)
	}
	loc, ok, err := s.GetLocator(ctx, "common", 5, "chat:1")
	if err != nil || !ok || loc.Conversation != "chat:1" {
		t.Fatalf("locator %+v %v %v", loc, ok, err)
	}
	pos, degraded, reason, err := s.Consistency(ctx, "common")
	if err != nil || pos != 10 || degraded || reason != "" {
		t.Fatalf("consistency %d %v %q %v", pos, degraded, reason, err)
	}
	if err := s.RecordGap(ctx, "common", 11, "cursor ahead"); err != nil {
		t.Fatal(err)
	}
	_, degraded, reason, _ = s.Consistency(ctx, "common")
	if !degraded || reason != "cursor ahead" {
		t.Fatalf("gap %v %q", degraded, reason)
	}
}

func TestCaptureBatchLeavesJournalPendingUntilCompleteDelivery(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	now := time.Unix(1500, 0).UTC()
	journalID, err := s.AppendJournal(ctx, []byte("raw"), now)
	if err != nil {
		t.Fatal(err)
	}
	first := InboxInput{ID: "first", SourceKey: "first", Conversation: "chat:1", Sequence: 1, Payload: []byte("first"), AvailableAt: now}
	if _, err := s.Capture(ctx, Capture{JournalID: journalID, KeepJournalPending: true, Inbox: &first}); err != nil {
		t.Fatal(err)
	}
	row, _, err := s.Journal(ctx, journalID)
	if err != nil || row.NormalizedAt != nil {
		t.Fatalf("journal normalized before completion: %+v err=%v", row, err)
	}
	second := InboxInput{ID: "second", SourceKey: "second", Conversation: "chat:1", Sequence: 2, Payload: []byte("second"), AvailableAt: now}
	if _, err := s.Capture(ctx, Capture{JournalID: journalID, Inbox: &second}); err != nil {
		t.Fatal(err)
	}
	row, _, err = s.Journal(ctx, journalID)
	if err != nil || row.NormalizedAt == nil {
		t.Fatalf("completed journal remained pending: %+v err=%v", row, err)
	}
}

func TestCaptureBatchRejectsUnboundedRowsBeforeTransaction(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	journalID, err := s.AppendJournal(ctx, []byte("raw"), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	captures := make([]Capture, MaxCaptureRows+1)
	for i := range captures {
		captures[i].JournalID = journalID
	}
	if _, err := s.CaptureBatch(ctx, captures); !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("oversized capture error = %v", err)
	}
	row, _, err := s.Journal(ctx, journalID)
	if err != nil || row.NormalizedAt != nil {
		t.Fatalf("oversized capture mutated journal: %+v err=%v", row, err)
	}
}

func TestClaimsPreserveConversationOrderAndRecoverLeases(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	now := time.Unix(2000, 0).UTC()
	for _, in := range []InboxInput{{ID: "a1", SourceKey: "s1", Conversation: "a", Sequence: 1, Payload: []byte("1"), AvailableAt: now}, {ID: "a2", SourceKey: "s2", Conversation: "a", Sequence: 2, Payload: []byte("2"), AvailableAt: now}, {ID: "b1", SourceKey: "s3", Conversation: "b", Sequence: 1, Payload: []byte("3"), AvailableAt: now}} {
		if ok, err := s.EnqueueInbox(ctx, in); err != nil || !ok {
			t.Fatalf("enqueue %v %v", ok, err)
		}
	}
	c1, ok, err := s.ClaimNext(ctx, "w1", now, time.Minute)
	if err != nil || !ok || c1.ID != "a1" {
		t.Fatalf("claim1 %+v %v %v", c1, ok, err)
	}
	c2, ok, err := s.ClaimNext(ctx, "w2", now, time.Minute)
	if err != nil || !ok || c2.ID != "b1" {
		t.Fatalf("claim2 %+v %v %v", c2, ok, err)
	}
	if err := s.CompleteClaim(ctx, "b1", "w2", now); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseClaim(ctx, "a1", "w1", now.Add(time.Hour), "capacity"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.ClaimNext(ctx, "w", now, time.Minute); ok {
		t.Fatal("later conversation row overtook delayed head")
	}
	c, ok, err := s.ClaimNext(ctx, "w", now.Add(2*time.Hour), time.Minute)
	if err != nil || !ok || c.ID != "a1" || c.Attempts != 2 {
		t.Fatalf("reclaim %+v %v %v", c, ok, err)
	}
	if err := s.CompleteClaim(ctx, "a1", "wrong", now); !errors.Is(err, ErrClaimConflict) {
		t.Fatalf("owner conflict %v", err)
	}
	if err := s.CompleteClaim(ctx, "a1", "w", now); err != nil {
		t.Fatal(err)
	}
	c, ok, err = s.ClaimNext(ctx, "w", now.Add(2*time.Hour), time.Minute)
	if err != nil || !ok || c.ID != "a2" {
		t.Fatalf("ordered successor %+v %v %v", c, ok, err)
	}
	if err := s.FailClaim(ctx, "a2", "w", "invalid", now); err != nil {
		t.Fatal(err)
	}
}

func TestClaimsProgressDistinctConversationBeforeLaterSameConversationRow(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	now := time.Unix(2500, 0).UTC()
	for _, in := range []InboxInput{
		{ID: "a1", SourceKey: "fair-a1", Conversation: "a", Sequence: 1, Payload: []byte("1"), AvailableAt: now},
		{ID: "a2", SourceKey: "fair-a2", Conversation: "a", Sequence: 2, Payload: []byte("2"), AvailableAt: now},
		{ID: "b1", SourceKey: "fair-b1", Conversation: "b", Sequence: 1, Payload: []byte("3"), AvailableAt: now},
	} {
		if ok, err := s.EnqueueInbox(ctx, in); err != nil || !ok {
			t.Fatalf("enqueue %v %v", ok, err)
		}
	}
	first, ok, err := s.ClaimNext(ctx, "worker", now, time.Minute)
	if err != nil || !ok || first.ID != "a1" {
		t.Fatalf("first claim=%+v ok=%v err=%v", first, ok, err)
	}
	if err := s.CompleteClaim(ctx, first.ID, "worker", now); err != nil {
		t.Fatal(err)
	}
	second, ok, err := s.ClaimNextAfter(ctx, "worker", now, time.Minute, first.Conversation)
	if err != nil || !ok || second.ID != "b1" {
		t.Fatalf("distinct conversation did not progress: claim=%+v ok=%v err=%v", second, ok, err)
	}
}

func TestClaimsPreserveInsertionOrderForEqualConversationSequence(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	now := time.Unix(2600, 0).UTC()
	for _, in := range []InboxInput{
		{ID: "first", SourceKey: "equal-first", Conversation: "a", Sequence: 7, Payload: []byte("1"), AvailableAt: now},
		{ID: "second", SourceKey: "equal-second", Conversation: "a", Sequence: 7, Payload: []byte("2"), AvailableAt: now},
	} {
		if ok, err := s.EnqueueInbox(ctx, in); err != nil || !ok {
			t.Fatalf("enqueue %v %v", ok, err)
		}
	}
	first, ok, err := s.ClaimNext(ctx, "worker", now, time.Minute)
	if err != nil || !ok || first.ID != "first" {
		t.Fatalf("first claim=%+v ok=%v err=%v", first, ok, err)
	}
	if err := s.CompleteClaim(ctx, first.ID, "worker", now); err != nil {
		t.Fatal(err)
	}
	second, ok, err := s.ClaimNext(ctx, "worker", now, time.Minute)
	if err != nil || !ok || second.ID != "second" {
		t.Fatalf("second claim=%+v ok=%v err=%v", second, ok, err)
	}
}

func TestRetentionCompactionAndAccountErasure(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	old := time.Now().UTC().Add(-40 * 24 * time.Hour)
	_, _ = s.EnqueueInbox(ctx, InboxInput{ID: "old", SourceKey: "old-source", Conversation: "a", Sequence: 1, Payload: []byte("secret"), AvailableAt: old})
	c, _, _ := s.ClaimNext(ctx, "w", old, time.Hour)
	_ = c
	if err := s.CompleteClaim(ctx, "old", "w", old); err != nil {
		t.Fatal(err)
	}
	jid, _ := s.AppendJournal(ctx, []byte("raw"), old)
	if err := s.MarkJournalNormalized(ctx, jid, old); err != nil {
		t.Fatal(err)
	}
	if err := s.Compact(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	row, ok, err := s.GetInbox(ctx, "old")
	if err != nil || ok || row.Payload != nil {
		t.Fatalf("expired inbox %+v %v %v", row, ok, err)
	}
	if _, ok, _ := s.Journal(ctx, jid); ok {
		t.Fatal("expired normalized journal retained")
	}
	if err := s.EraseAccount(ctx); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.GetState(ctx, 42); found {
		t.Fatal("state survived erasure")
	}
	if _, ok, _ := s.GetPeer(ctx, "channel", 7); ok {
		t.Fatal("peer survived erasure")
	}
}

func TestSendIntentTransitionsConflictRestartAndRetention(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	now := time.Unix(3000, 0).UTC()
	in := SendIntent{Key: "key", RequestDigest: []byte("digest"), RandomID: 99, PeerLocator: []byte("peer")}
	got, created, err := s.CreateSendIntent(ctx, in, now)
	if err != nil || !created || got.Status != SendCreated {
		t.Fatalf("create %+v %v %v", got, created, err)
	}
	_, created, err = s.CreateSendIntent(ctx, in, now)
	if err != nil || created {
		t.Fatalf("idempotent %v %v", created, err)
	}
	other := in
	other.RequestDigest = []byte("other")
	if _, _, err = s.CreateSendIntent(ctx, other, now); !errors.Is(err, ErrSendConflict) {
		t.Fatalf("digest conflict %v", err)
	}
	if err := s.TransitionSend(ctx, "key", SendCreated, SendSucceeded, SendTransition{At: now}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("invalid edge %v", err)
	}
	if err := s.TransitionSend(ctx, "key", SendCreated, SendInFlight, SendTransition{At: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.TransitionSend(ctx, "key", SendCreated, SendInFlight, SendTransition{At: now}); !errors.Is(err, ErrSendConflict) {
		t.Fatalf("stale edge %v", err)
	}
	retry := now.Add(time.Hour)
	if err := s.TransitionSend(ctx, "key", SendInFlight, SendRetryable, SendTransition{At: now, RetryAt: &retry, Error: "flood"}); err != nil {
		t.Fatal(err)
	}
	pending, err := s.RecoverableSends(ctx, now.Add(2*time.Hour))
	if err != nil || len(pending) != 1 || pending[0].RandomID != 99 {
		t.Fatalf("recoverable %+v %v", pending, err)
	}
	if err := s.TransitionSend(ctx, "key", SendRetryable, SendInFlight, SendTransition{At: retry}); err != nil {
		t.Fatal(err)
	}
	if err := s.TransitionSend(ctx, "key", SendInFlight, SendSucceeded, SendTransition{At: retry, ProviderMessageID: 123}); err != nil {
		t.Fatal(err)
	}
	if err := s.PurgeTerminalSends(ctx, retry.Add(31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.GetSendIntent(ctx, "key"); ok {
		t.Fatal("terminal send retained")
	}
	amb := SendIntent{Key: "amb", RequestDigest: []byte("d"), RandomID: 100}
	_, _, _ = s.CreateSendIntent(ctx, amb, oldTime())
	_ = s.TransitionSend(ctx, "amb", SendCreated, SendInFlight, SendTransition{At: oldTime()})
	_ = s.TransitionSend(ctx, "amb", SendInFlight, SendAmbiguous, SendTransition{At: oldTime()})
	_ = s.PurgeTerminalSends(ctx, time.Now().UTC())
	if _, ok, _ := s.GetSendIntent(ctx, "amb"); !ok {
		t.Fatal("ambiguous send purged")
	}
}

func TestCaptureBatchCompletesCorrelatedSendAtomically(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	now := time.Unix(4000, 0).UTC()
	_, _, err := s.CreateSendIntent(ctx, SendIntent{Key: "correlated", RequestDigest: []byte("digest"), RandomID: 77}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.TransitionSend(ctx, "correlated", SendCreated, SendInFlight, SendTransition{At: now}); err != nil {
		t.Fatal(err)
	}
	journal, err := s.AppendJournal(ctx, []byte("packet"), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CaptureBatch(ctx, []Capture{{JournalID: journal, CapturedAt: now, SendCorrelations: []SendCorrelation{{RandomID: 77, MessageID: 88}}}}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetSendIntent(ctx, "correlated")
	if err != nil || !ok || got.Status != SendSucceeded || got.ProviderMessageID != 88 {
		t.Fatalf("got=%+v ok=%v err=%v", got, ok, err)
	}
}

func oldTime() time.Time { return time.Unix(1, 0).UTC() }
