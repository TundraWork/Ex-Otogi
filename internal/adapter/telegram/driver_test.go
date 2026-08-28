package telegram

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	gotdtelegram "github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestDriverStartReadyDrainStopAndOneShot(t *testing.T) {
	cfg := validDriverConfig(t)
	store := &fakeDriverStore{ready: true}
	cfg.OpenStore = func(context.Context, StoreConfig) (DriverStore, error) { return store, nil }
	claims := make(chan struct{}, 1)
	cfg.RunDispatcher = func(ctx context.Context, _ DriverStore) error { claims <- struct{}{}; <-ctx.Done(); return ctx.Err() }
	drained := false
	cfg.DrainDispatcher = func(context.Context) error { drained = true; return nil }
	d := NewDriver(cfg)
	if d.State() != StateNew {
		t.Fatalf("state = %s", d.State())
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d.State() != StateReady || !d.Ready() {
		t.Fatalf("state=%s ready=%v", d.State(), d.Ready())
	}
	select {
	case <-claims:
	case <-time.After(time.Second):
		t.Fatal("dispatcher not started")
	}
	if err := d.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d.State() != StateDraining || d.Ready() {
		t.Fatalf("state=%s ready=%v", d.State(), d.Ready())
	}
	if !drained || store.closed {
		t.Fatalf("drained=%v closed=%v", drained, store.closed)
	}
	if err := d.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d.State() != StateStopped || !store.closed {
		t.Fatalf("state=%s closed=%v", d.State(), store.closed)
	}
	if err := d.Stop(context.Background()); err != nil || store.closeCount != 1 {
		t.Fatalf("second Stop: err=%v closes=%d", err, store.closeCount)
	}
	if err := d.Start(context.Background()); !errors.Is(err, ErrInvalidLifecycle) {
		t.Fatalf("restart error=%v", err)
	}
}

func TestDriverDegradedRecoveryNeverBecomesReady(t *testing.T) {
	cfg := validDriverConfig(t)
	cfg.OpenStore = func(context.Context, StoreConfig) (DriverStore, error) { return &fakeDriverStore{ready: false}, nil }
	d := NewDriver(cfg)
	err := d.Start(context.Background())
	if !errors.Is(err, ErrDegraded) || d.State() != StateFailed || d.Ready() {
		t.Fatalf("err=%v state=%s ready=%v", err, d.State(), d.Ready())
	}
	_ = d.Stop(context.Background())
}

func TestDriverChildFailureAndPanicFailLifecycle(t *testing.T) {
	for _, tt := range []struct {
		name string
		run  func(context.Context, DriverStore) error
	}{
		{"error", func(context.Context, DriverStore) error { return errors.New("dispatch failed") }},
		{"panic", func(context.Context, DriverStore) error { panic("dispatch panic") }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validDriverConfig(t)
			cfg.RunDispatcher = tt.run
			d := NewDriver(cfg)
			if err := d.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-d.Done():
			case <-time.After(time.Second):
				t.Fatal("failure not propagated")
			}
			if d.State() != StateFailed || d.Err() == nil || d.Ready() {
				t.Fatalf("state=%s err=%v", d.State(), d.Err())
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = d.Stop(ctx)
		})
	}
}

func TestDriverStopDeadlineStillClosesStoreLastAndStops(t *testing.T) {
	cfg := validDriverConfig(t)
	store := &fakeDriverStore{ready: true}
	block := make(chan struct{})
	cfg.OpenStore = func(context.Context, StoreConfig) (DriverStore, error) { return store, nil }
	cfg.RunDispatcher = func(context.Context, DriverStore) error { <-block; return nil }
	d := NewDriver(cfg)
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := d.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop error=%v", err)
	}
	if d.State() != StateStopped || store.closed {
		t.Fatalf("state=%s closed-before-child=%v", d.State(), store.closed)
	}
	close(block)
	select {
	case <-d.stopDone:
	case <-time.After(time.Second):
		t.Fatal("background cleanup did not finish")
	}
	if !store.closed {
		t.Fatal("store not closed after child exited")
	}
}

