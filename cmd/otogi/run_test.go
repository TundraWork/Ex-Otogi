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
	cfg, err := loadConfig(writeConfig(t, "data_dir: "+t.TempDir()+"\nmanagement: {addr: \"127.0.0.1:0\"}\nmodules:\n  echo: {}\n"))
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

type fakeDriver struct{ applied, fetched int }

func (d *fakeDriver) Apply(context.Context, otogi.Effect) (otogi.EventRef, error) {
	d.applied++
	return otogi.EventRef{}, nil
}

func (d *fakeDriver) FetchMedia(context.Context, otogi.Destination, otogi.MediaSource) ([]byte, error) {
	d.fetched++
	return nil, nil
}

func TestRouterRoutesByPlatformAndAccount(t *testing.T) {
	main, other := &fakeDriver{}, &fakeDriver{}
	r := router{{"telegram", "main"}: main, {"telegram", "other"}: other}
	ctx := context.Background()
	if _, err := r.Apply(ctx, otogi.Effect{Kind: otogi.EffectSend, To: otogi.Destination{Platform: "telegram", Account: "main"}}); err != nil {
		t.Fatal(err)
	}
	// Non-send effects route by the message they apply to.
	if _, err := r.Apply(ctx, otogi.Effect{Kind: otogi.EffectDelete, Ref: otogi.EventRef{Platform: "telegram", Account: "other"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.FetchMedia(ctx, otogi.Destination{Platform: "telegram", Account: "main"}, otogi.MediaSource{}); err != nil {
		t.Fatal(err)
	}
	if main.applied != 1 || other.applied != 1 || main.fetched != 1 {
		t.Fatalf("main = %+v, other = %+v", main, other)
	}
	if _, err := r.Apply(ctx, otogi.Effect{Kind: otogi.EffectSend, To: otogi.Destination{Platform: "discord", Account: "main"}}); !errors.Is(err, otogi.ErrUnavailable) {
		t.Fatalf("unknown destination = %v", err)
	}
}
