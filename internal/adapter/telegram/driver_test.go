package telegram

import (
	"context"
	"errors"
	"fmt"
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
	cfg.RunDispatcher = func(ctx context.Context, _ DriverStore, ready func()) error {
		claims <- struct{}{}
		ready()
		<-ctx.Done()
		return ctx.Err()
	}
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

func TestDriverImmediateDispatcherFailureFailsStart(t *testing.T) {
	for _, tt := range []struct {
		name string
		run  func(context.Context, DriverStore, func()) error
	}{
		{"error", func(context.Context, DriverStore, func()) error { return errors.New("dispatch failed") }},
		{"panic", func(context.Context, DriverStore, func()) error { panic("dispatch panic") }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validDriverConfig(t)
			cfg.RunDispatcher = tt.run
			d := NewDriver(cfg)
			if err := d.Start(context.Background()); err == nil {
				t.Fatal("Start returned nil")
			}
			if d.State() != StateFailed || d.Err() == nil || d.Ready() {
				t.Fatalf("state=%s err=%v", d.State(), d.Err())
			}
			_ = d.Stop(context.Background())
		})
	}
}

func TestDriverStopDeadlineKeepsLifecycleActiveAndClosesStoreAfterChild(t *testing.T) {
	cfg := validDriverConfig(t)
	store := &fakeDriverStore{ready: true}
	block := make(chan struct{})
	cfg.OpenStore = func(context.Context, StoreConfig) (DriverStore, error) { return store, nil }
	cfg.RunDispatcher = func(_ context.Context, _ DriverStore, ready func()) error { ready(); <-block; return nil }
	d := NewDriver(cfg)
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := d.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
	if d.State() != StateDraining || d.Ready() || store.closed {
		t.Fatalf("state=%s ready=%v closed=%v", d.State(), d.Ready(), store.closed)
	}
	close(block)
	select {
	case <-d.stopDone:
	case <-time.After(time.Second):
		t.Fatal("cleanup blocked")
	}
	if d.State() != StateStopped || !store.closed {
		t.Fatalf("state=%s closed=%v", d.State(), store.closed)
	}
}

func TestDriverStopClosesStorePublishedDuringCancellation(t *testing.T) {
	cfg := validDriverConfig(t)
	store := &fakeDriverStore{ready: true}
	entered := make(chan struct{})
	release := make(chan struct{})
	cfg.OpenStore = func(context.Context, StoreConfig) (DriverStore, error) { close(entered); <-release; return store, nil }
	d := NewDriver(cfg)
	start := make(chan error, 1)
	go func() { start <- d.Start(context.Background()) }()
	<-entered
	stop := make(chan error, 1)
	go func() { stop <- d.Stop(context.Background()) }()
	close(release)
	<-stop
	if !store.closed {
		t.Fatal("late-published store not closed")
	}
	<-start
}

func TestDriverStopWaitsForConcurrentDrainBeforeStoreClose(t *testing.T) {
	cfg := validDriverConfig(t)
	order := []string{}
	var mu sync.Mutex
	drainStarted := make(chan struct{})
	drainRelease := make(chan struct{})
	store := &fakeDriverStore{ready: true, onClose: func() { mu.Lock(); order = append(order, "close"); mu.Unlock() }}
	cfg.OpenStore = func(context.Context, StoreConfig) (DriverStore, error) { return store, nil }
	cfg.DrainDispatcher = func(ctx context.Context) error {
		close(drainStarted)
		<-drainRelease
		mu.Lock()
		order = append(order, "drain")
		mu.Unlock()
		return ctx.Err()
	}
	d := NewDriver(cfg)
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	drained := make(chan error, 1)
	go func() { drained <- d.Drain(context.Background()) }()
	<-drainStarted
	stopped := make(chan error, 1)
	go func() { stopped <- d.Stop(context.Background()) }()
	select {
	case <-stopped:
		t.Fatal("Stop passed active drain")
	case <-time.After(20 * time.Millisecond):
	}
	close(drainRelease)
	<-drained
	<-stopped
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(order) != "[drain close]" {
		t.Fatalf("order=%v", order)
	}
}

