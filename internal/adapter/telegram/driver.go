package telegram

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"

	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	gotdtelegram "github.com/gotd/td/telegram"
	gotdauth "github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
)

// Lifecycle errors reported by the Telegram driver.
var (
	ErrInvalidLifecycle = errors.New("telegram: invalid lifecycle transition")
	ErrDegraded         = errors.New("telegram: durable recovery is degraded")
)

var errStopDrain = errors.New("telegram: stop drain")

// State is the serialized driver lifecycle state.
type State string

// Driver lifecycle states.
const (
	StateNew      State = "new"
	StateStarting State = "starting"
	StateReady    State = "ready"
	StateDraining State = "draining"
	StateFailed   State = "failed"
	StateStopped  State = "stopped"
)

// ClientOptions are the gotd options assembled by the driver.
type ClientOptions = gotdtelegram.Options

// ClientSession is the narrow, mockable lifetime of one gotd client.
type ClientSession interface {
	// Run owns one client transport lifetime.
	Run(context.Context, func(context.Context) error) error
	// Authorized reports current session authorization.
	Authorized(context.Context) (bool, error)
	// Authorize runs the injected authorization flow.
	Authorize(context.Context, AuthFlow) error
	// Self returns the authorized Telegram user.
	Self(context.Context) (*tg.User, error)
}

// ClientFactory constructs exactly one inert client per Driver.
type ClientFactory interface {
	// New constructs exactly one inert client session.
	New(int, string, ClientOptions) ClientSession
}

// GotdClientFactory constructs the production gotd client.
type GotdClientFactory struct{}

// New constructs one inert gotd client session.
func (GotdClientFactory) New(appID int, appHash string, options ClientOptions) ClientSession {
	return gotdClientSession{client: gotdtelegram.NewClient(appID, appHash, options)}
}

type gotdClientSession struct{ client *gotdtelegram.Client }

func (c gotdClientSession) Run(ctx context.Context, fn func(context.Context) error) error {
	return c.client.Run(ctx, fn)
}
func (c gotdClientSession) Authorized(ctx context.Context) (bool, error) {
	s, e := c.client.Auth().Status(ctx)
	return e == nil && s.Authorized, e
}
func (c gotdClientSession) Authorize(ctx context.Context, flow AuthFlow) error {
	return flow.Run(ctx, c.client.Auth())
}
func (c gotdClientSession) Self(ctx context.Context) (*tg.User, error) { return c.client.Self(ctx) }

// Driver owns one non-restartable gotd client and all adapter children.
type Driver struct {
	cfg            Config
	mu             sync.Mutex
	state          State
	cause          error
	client         ClientSession
	store          DriverStore
	stopping       bool
	lifeCancel     context.CancelFunc
	dispatchCancel context.CancelFunc
	done           chan struct{}
	startResult    chan error
	dispatchDone   <-chan error
	childErrs      []error
	stopOnce       sync.Once
	children       sync.WaitGroup
	drainDone      chan struct{}
	drainCancel    context.CancelCauseFunc
	stopDone       chan struct{}
	stopErr        error
}

// NewDriver constructs an inert, one-shot Telegram driver.
func NewDriver(cfg Config) *Driver {
	drainDone := make(chan struct{})
	close(drainDone)
	return &Driver{cfg: cfg, state: StateNew, done: make(chan struct{}), startResult: make(chan error, 1), stopDone: make(chan struct{}), drainDone: drainDone}
}

// State reports the current serialized lifecycle state.
func (d *Driver) State() State { d.mu.Lock(); defer d.mu.Unlock(); return d.state }

// Ready reports whether the driver can currently serve its contract.
func (d *Driver) Ready() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return !d.stopping && d.state == StateReady
}

// Err reports the most recent lifecycle failure.
func (d *Driver) Err() error { d.mu.Lock(); defer d.mu.Unlock(); return d.cause }

// Done closes after the client and all owned children exit.
func (d *Driver) Done() <-chan struct{} { return d.done }

