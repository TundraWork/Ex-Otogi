// Package telegram is the Telegram driver. It logs in as a user account over
// MTProto (gotd), persists routed updates in a durable SQLite inbox before
// gotd advances its position, dispatches them to the kernel, and sends
// messages exactly once per idempotency key.
package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"

	"github.com/gotd/td/session"
	gotdtelegram "github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
	"golang.org/x/sync/errgroup"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/mapper"
	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Platform is the platform ID of Telegram events.
const Platform otogi.PlatformID = "telegram"

// Config configures one Telegram account.
type Config struct {
	// AppID and AppHash identify the Telegram application (my.telegram.org).
	AppID   int
	AppHash string
	// AccountKey names this account in the deployment; events carry it in
	// their account ID, and the database is bound to it.
	AccountKey string
	// SessionPath is the gotd session file; it holds the login.
	SessionPath string
	// DatabasePath is the account's SQLite database.
	DatabasePath string
	// Authenticator supplies credentials when the session is not logged in.
	// If nil, an unauthorized session is a startup error.
	Authenticator Authenticator
	// Kernel receives events.
	Kernel Submitter
	// Logger receives driver logs; nil means slog.Default().
	Logger *slog.Logger
}

// client is the part of gotd's *telegram.Client the driver uses.
type client interface {
	Run(ctx context.Context, f func(context.Context) error) error
	API() *tg.Client
	Auth() *auth.Client
	Self(ctx context.Context) (*tg.User, error)
}

// Driver runs one Telegram account. It implements otogi.Messenger.
type Driver struct {
	cfg       Config
	log       *slog.Logger
	newClient func(gotdtelegram.Options) client

	mu        sync.Mutex
	started   bool
	ready     bool
	sender    *sender
	cancel    context.CancelFunc
	drain     context.CancelFunc
	drained   chan struct{}
	readyCh   chan struct{}
	readyOnce sync.Once
	done      chan struct{}
	err       error
}

// New returns a driver that has not started.
func New(cfg Config) *Driver {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	d := &Driver{cfg: cfg, log: cfg.Logger.With("driver", "telegram", "account", cfg.AccountKey), readyCh: make(chan struct{}), done: make(chan struct{}), drained: make(chan struct{})}
	d.newClient = func(opts gotdtelegram.Options) client { return gotdtelegram.NewClient(cfg.AppID, cfg.AppHash, opts) }
	return d
}

// Start opens the database, connects, logs in if needed, and returns once
// the driver receives updates, or with the reason it could not.
func (d *Driver) Start(ctx context.Context) error {
	d.mu.Lock()
	if d.started {
		d.mu.Unlock()
		return fmt.Errorf("telegram: already started: %w", otogi.ErrUnavailable)
	}
	d.started = true
	d.mu.Unlock()

	store, err := telegramstore.Open(ctx, d.cfg.DatabasePath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(d.cfg.SessionPath), 0o700); err != nil {
		_ = store.Close()
		return fmt.Errorf("telegram: create session directory: %w", err)
	}

	failures := make(chan error, 1)
	wake := make(chan struct{}, 1)
	guard := &positionGuard{StateStorage: store}
	handler := &inboxHandler{
		store: store, guard: guard, wake: wake, log: d.log, now: time.Now,
		fail: func(err error) {
			select {
			case failures <- err:
			default:
			}
		},
	}
	manager := updates.New(updates.Config{
		Handler:                  handler,
		Storage:                  guard,
		AccessHasher:             store,
		UserAccessHasher:         store,
		OnTooLong:                func() { d.recordGap(store, "too_long", 0) },
		OnChannelTooLong:         func(id int64) { d.recordGap(store, "channel_too_long", id) },
		OnChannelInaccessible:    func(id int64) { d.recordGap(store, "channel_inaccessible", id) },
		OnLoadUserStateFailed:    func() { d.recordGap(store, "no_saved_state", 0) },
		OnLoadChannelStateFailed: func(id int64) { d.recordGap(store, "no_saved_channel_state", id) },
	})
	c := d.newClient(gotdtelegram.Options{UpdateHandler: manager, SessionStorage: &session.FileStorage{Path: d.cfg.SessionPath}})
	handler.peers = peers.Options{Storage: store}.Build(c.API())

	runCtx, cancel := context.WithCancel(context.Background())
	d.mu.Lock()
	d.cancel = cancel
	d.mu.Unlock()
	go func() {
		err := c.Run(runCtx, func(ctx context.Context) error {
			return d.connected(ctx, c, store, manager, handler, wake, failures)
		})
		d.finish(runCtx, store, err)
	}()

	select {
	case <-d.readyCh:
		return nil
	case <-d.done:
		return d.Err()
	case <-ctx.Done():
		cancel()
		<-d.done
		return ctx.Err()
	}
}

