package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestRunStopsCleanlyOnCancel(t *testing.T) {
	cfg, err := loadConfig(writeConfig(t, "modules:\n  echo: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	running := make(chan struct{})
	log := slog.New(&signalHandler{Handler: slog.NewTextHandler(io.Discard, nil), message: "otogi: running", seen: running})
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg, log) }()
	<-running
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not stop")
	}
}

// signalHandler closes seen when a record with message is logged.
type signalHandler struct {
	slog.Handler
	message string
	seen    chan struct{}
	once    sync.Once
}

func (h *signalHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Message == h.message {
		h.once.Do(func() { close(h.seen) })
	}
	return h.Handler.Handle(ctx, r)
}

type messengerFunc func(context.Context, otogi.OutboundMessage) (otogi.EventRef, error)

func (f messengerFunc) Send(ctx context.Context, msg otogi.OutboundMessage) (otogi.EventRef, error) {
	return f(ctx, msg)
}

func TestRouterRoutesByPlatformAndAccount(t *testing.T) {
	var got otogi.AccountID
	r := router{{"telegram", "main"}: messengerFunc(func(_ context.Context, m otogi.OutboundMessage) (otogi.EventRef, error) {
		got = m.Account
		return otogi.EventRef{}, nil
	})}
	if _, err := r.Send(context.Background(), otogi.OutboundMessage{Platform: "telegram", Account: "main"}); err != nil || got != "main" {
		t.Fatalf("Send = %v, routed to %q", err, got)
	}
	if _, err := r.Send(context.Background(), otogi.OutboundMessage{Platform: "telegram", Account: "other"}); !errors.Is(err, otogi.ErrUnavailable) {
		t.Fatalf("unknown destination = %v", err)
	}
}