// Start performs bounded initialization once and waits until readiness or failure.
func (d *Driver) Start(ctx context.Context) error {
	d.mu.Lock()
	if d.state != StateNew {
		d.mu.Unlock()
		return ErrInvalidLifecycle
	}
	d.state = StateStarting
	d.mu.Unlock()
	if err := d.cfg.Validate(); err != nil {
		d.failStartup(err)
		return err
	}
	if err := ValidateSessionPath(d.cfg.SessionPath); err != nil {
		d.failStartup(err)
		return err
	}
	storage, err := NewSessionStorage(d.cfg.SessionPath)
	if err != nil {
		d.failStartup(err)
		return err
	}
	options := gotdtelegram.Options{SessionStorage: storage, Device: d.cfg.Device}
	if err := d.cfg.ConfigureClientOptions(&options); err != nil {
		d.failStartup(fmt.Errorf("configure Telegram updates: %w", err))
		return err
	}
	client := d.cfg.ClientFactory.New(d.cfg.AppID, d.cfg.AppHash, options)
	life, cancel := context.WithCancel(context.WithoutCancel(ctx))
	d.mu.Lock()
	if d.state != StateStarting || d.stopping {
		d.mu.Unlock()
		cancel()
		return ErrInvalidLifecycle
	}
	d.client, d.lifeCancel = client, cancel
	d.mu.Unlock()
	go d.supervise(life)
	select {
	case err := <-d.startResult:
		return err
	case <-ctx.Done():
		cancel()
		d.fail(ctx.Err())
		return ctx.Err()
	}
}

func (d *Driver) supervise(ctx context.Context) {
	err := panicError("client", func() error { return d.client.Run(ctx, d.runConnected) })
	d.children.Wait()
	d.mu.Lock()
	stopping := d.stopping || errors.Is(ctx.Err(), context.Canceled)
	starting := d.state == StateStarting
	if err == nil && !stopping {
		err = errors.New("telegram: client stopped unexpectedly")
	}
	if !stopping && (d.state == StateStarting || d.state == StateReady || d.state == StateDraining) {
		d.state = StateFailed
		if d.cause == nil {
			d.cause = err
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			d.childErrs = append(d.childErrs, err)
		}
	}
	d.mu.Unlock()
	if starting && err != nil {
		d.signalStart(err)
	}
	close(d.done)
}
func (d *Driver) runConnected(ctx context.Context) error {
	authorized, err := d.client.Authorized(ctx)
	if err != nil {
		return d.startFailed(fmt.Errorf("authorize status: %w", err))
	}
	if !authorized {
		if err = d.client.Authorize(ctx, NewAuthFlow(d.cfg.Authenticator)); err != nil {
			return d.startFailed(fmt.Errorf("authorize Telegram account: %w", err))
		}
	}
	self, err := d.client.Self(ctx)
	if err != nil {
		return d.startFailed(fmt.Errorf("load authorized account: %w", err))
	}
	store, err := d.cfg.OpenStore(ctx, telegramstore.Config{Path: d.cfg.DatabasePath, AccountKey: d.cfg.AccountKey, AuthorizedUserID: self.ID, GotdVersion: "v0.161.0", TGLayer: tg.Layer})
	if err != nil {
		return d.startFailed(fmt.Errorf("open Telegram store: %w", err))
	}
	d.mu.Lock()
	d.store = store
	d.mu.Unlock()
	if err = panicError("resynchronizer", func() error { return d.cfg.RunResynchronizer(ctx, d.client, store) }); err != nil {
		return d.startFailed(err)
	}
	managerReady := make(chan struct{})
	var readyOnce sync.Once
	managerResult := d.runChild(ctx, "manager", func(child context.Context) error {
		return d.cfg.RunManager(child, d.client, store, func() { readyOnce.Do(func() { close(managerReady) }) })
	})
	select {
	case <-managerReady:
	case err := <-managerResult:
		if err == nil {
			err = errors.New("telegram: manager stopped before ready")
		}
		return d.startFailed(err)
	case <-ctx.Done():
		return d.startFailed(ctx.Err())
	}
	ready, _, err := store.RecoveryReady(ctx)
	if err != nil {
		return d.startFailed(fmt.Errorf("read recovery readiness: %w", err))
	}
	if !ready {
		return d.startFailed(ErrDegraded)
	}
	dispatchCtx, dispatchCancel := context.WithCancel(ctx)
	dispatchReady := make(chan struct{})
	var dispatchReadyOnce sync.Once
	d.mu.Lock()
	d.dispatchCancel = dispatchCancel
	d.mu.Unlock()
	d.dispatchDone = d.runChild(dispatchCtx, "dispatcher", func(child context.Context) error {
		return d.cfg.RunDispatcher(child, store, func() { dispatchReadyOnce.Do(func() { close(dispatchReady) }) })
	})
	select {
	case <-dispatchReady:
		d.mu.Lock()
		if d.state != StateStarting {
			err = d.cause
			d.mu.Unlock()
			if err == nil {
				err = errors.New("telegram: dispatcher failed before ready")
			}
			return d.startFailed(err)
		}
		d.state = StateReady
		d.mu.Unlock()
		d.signalStart(nil)
	case err = <-d.dispatchDone:
		return d.startFailed(err)
	case <-ctx.Done():
		return d.startFailed(ctx.Err())
	}
	<-ctx.Done()
	return ctx.Err()
}

