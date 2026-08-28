package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testConfig(path string) Config {
	return Config{Path: path, AccountKey: "deployment", AuthorizedUserID: 42, GotdVersion: "v0.161.0", TGLayer: 228, MaxReaders: 2, BusyTimeout: time.Second}
}

func TestOpenCreatesPrivateFileAndRequiresPrivateOwnedRegularExistingFile(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "telegram.db")
	s, err := Open(ctx, testConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %04o", info.Mode().Perm())
	}
	if err := os.Chmod(path, 0640); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, testConfig(path)); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("permissive existing file: %v", err)
	}
}

func TestOpenBindingChecksEveryIdentityField(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "telegram.db")
	cfg := testConfig(path)
	s, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	cases := []Config{cfg, cfg, cfg, cfg}
	cases[0].AccountKey = "other"
	cases[1].AuthorizedUserID++
	cases[2].GotdVersion = "v0.162.0"
	cases[3].TGLayer++
	for _, candidate := range cases {
		if _, err := Open(ctx, candidate); !errors.Is(err, ErrAccountMismatch) {
			t.Fatalf("candidate %+v: %v", candidate, err)
		}
	}
}

func TestLocatorIdentityRetentionAndErasure(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	at := time.Unix(10_000, 0).UTC()
	jid, err := s.AppendJournal(ctx, []byte("raw"), at)
	if err != nil {
		t.Fatal(err)
	}
	locs := []Locator{{Namespace: "message", MessageID: 7, Conversation: "chat:1", Payload: []byte("one")}, {Namespace: "message", MessageID: 7, Conversation: "chat:2", Payload: []byte("two")}}
	if _, err := s.Capture(ctx, Capture{JournalID: jid, Locators: locs, CapturedAt: at}); err != nil {
		t.Fatal(err)
	}
	for _, loc := range locs {
		got, ok, err := s.GetLocator(ctx, loc.Namespace, loc.MessageID, loc.Conversation)
		if err != nil || !ok || string(got.Payload) != string(loc.Payload) {
			t.Fatalf("locator %+v: got=%+v ok=%v err=%v", loc, got, ok, err)
		}
	}
	if err := s.Compact(ctx, at.Add(8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetLocator(ctx, "message", 7, "chat:1")
	if err != nil || !ok || got.Payload != nil || len(got.PayloadHash) == 0 {
		t.Fatalf("compacted locator=%+v ok=%v err=%v", got, ok, err)
	}
	if err := s.Compact(ctx, at.Add(31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.GetLocator(ctx, "message", 7, "chat:1"); err != nil || ok {
		t.Fatalf("expired found=%v err=%v", ok, err)
	}
}

func TestCaptureRequiresPositiveExistingJournalBeforeAnyWrite(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	in := InboxInput{ID: "id", SourceKey: "source", Conversation: "chat:1", Payload: []byte("p"), AvailableAt: time.Unix(1, 0)}
	for _, journalID := range []int64{0, -1, 999} {
		if _, err := s.Capture(ctx, Capture{JournalID: journalID, Inbox: &in, Box: "common", Position: 4}); err == nil {
			t.Fatalf("journal %d accepted", journalID)
		}
		if _, ok, _ := s.GetInbox(ctx, "id"); ok {
			t.Fatal("inbox inserted")
		}
		if pos, _, _, _ := s.Consistency(ctx, "common"); pos != 0 {
			t.Fatal("marker advanced")
		}
	}
}

func TestGetChannelAccessHashPropagatesDatabaseErrors(t *testing.T) {
	s := openTestStore(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.GetChannelAccessHash(context.Background(), 1, 2); err == nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestChannelHashDoesNotCreateCursor(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	if err := s.SetChannelAccessHash(ctx, 42, 99, 123); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.GetChannelPts(ctx, 42, 99); err != nil || ok {
		t.Fatalf("cursor found=%v err=%v", ok, err)
	}
	called := false
	if err := s.ForEachChannels(ctx, 42, func(context.Context, int64, int) error { called = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("hash-only row enumerated as cursor")
	}
}

func TestSendRetryTimeInvariant(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	at := time.Unix(20, 0).UTC()
	for i, tc := range []struct {
		to    SendStatus
		retry *time.Time
	}{{SendRetryable, nil}, {SendSucceeded, &at}, {SendAmbiguous, &at}, {SendPermanentFailed, &at}} {
		key := string(rune('a' + i))
		_, _, err := s.CreateSendIntent(ctx, SendIntent{Key: key, RequestDigest: []byte(key)}, at)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.TransitionSend(ctx, key, SendCreated, SendInFlight, SendTransition{At: at}); err != nil {
			t.Fatal(err)
		}
		if err := s.TransitionSend(ctx, key, SendInFlight, tc.to, SendTransition{At: at, RetryAt: tc.retry}); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("to=%s retry=%v err=%v", tc.to, tc.retry, err)
		}
	}
}

func TestClaimNextAllowsConcurrentStoreWrite(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	at := time.Unix(40, 0).UTC()
	if _, err := s.EnqueueInbox(ctx, InboxInput{ID: "one", SourceKey: "one", Conversation: "chat:1", Payload: []byte("p"), AvailableAt: at}); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	errs := make(chan error, 2)
	go func() { defer wg.Done(); <-start; _, _, err := s.ClaimNext(ctx, "owner", at, time.Minute); errs <- err }()
	go func() { defer wg.Done(); <-start; _, err := s.AppendJournal(ctx, []byte("raw"), at); errs <- err }()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestOversizedInputsAreRejectedWithoutInsertion(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	at := time.Unix(50, 0).UTC()
	tooBigEvent := make([]byte, MaxEventPayloadBytes+1)
	if _, err := s.EnqueueInbox(ctx, InboxInput{ID: "big", SourceKey: "big", Conversation: "chat:1", Payload: tooBigEvent, AvailableAt: at}); !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("inbox: %v", err)
	}
	if _, ok, _ := s.GetInbox(ctx, "big"); ok {
		t.Fatal("oversized inbox inserted")
	}
	if _, err := s.AppendJournal(ctx, make([]byte, MaxRawFieldBytes+1), at); !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("journal: %v", err)
	}
	if err := s.PutPeer(ctx, Peer{Kind: "user", ID: 1, Payload: make([]byte, MaxRawFieldBytes+1)}); !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("peer: %v", err)
	}
	if _, _, err := s.CreateSendIntent(ctx, SendIntent{Key: "send", RequestDigest: make([]byte, MaxRawFieldBytes+1)}, at); !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("send: %v", err)
	}
	jid, err := s.AppendJournal(ctx, []byte("raw"), at)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Capture(ctx, Capture{JournalID: jid, Locators: []Locator{{Namespace: "m", MessageID: 1, Conversation: "c", Payload: make([]byte, MaxLocatorPayloadBytes+1)}}})
	if !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("locator: %v", err)
	}
}

func TestCurrentSchemaHasSeparatedHashAndLocatorIdentityAndMigratesPreviousLayout(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	for _, table := range []string{"channel_access_hash", "locators"} {
		var name string
		if err := s.db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name); err != nil {
			t.Fatalf("missing %s: %v", table, err)
		}
	}
	_ = s.Close()
	path := filepath.Join(t.TempDir(), "old.db")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	old, err := Open(ctx, testConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = old.db.ExecContext(ctx, "UPDATE schema_meta SET version=?", CurrentSchemaVersion-1); err != nil {
		t.Fatal(err)
	}
	_ = old.Close()
	upgraded, err := Open(ctx, testConfig(path))
	if err != nil {
		t.Fatalf("upgrade previous schema: %v", err)
	}
	_ = upgraded.Close()
	path = filepath.Join(t.TempDir(), "bad-layout.db")
	bad, err := Open(ctx, testConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = bad.db.ExecContext(ctx, "ALTER TABLE channel_state ADD COLUMN access_hash INTEGER"); err != nil {
		t.Fatal(err)
	}
	_ = bad.Close()
	if _, err = Open(ctx, testConfig(path)); err == nil || !strings.Contains(err.Error(), "unsupported schema layout") {
		t.Fatalf("incompatible current layout: %v", err)
	}
}

func TestOpenRejectsCurrentSchemaWithWrongLocatorPrimaryKey(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "wrong-locator-key.db")
	s, err := Open(ctx, testConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.ExecContext(ctx, `
ALTER TABLE locators RENAME TO valid_locators;
CREATE TABLE locators(account_key TEXT NOT NULL,namespace TEXT NOT NULL,message_id INTEGER NOT NULL,conversation TEXT NOT NULL,payload BLOB,payload_hash BLOB NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,PRIMARY KEY(account_key,namespace,message_id));
DROP TABLE valid_locators;`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, testConfig(path)); err == nil || !strings.Contains(err.Error(), "unsupported schema layout") {
		t.Fatalf("wrong locator primary key accepted: %v", err)
	}
}