func TestDriverStopCancellationFinishesDrainBeforeCloseWithoutFailure(t *testing.T) {
	cfg := validDriverConfig(t)
	order := []string{}
	var mu sync.Mutex
	started := make(chan struct{})
	store := &fakeDriverStore{ready: true, onClose: func() { mu.Lock(); order = append(order, "close"); mu.Unlock() }}
	cfg.OpenStore = func(context.Context, StoreConfig) (DriverStore, error) { return store, nil }
	cfg.DrainDispatcher = func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		mu.Lock()
		order = append(order, "classified")
		mu.Unlock()
		return ctx.Err()
	}
	d := NewDriver(cfg)
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	drained := make(chan error, 1)
	go func() { drained <- d.Drain(context.Background()) }()
	<-started
	if err := d.Stop(context.Background()); err != nil {
		t.Fatalf("Stop error=%v", err)
	}
	if err := <-drained; err != nil {
		t.Fatalf("shutdown Drain error=%v", err)
	}
	if d.Err() != nil {
		t.Fatalf("lifecycle error=%v", d.Err())
	}
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(order) != "[classified close]" {
		t.Fatalf("order=%v", order)
	}
}

func TestDriverStopAcceptsCauseAwareDrainError(t *testing.T) {
	cfg := validDriverConfig(t)
	started := make(chan struct{})
	cfg.DrainDispatcher = func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return fmt.Errorf("drain canceled: %w", context.Cause(ctx))
	}
	d := NewDriver(cfg)
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	drained := make(chan error, 1)
	go func() { drained <- d.Drain(context.Background()) }()
	<-started
	if err := d.Stop(context.Background()); err != nil {
		t.Fatalf("Stop error=%v", err)
	}
	if err := <-drained; err != nil {
		t.Fatalf("shutdown Drain error=%v", err)
	}
	if d.Err() != nil {
		t.Fatalf("lifecycle error=%v", d.Err())
	}
}

func TestDriverCallerCanceledDrainFailsLifecycle(t *testing.T) {
	cfg := validDriverConfig(t)
	cfg.DrainDispatcher = func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
	d := NewDriver(cfg)
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := d.Drain(ctx)
	if !errors.Is(err, context.Canceled) || d.State() != StateFailed || !errors.Is(d.Err(), context.Canceled) {
		t.Fatalf("err=%v state=%s cause=%v", err, d.State(), d.Err())
	}
	_ = d.Stop(context.Background())
}

func TestDriverDrainRejectsNonStopErrors(t *testing.T) {
	drainFailed := errors.New("drain failed")
	tests := []struct {
		name  string
		ctx   func() context.Context
		drain func(context.Context) error
		want  error
	}{
		{
			name: "caller deadline",
			ctx: func() context.Context {
				ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				t.Cleanup(cancel)
				return ctx
			},
			drain: func(ctx context.Context) error { <-ctx.Done(); return context.Cause(ctx) },
			want:  context.DeadlineExceeded,
		},
		{
			name:  "arbitrary error",
			ctx:   context.Background,
			drain: func(context.Context) error { return drainFailed },
			want:  drainFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validDriverConfig(t)
			cfg.DrainDispatcher = tt.drain
			d := NewDriver(cfg)
			if err := d.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			err := d.Drain(tt.ctx())
			if !errors.Is(err, tt.want) || d.State() != StateFailed || !errors.Is(d.Err(), tt.want) {
				t.Fatalf("err=%v state=%s cause=%v", err, d.State(), d.Err())
			}
			_ = d.Stop(context.Background())
		})
	}
}

