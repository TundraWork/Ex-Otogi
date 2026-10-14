package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/updates"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "account", "state.sqlite")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func TestOpenConfiguresSQLiteAndReopens(t *testing.T) {
	ctx := context.Background()
	s, path := open(t)
	for pragma, want := range map[string]string{"journal_mode": "wal", "synchronous": "1", "foreign_keys": "1", "busy_timeout": "5000"} {
		var got string
		if err := s.db.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got); err != nil || got != want {
			t.Errorf("PRAGMA %s = %q, %v; want %q", pragma, got, err, want)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	_ = reopened.Close()
}

func TestBind(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	if err := s.Bind(ctx, "main", 42); err != nil {
		t.Fatal(err)
	}
	if err := s.Bind(ctx, "main", 42); err != nil {
		t.Fatalf("rebinding same account: %v", err)
	}
	for _, other := range []struct {
		account string
		user    int64
	}{{"other", 42}, {"main", 7}} {
		if err := s.Bind(ctx, other.account, other.user); !errors.Is(err, ErrAccountMismatch) {
			t.Errorf("Bind(%q, %d) = %v, want ErrAccountMismatch", other.account, other.user, err)
		}
	}
}

func TestUpdateState(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	if _, found, err := s.GetState(ctx, 1); found || err != nil {
		t.Fatalf("empty GetState found=%v err=%v", found, err)
	}
	// Individual setters work even before SetState.
	if err := s.SetPts(ctx, 1, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.SetState(ctx, 1, updates.State{Pts: 10, Qts: 2, Date: 3, Seq: 4}); err != nil {
		t.Fatal(err)
	}
	for _, set := range []func() error{
		func() error { return s.SetPts(ctx, 1, 11) },
		func() error { return s.SetQts(ctx, 1, 12) },
		func() error { return s.SetDateSeq(ctx, 1, 13, 14) },
	} {
		if err := set(); err != nil {
			t.Fatal(err)
		}
	}
	got, found, err := s.GetState(ctx, 1)
	if err != nil || !found || got != (updates.State{Pts: 11, Qts: 12, Date: 13, Seq: 14}) {
		t.Fatalf("GetState = %+v, %v, %v", got, found, err)
	}
}

func TestChannelStateAndAccessHashes(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	for id, pts := range map[int64]int{100: 1, 200: 2} {
		if err := s.SetChannelPts(ctx, 1, id, pts); err != nil {
			t.Fatal(err)
		}
		if err := s.SetChannelAccessHash(ctx, 1, id, id*10); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[int64]int{}
	// The callback queries the store, as gotd does; this must not deadlock on
	// the single connection.
	err := s.ForEachChannels(ctx, 1, func(ctx context.Context, id int64, pts int) error {
		if hash, found, err := s.GetChannelAccessHash(ctx, 1, id); err != nil || !found || hash != id*10 {
			t.Errorf("channel %d hash = %d, %v, %v", id, hash, found, err)
		}
		seen[id] = pts
		return nil
	})
	if err != nil || len(seen) != 2 || seen[100] != 1 || seen[200] != 2 {
		t.Fatalf("ForEachChannels saw %v, %v", seen, err)
	}
	// The update manager's hashers and gotd's peers.Manager share one table.
	if err := s.SetUserAccessHash(ctx, 1, 7, 70); err != nil {
		t.Fatal(err)
	}
	if v, found, err := s.Find(ctx, peers.Key{Prefix: usersPrefix, ID: 7}); err != nil || !found || v.AccessHash != 70 {
		t.Fatalf("peers.Find user = %+v, %v, %v", v, found, err)
	}
}

func TestPeersStorage(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	key := peers.Key{Prefix: usersPrefix, ID: 9}
	if err := s.Save(ctx, key, peers.Value{AccessHash: 90}); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePhone(ctx, "+100", key); err != nil {
		t.Fatal(err)
	}
	gotKey, value, found, err := s.FindPhone(ctx, "+100")
	if err != nil || !found || gotKey != key || value.AccessHash != 90 {
		t.Fatalf("FindPhone = %+v, %+v, %v, %v", gotKey, value, found, err)
	}
	if hash, err := s.GetContactsHash(ctx); err != nil || hash != 0 {
		t.Fatalf("empty contacts hash = %d, %v", hash, err)
	}
	if err := s.SaveContactsHash(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if hash, err := s.GetContactsHash(ctx); err != nil || hash != 5 {
		t.Fatalf("contacts hash = %d, %v", hash, err)
	}
}

func TestInbox(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	now := time.Unix(1000, 0)
	for _, key := range []string{"a", "b", "a", "c"} {
		if _, err := s.InsertInbox(ctx, key, []byte(key), now); err != nil {
			t.Fatal(err)
		}
	}
	if inserted, err := s.InsertInbox(ctx, "b", []byte("again"), now); err != nil || inserted {
		t.Fatal("duplicate dedup key inserted")
	}
	rows, err := s.PendingInbox(ctx, 0, 10)
	if err != nil || len(rows) != 3 || string(rows[0].Payload) != "a" || string(rows[2].Payload) != "c" {
		t.Fatalf("pending = %+v, %v", rows, err)
	}
	if err := s.MarkInboxDone(ctx, rows[0].ID, now); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkInboxFailed(ctx, rows[1].ID, "bad mapping", now); err != nil {
		t.Fatal(err)
	}
	pending, _ := s.PendingInbox(ctx, 0, 10)
	if len(pending) != 1 || pending[0].ID != rows[2].ID {
		t.Fatalf("pending after finishing two = %+v", pending)
	}
	if after, _ := s.PendingInbox(ctx, rows[2].ID, 10); len(after) != 0 {
		t.Fatalf("pending after last ID = %+v", after)
	}
}

func TestRecordsAndPrune(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	old, now := time.Unix(0, 0), time.Unix(0, 0).Add(LocatorRetention+time.Hour)

	if err := s.PutLocator(ctx, "common", 5, "user:1", old); err != nil {
		t.Fatal(err)
	}
	if conv, found, err := s.FindLocator(ctx, "common", 5); err != nil || !found || conv != "user:1" {
		t.Fatalf("FindLocator = %q, %v, %v", conv, found, err)
	}
	if err := s.PutSent(ctx, Sent{Key: "k", Conversation: "user:1", MessageID: 9}, old); err != nil {
		t.Fatal(err)
	}
	if sent, found, err := s.FindSent(ctx, "k"); err != nil || !found || sent.MessageID != 9 {
		t.Fatalf("FindSent = %+v, %v, %v", sent, found, err)
	}
	if err := s.RecordGap(ctx, "too_long", 0, old); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"done", "pending"} {
		if _, err := s.InsertInbox(ctx, key, []byte(key), old); err != nil {
			t.Fatal(err)
		}
	}
	rows, _ := s.PendingInbox(ctx, 0, 10)
	_ = s.MarkInboxDone(ctx, rows[0].ID, old)

	if err := s.Prune(ctx, now); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.FindLocator(ctx, "common", 5); found {
		t.Error("old locator survived prune")
	}
	if _, found, _ := s.FindSent(ctx, "k"); found {
		t.Error("old send survived prune")
	}
	if pending, _ := s.PendingInbox(ctx, 0, 10); len(pending) != 1 {
		t.Errorf("prune touched pending rows: %+v", pending)
	}
	var finished int
	_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM inbox WHERE state = 'done'").Scan(&finished)
	if finished != 0 {
		t.Error("old done inbox row survived prune")
	}
}
