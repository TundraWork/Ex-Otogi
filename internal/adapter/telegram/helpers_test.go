package telegram

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"go.uber.org/goleak"

	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// rpc answers Telegram RPCs for a tg.Client in tests.
type rpc func(ctx context.Context, request bin.Encoder) (bin.Encoder, error)

func (f rpc) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	result, err := f(ctx, input)
	if err != nil {
		return err
	}
	var buf bin.Buffer
	if err := result.Encode(&buf); err != nil {
		return err
	}
	return output.Decode(&buf)
}

func openStore(t *testing.T) *telegramstore.Store {
	t.Helper()
	s, err := telegramstore.Open(context.Background(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func encodeUpdate(t *testing.T, u tg.UpdateClass) []byte {
	t.Helper()
	var buf bin.Buffer
	if err := u.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Buf
}

func newMessage(id int, peer tg.PeerClass, text string) *tg.Message {
	m := &tg.Message{ID: id, PeerID: peer, Date: 100, Message: text}
	m.SetFromID(&tg.PeerUser{UserID: 5})
	return m
}

// submitter records submitted events and lets tests complete them.
type submitter struct {
	mu        sync.Mutex
	events    []otogi.Event
	callbacks []func(otogi.Outcome)
	submitted chan otogi.Event
	err       error
	auto      bool // complete immediately
}

func newSubmitter() *submitter { return &submitter{submitted: make(chan otogi.Event, 64)} }

func (s *submitter) Submit(_ context.Context, event otogi.Event, done func(otogi.Outcome)) error {
	s.mu.Lock()
	if s.err != nil {
		s.mu.Unlock()
		return s.err
	}
	s.events = append(s.events, event)
	s.callbacks = append(s.callbacks, done)
	auto := s.auto
	s.mu.Unlock()
	s.submitted <- event
	if auto {
		done(otogi.Outcome{EventID: event.ID})
	}
	return nil
}

func (s *submitter) complete(i int, outcome otogi.Outcome) {
	s.mu.Lock()
	done := s.callbacks[i]
	s.mu.Unlock()
	done(outcome)
}

func (s *submitter) next(t *testing.T) otogi.Event {
	t.Helper()
	select {
	case e := <-s.submitted:
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a submitted event")
		return otogi.Event{}
	}
}

// eventually polls cond until it holds or the deadline passes.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func pendingCount(t *testing.T, s *telegramstore.Store) int {
	t.Helper()
	rows, err := s.PendingInbox(context.Background(), 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return len(rows)
}

func unexpected(request bin.Encoder) error { return fmt.Errorf("unexpected RPC %T", request) }