func (d *Driver) runChild(ctx context.Context, name string, fn func(context.Context) error) <-chan error {
	result := make(chan error, 1)
	d.children.Add(1)
	go func() {
		defer d.children.Done()
		err := panicError(name, func() error { return fn(ctx) })
		if err == nil {
			err = fmt.Errorf("telegram: %s stopped unexpectedly", name)
		}
		result <- err
		if !errors.Is(err, context.Canceled) {
			d.fail(err)
			d.mu.Lock()
			cancel := d.lifeCancel
			d.mu.Unlock()
			if cancel != nil {
				cancel()
			}
		}
	}()
	return result
}
func panicError(name string, fn func() error) (err error) {
	defer func() {
		if recover() != nil {
			digest := sha256.Sum256(debug.Stack())
			err = fmt.Errorf("telegram %s panic (stack %x)", name, digest[:8])
		}
	}()
	return fn()
}
func (d *Driver) signalStart(err error) {
	select {
	case d.startResult <- err:
	default:
	}
}
func (d *Driver) startFailed(err error) error {
	d.fail(err)
	d.signalStart(err)
	if d.lifeCancel != nil {
		d.lifeCancel()
	}
	return err
}
func (d *Driver) failStartup(err error) {
	d.mu.Lock()
	d.state = StateFailed
	d.cause = err
	d.mu.Unlock()
	d.signalStart(err)
}
func (d *Driver) fail(err error) {
	d.mu.Lock()
	if d.state != StateStopped {
		d.state = StateFailed
		d.cause = err
		d.childErrs = append(d.childErrs, err)
	}
	d.mu.Unlock()
}

// Drain stops new dispatcher claims while update capture continues.
func (d *Driver) Drain(ctx context.Context) (err error) {
	d.mu.Lock()
	if d.state != StateReady || d.stopping {
		d.mu.Unlock()
		return ErrInvalidLifecycle
	}
	d.state = StateDraining
	drainCtx, cancel := context.WithCancelCause(ctx)
	d.drainCancel = cancel
	d.drainDone = make(chan struct{})
	done := d.drainDone
	d.mu.Unlock()
	defer func() { cancel(nil); close(done) }()
	err = panicError("dispatcher drain", func() error { return d.cfg.DrainDispatcher(drainCtx) })
	if err == nil {
		return nil
	}
	drainCause := context.Cause(drainCtx)
	if errors.Is(drainCause, errStopDrain) && (errors.Is(err, context.Canceled) || errors.Is(err, drainCause)) {
		return nil
	}
	d.fail(err)
	return err
}

// Stop cancels children, waits within ctx, closes storage last, and always becomes stopped.
func (d *Driver) Stop(ctx context.Context) error {
	d.stopOnce.Do(func() {
		d.mu.Lock()
		d.stopping = true
		if d.state == StateStarting || d.state == StateReady || d.state == StateDraining {
			d.state = StateDraining
		}
		cancel, started, drainCancel := d.lifeCancel, d.client != nil, d.drainCancel
		d.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if drainCancel != nil {
			drainCancel(errStopDrain)
		}
		go func() {
			var errs []error
			if started {
				<-d.done
			}
			d.mu.Lock()
			drainDone := d.drainDone
			d.mu.Unlock()
			<-drainDone
			d.mu.Lock()
			store := d.store
			d.mu.Unlock()
			if store != nil {
				if err := panicError("store close", store.Close); err != nil {
					errs = append(errs, err)
				}
			}
			d.mu.Lock()
			errs = append(errs, d.childErrs...)
			d.state = StateStopped
			d.stopErr = errors.Join(errs...)
			d.mu.Unlock()
			close(d.stopDone)
		}()
	})
	select {
	case <-d.stopDone:
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.stopErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

var _ gotdauth.FlowClient = (*gotdauth.Client)(nil)
