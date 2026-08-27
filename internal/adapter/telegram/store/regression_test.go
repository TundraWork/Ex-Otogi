package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestCaptureRejectsMissingJournalAndRollsBack(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	in := InboxInput{ID: "event", SourceKey: "source", Conversation: "chat:1", Sequence: 1, Payload: []byte("payload"), AvailableAt: time.Unix(1, 0).UTC()}

	if _, err := s.Capture(ctx, Capture{JournalID: 999, Inbox: &in, Box: "common", Position: 12}); err == nil {
		t.Fatal("capture accepted a journal row that does not belong to the account")
	}
	if _, ok, err := s.GetInbox(ctx, in.ID); err != nil || ok {
		t.Fatalf("failed capture persisted inbox row: found=%v err=%v", ok, err)
	}
	if position, _, _, err := s.Consistency(ctx, "common"); err != nil || position != 0 {
		t.Fatalf("failed capture persisted consistency position: position=%d err=%v", position, err)
	}
}

func TestCreateSendIntentConcurrentIdempotency(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	at := time.Unix(3000, 0).UTC()
	in := SendIntent{Key: "same-key", RequestDigest: []byte("same-digest"), RandomID: 99, PeerLocator: []byte("peer")}

	const callers = 32
	start := make(chan struct{})
	results := make(chan struct {
		intent  SendIntent
		created bool
		err     error
	}, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got, created, err := s.CreateSendIntent(ctx, in, at)
			results <- struct {
				intent  SendIntent
				created bool
				err     error
			}{got, created, err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	created := 0
	for result := range results {
		if result.err != nil {
			t.Errorf("concurrent idempotent create returned error: %v", result.err)
			continue
		}
		if result.created {
			created++
		}
		if result.intent.Key != in.Key || !bytes.Equal(result.intent.RequestDigest, in.RequestDigest) || result.intent.RandomID != in.RandomID {
			t.Errorf("concurrent create returned wrong intent: %+v", result.intent)
		}
	}
	if created != 1 {
		t.Fatalf("created count = %d, want 1", created)
	}
}

func TestCreateSendIntentConcurrentDigestConflictIsDeterministic(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	at := time.Unix(3000, 0).UTC()

	const callers = 32
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, err := s.CreateSendIntent(ctx, SendIntent{Key: "same-key", RequestDigest: []byte(fmt.Sprintf("digest-%d", i)), RandomID: int64(i + 1)}, at)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	succeeded := 0
	for err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrSendConflict):
		default:
			t.Errorf("concurrent conflicting create returned raw error: %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("successful create count = %d, want 1", succeeded)
	}
}

func TestCompactQuarantinePayloadBeforeDedupeTombstone(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	now := time.Now().UTC()
	q := QuarantineInput{SourceKey: "unsupported:1", Kind: "new-layer", Payload: []byte("participant-payload")}
	journalID, err := s.AppendJournal(ctx, []byte("raw"), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Capture(ctx, Capture{JournalID: journalID, Quarantine: &q}); err != nil {
		t.Fatal(err)
	}
	created := now.Add(-8 * 24 * time.Hour).UnixNano()
	if _, err := s.db.ExecContext(ctx, "UPDATE quarantine SET created_at=? WHERE account_key=? AND source_key=?", created, s.accountKey, q.SourceKey); err != nil {
		t.Fatal(err)
	}

	if err := s.Compact(ctx, now); err != nil {
		t.Fatal(err)
	}
	var payload []byte
	if err := s.db.QueryRowContext(ctx, "SELECT payload FROM quarantine WHERE account_key=? AND source_key=?", s.accountKey, q.SourceKey).Scan(&payload); err != nil {
		t.Fatalf("quarantine tombstone was removed before 30 days: %v", err)
	}
	if payload != nil {
		t.Fatalf("quarantine participant payload retained after 7 days: %q", payload)
	}
	journalID, err = s.AppendJournal(ctx, []byte("raw-again"), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Capture(ctx, Capture{JournalID: journalID, Quarantine: &q}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM quarantine WHERE account_key=? AND source_key=?", s.accountKey, q.SourceKey).Scan(&count); err != nil || count != 1 {
		t.Fatalf("quarantine source-key tombstone did not deduplicate: count=%d err=%v", count, err)
	}

	if _, err := s.db.ExecContext(ctx, "UPDATE quarantine SET created_at=? WHERE account_key=? AND source_key=?", now.Add(-31*24*time.Hour).UnixNano(), s.accountKey, q.SourceKey); err != nil {
		t.Fatal(err)
	}
	if err := s.Compact(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM quarantine WHERE account_key=? AND source_key=?", s.accountKey, q.SourceKey).Scan(&count); err != nil || count != 0 {
		t.Fatalf("expired quarantine tombstone retained: count=%d err=%v", count, err)
	}
}