func TestDriverStopAggregatesCleanupErrorAndClosesAfterChildren(t *testing.T) {
	cfg := validDriverConfig(t)
	order := []string{}
	mu := sync.Mutex{}
	store := &fakeDriverStore{ready: true, closeErr: errors.New("close failed"), onClose: func() { mu.Lock(); order = append(order, "close"); mu.Unlock() }}
	cfg.OpenStore = func(context.Context, StoreConfig) (DriverStore, error) { return store, nil }
	cfg.RunDispatcher = func(ctx context.Context, _ DriverStore) error {
		<-ctx.Done()
		mu.Lock()
		order = append(order, "child")
		mu.Unlock()
		return errors.New("child cleanup")
	}
	d := NewDriver(cfg)
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := d.Stop(context.Background())
	if err == nil || d.State() != StateStopped {
		t.Fatalf("err=%v state=%s", err, d.State())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "child" || order[1] != "close" {
		t.Fatalf("order=%v", order)
	}
}

func TestDriverClientFailureBeforeCallbackFailsStart(t *testing.T) {
	for _, tt := range []struct {
		name       string
		runErr     error
		panicValue any
	}{
		{name: "error", runErr: errors.New("connect failed")},
		{name: "panic", panicValue: "session-secret"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeClientSession{runErr: tt.runErr, panicValue: tt.panicValue}
			cfg := validDriverConfig(t)
			cfg.ClientFactory = fakeClientFactory{client: client}
			d := NewDriver(cfg)
			err := d.Start(context.Background())
			if err == nil || d.State() != StateFailed {
				t.Fatalf("err=%v state=%s", err, d.State())
			}
			if strings.Contains(err.Error(), "session-secret") {
				t.Fatalf("panic leaked secret: %v", err)
			}
		})
	}
}

func TestDriverConfiguresUpdatesBeforeClientConstruction(t *testing.T) {
	cfg := validDriverConfig(t)
	configured := false
	cfg.ConfigureClientOptions = func(options *ClientOptions) error {
		configured = true
		options.UpdateHandler = gotdtelegram.UpdateHandlerFunc(func(context.Context, tg.UpdatesClass) error { return nil })
		return nil
	}
	factory := &observingClientFactory{configured: &configured}
	cfg.ClientFactory = factory
	d := NewDriver(cfg)
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !factory.sawUpdateHandler {
		t.Fatal("client constructed before update handler installed")
	}
	_ = d.Stop(context.Background())
}

type observingClientFactory struct {
	configured       *bool
	sawUpdateHandler bool
}

func (f *observingClientFactory) New(_ int, _ string, options ClientOptions) ClientSession {
	f.sawUpdateHandler = *f.configured && options.UpdateHandler != nil && options.SessionStorage != nil
	return &fakeClientSession{}
}

type fakeClientFactory struct{ client *fakeClientSession }

func (f fakeClientFactory) New(int, string, ClientOptions) ClientSession {
	if f.client != nil {
		return f.client
	}
	return &fakeClientSession{}
}

type fakeClientSession struct {
	runErr     error
	panicValue any
}

func (f *fakeClientSession) Run(ctx context.Context, fn func(context.Context) error) error {
	if f.panicValue != nil {
		panic(f.panicValue)
	}
	if f.runErr != nil {
		return f.runErr
	}
	return fn(ctx)
}
func (*fakeClientSession) Authorized(context.Context) (bool, error)  { return true, nil }
func (*fakeClientSession) Authorize(context.Context, AuthFlow) error { return nil }
func (*fakeClientSession) Self(context.Context) (*tg.User, error)    { return &tg.User{ID: 42}, nil }

type fakeDriverStore struct {
	ready      bool
	closed     bool
	closeCount int
	closeErr   error
	onClose    func()
}

func (s *fakeDriverStore) RecoveryReady(context.Context) (bool, []telegramstore.RecoveryRecord, error) {
	return s.ready, nil, nil
}
func (s *fakeDriverStore) Close() error {
	s.closeCount++
	s.closed = true
	if s.onClose != nil {
		s.onClose()
	}
	return s.closeErr
}