func TestDriverCallerCancellationWinsStopRace(t *testing.T) {
	cfg := validDriverConfig(t)
	callbackCanceled := make(chan struct{})
	release := make(chan struct{})
	allowClose := make(chan struct{})
	store := &fakeDriverStore{ready: true, onClose: func() { <-allowClose }}
	cfg.OpenStore = func(context.Context, StoreConfig) (DriverStore, error) { return store, nil }
	cfg.DrainDispatcher = func(ctx context.Context) error { <-ctx.Done(); close(callbackCanceled); <-release; return ctx.Err() }
	d := NewDriver(cfg)
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	drainCtx, cancel := context.WithCancel(context.Background())
	drained := make(chan error, 1)
	go func() { drained <- d.Drain(drainCtx) }()
	cancel()
	<-callbackCanceled
	stopped := make(chan error, 1)
	go func() { stopped <- d.Stop(context.Background()) }()
	deadline := time.After(time.Second)
	for {
		d.mu.Lock()
		stopping := d.stopping
		d.mu.Unlock()
		if stopping {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Stop did not publish stopping")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	close(release)
	if err := <-drained; !errors.Is(err, context.Canceled) {
		t.Fatalf("Drain error=%v", err)
	}
	if !errors.Is(d.Err(), context.Canceled) || d.State() != StateFailed {
		t.Fatalf("state=%s cause=%v", d.State(), d.Err())
	}
	close(allowClose)
	if err := <-stopped; !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop error=%v", err)
	}
	if d.State() != StateStopped || !errors.Is(d.Err(), context.Canceled) {
		t.Fatalf("final state=%s cause=%v", d.State(), d.Err())
	}
}

func TestDriverWaitsForManagerGateAndPropagatesGateFailure(t *testing.T) {
	t.Run("blocks until gate open", func(t *testing.T) {
		cfg := validDriverConfig(t)
		release := make(chan struct{})
		cfg.RunManager = func(ctx context.Context, _ ClientSession, _ DriverStore, ready func()) error {
			<-release
			ready()
			<-ctx.Done()
			return ctx.Err()
		}
		d := NewDriver(cfg)
		started := make(chan error, 1)
		go func() { started <- d.Start(context.Background()) }()
		select {
		case err := <-started:
			t.Fatalf("Start returned before gate: %v", err)
		case <-time.After(20 * time.Millisecond):
		}
		close(release)
		if err := <-started; err != nil {
			t.Fatal(err)
		}
		_ = d.Stop(context.Background())
	})
	t.Run("gate failure", func(t *testing.T) {
		want := errors.New("gate replay failed")
		cfg := validDriverConfig(t)
		cfg.RunManager = func(context.Context, ClientSession, DriverStore, func()) error { return want }
		d := NewDriver(cfg)
		if err := d.Start(context.Background()); !errors.Is(err, want) {
			t.Fatalf("error=%v", err)
		}
		_ = d.Stop(context.Background())
	})
}

func TestDriverStopAggregatesCleanupErrorAndClosesAfterChildren(t *testing.T) {
	cfg := validDriverConfig(t)
	order := []string{}
	mu := sync.Mutex{}
	store := &fakeDriverStore{ready: true, closeErr: errors.New("close failed"), onClose: func() { mu.Lock(); order = append(order, "close"); mu.Unlock() }}
	cfg.OpenStore = func(context.Context, StoreConfig) (DriverStore, error) { return store, nil }
	cfg.RunDispatcher = func(ctx context.Context, _ DriverStore, ready func()) error {
		ready()
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

func TestDriverNormalStopAcceptsNilClientRunResult(t *testing.T) {
	cfg := validDriverConfig(t)
	cfg.ClientFactory = fakeClientFactory{client: &fakeClientSession{nilOnCancel: true}}
	d := NewDriver(cfg)
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := d.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDriverStopContainsStoreClosePanic(t *testing.T) {
	cfg := validDriverConfig(t)
	store := &fakeDriverStore{ready: true, panicClose: "session-secret"}
	cfg.OpenStore = func(context.Context, StoreConfig) (DriverStore, error) { return store, nil }
	d := NewDriver(cfg)
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := d.Stop(context.Background())
	if err == nil || strings.Contains(err.Error(), "session-secret") {
		t.Fatalf("error=%v", err)
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
	runErr      error
	panicValue  any
	nilOnCancel bool
}

func (f *fakeClientSession) Run(ctx context.Context, fn func(context.Context) error) error {
	if f.panicValue != nil {
		panic(f.panicValue)
	}
	if f.runErr != nil {
		return f.runErr
	}
	err := fn(ctx)
	if f.nilOnCancel && errors.Is(err, context.Canceled) {
		return nil
	}
	return err
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
	panicClose any
}

func (s *fakeDriverStore) RecoveryReady(context.Context) (bool, []telegramstore.RecoveryRecord, error) {
	return s.ready, nil, nil
}
func (s *fakeDriverStore) Close() error {
	if s.panicClose != nil {
		panic(s.panicClose)
	}
	s.closeCount++
	s.closed = true
	if s.onClose != nil {
		s.onClose()
	}
	return s.closeErr
}