// connected runs once the client is connected: log in, bind the database to
// the account, then run the update manager, dispatcher, and pruner until one
// fails or ctx ends.
func (d *Driver) connected(ctx context.Context, c client, store *telegramstore.Store, manager *updates.Manager, handler *inboxHandler, wake chan struct{}, failures <-chan error) error {
	if err := d.authorize(ctx, c); err != nil {
		return err
	}
	self, err := c.Self(ctx)
	if err != nil {
		return fmt.Errorf("telegram: load own account: %w", err)
	}
	if err := store.Bind(ctx, d.cfg.AccountKey, self.ID); err != nil {
		return err
	}
	m := mapper.New(mapper.Config{Platform: Platform, Account: otogi.AccountID(d.cfg.AccountKey), AuthorizedUserID: self.ID})
	d.mu.Lock()
	d.sender = &sender{api: c.API(), store: store, resolve: peerResolver(handler.peers), updates: manager, ref: m.MessageRef, log: d.log, now: time.Now}
	d.mu.Unlock()

	g, ctx := errgroup.WithContext(ctx)
	dispatchCtx, stopDispatch := context.WithCancel(ctx)
	d.mu.Lock()
	d.drain = stopDispatch
	d.mu.Unlock()
	g.Go(guarded("update manager", func() error {
		return manager.Run(ctx, c.API(), self.ID, updates.AuthOptions{OnStart: func(context.Context) { d.markReady() }})
	}))
	g.Go(guarded("dispatcher", func() error {
		defer close(d.drained)
		dispatcher := &dispatcher{store: store, mapper: m, kernel: d.cfg.Kernel, wake: wake, poll: time.Second, log: d.log, now: time.Now, pending: map[int64]*rowProgress{}}
		return dispatcher.run(dispatchCtx)
	}))
	g.Go(guarded("pruner", func() error { return prune(ctx, store, d.log) }))
	g.Go(func() error {
		select {
		case err := <-failures:
			return err
		case <-ctx.Done():
			return nil
		}
	})
	return g.Wait()
}

func (d *Driver) authorize(ctx context.Context, c client) error {
	status, err := c.Auth().Status(ctx)
	if err != nil {
		return fmt.Errorf("telegram: check login: %w", err)
	}
	if status.Authorized {
		return nil
	}
	if d.cfg.Authenticator == nil {
		return errors.New("telegram: session is not logged in and no authenticator is configured")
	}
	d.log.Info("telegram: logging in")
	if err := auth.NewFlow(userAuthenticator{d.cfg.Authenticator}, auth.SendCodeOptions{}).Run(ctx, c.Auth()); err != nil {
		return fmt.Errorf("telegram: log in: %w", err)
	}
	return nil
}

func (d *Driver) markReady() {
	d.readyOnce.Do(func() {
		d.mu.Lock()
		d.ready = true
		d.mu.Unlock()
		d.log.Info("telegram: ready")
		close(d.readyCh)
	})
}

func (d *Driver) finish(runCtx context.Context, store *telegramstore.Store, err error) {
	if runCtx.Err() != nil && (err == nil || errors.Is(err, context.Canceled)) {
		err = nil // stopped on request
	} else if err == nil {
		err = errors.New("telegram: client stopped unexpectedly")
	}
	if closeErr := store.Close(); closeErr != nil && err == nil {
		err = fmt.Errorf("telegram: close database: %w", closeErr)
	}
	d.mu.Lock()
	d.ready, d.err = false, err
	d.mu.Unlock()
	if err != nil {
		d.log.Error("telegram: driver failed", "error", err)
	}
	close(d.done)
}

func (d *Driver) recordGap(store *telegramstore.Store, kind string, channelID int64) {
	d.log.Warn("telegram: updates could not be recovered", "gap", kind, "channel", channelID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.RecordGap(ctx, kind, channelID, time.Now()); err != nil {
		d.log.Error("telegram: record gap", "gap", kind, "error", err)
	}
}

// Ready reports whether the driver is receiving updates.
func (d *Driver) Ready() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.ready
}

// Done is closed when the driver has stopped, on request or by failure.
func (d *Driver) Done() <-chan struct{} { return d.done }

// Err returns why the driver failed, or nil if it is running or was stopped
// on request.
func (d *Driver) Err() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.err
}

// Drain stops dispatching new inbox rows to the kernel; updates are still
// persisted. Rows left undispatched are processed on the next start.
func (d *Driver) Drain(ctx context.Context) error {
	d.mu.Lock()
	drain := d.drain
	d.mu.Unlock()
	if drain == nil {
		return nil
	}
	drain()
	select {
	case <-d.drained:
		return nil
	case <-d.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stop disconnects and waits for the driver to finish, or ctx to end.
func (d *Driver) Stop(ctx context.Context) error {
	d.mu.Lock()
	cancel := d.cancel
	d.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-d.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Send delivers an outbound message; see otogi.Messenger.
func (d *Driver) Send(ctx context.Context, msg otogi.OutboundMessage) (otogi.EventRef, error) {
	d.mu.Lock()
	s := d.sender
	d.mu.Unlock()
	if s == nil {
		return otogi.EventRef{}, fmt.Errorf("telegram: not logged in yet: %w", otogi.ErrUnavailable)
	}
	return s.Send(ctx, msg)
}

func peerResolver(m *peers.Manager) func(context.Context, string) (tg.InputPeerClass, error) {
	return func(ctx context.Context, conversation string) (tg.InputPeerClass, error) {
		kind, id, err := parsePeer(conversation)
		if err != nil {
			return nil, err
		}
		var peer peers.Peer
		switch kind {
		case "user":
			peer, err = m.ResolveUserID(ctx, id)
		case "chat":
			peer, err = m.ResolveChatID(ctx, id)
		default:
			peer, err = m.ResolveChannelID(ctx, id)
		}
		if err != nil {
			return nil, err
		}
		return peer.InputPeer(), nil
	}
}

func prune(ctx context.Context, store *telegramstore.Store, log *slog.Logger) error {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if err := store.Prune(ctx, time.Now()); err != nil && ctx.Err() == nil {
			log.Warn("telegram: prune database", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// guarded turns a panic in fn into an error carrying the stack.
func guarded(name string, fn func() error) func() error {
	return func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("telegram: %s panicked: %v\n%s", name, r, debug.Stack())
			}
		}()
		return fn()
	}
}
