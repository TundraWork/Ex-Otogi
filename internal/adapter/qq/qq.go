// Package qq is the QQ driver. It runs one bot on the official QQ Bot API
// v2: a WebSocket gateway session for events, persisted in a durable SQLite
// inbox before their sequence number is taken as seen, and REST calls for
// everything else.
package qq

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	qqstore "github.com/TundraWork/Ex-Otogi/internal/adapter/qq/store"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Platform is the platform ID of QQ events.
const Platform otogi.PlatformID = "qq"

// Config configures one QQ bot.
type Config struct {
	// AccountKey names this bot in the deployment; events carry it in their
	// account ID, and the database is bound to it.
	AccountKey string
	// AppID and AppSecret are the bot's credentials from the QQ Open
	// Platform console.
	AppID, AppSecret string
	// DatabasePath is the bot's SQLite database.
	DatabasePath string
	// Kernel receives events.
	Kernel Submitter
	// Logger receives driver logs; nil means slog.Default().
	Logger *slog.Logger
	// Recorder receives event log records; nil discards them.
	Recorder otogi.Recorder
}

// Driver runs one QQ bot.
type Driver struct {
	cfg Config
	log *slog.Logger
	api *api
	// backoff is the gateway's wait before each reconnect attempt in a row.
	backoff []time.Duration

	mu        sync.Mutex
	store     *qqstore.Store
	effects   *effects
	started   bool
	ready     bool
	self      string // the bot's own user ID, from READY
	username  string
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
	if cfg.Recorder == nil {
		cfg.Recorder = otogi.NopRecorder{}
	}
	return &Driver{
		cfg: cfg, log: cfg.Logger.With("driver", "qq", "account", cfg.AccountKey),
		api:     &api{base: defaultAPI, appID: cfg.AppID, secret: cfg.AppSecret, http: &http.Client{Timeout: time.Minute}, now: time.Now},
		backoff: []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second, time.Minute},
		readyCh: make(chan struct{}), done: make(chan struct{}), drained: make(chan struct{}),
	}
}

// Start opens the database, connects, and returns once the gateway session
// is ready, or with the reason it could not be.
func (d *Driver) Start(ctx context.Context) error {
	d.mu.Lock()
	if d.started {
		d.mu.Unlock()
		return fmt.Errorf("qq: already started: %w", otogi.ErrUnavailable)
	}
	d.started = true
	d.mu.Unlock()

	store, err := qqstore.Open(ctx, d.cfg.DatabasePath)
	if err != nil {
		return err
	}
	if err := store.Bind(ctx, d.cfg.AccountKey, d.cfg.AppID); err != nil {
		_ = store.Close()
		if errors.Is(err, qqstore.ErrAccountMismatch) {
			return fmt.Errorf("%w; if the account now runs another bot on purpose, move %s aside so it starts afresh", err, d.cfg.DatabasePath)
		}
		return err
	}
	self, username, err := store.Bot(ctx)
	if err != nil {
		_ = store.Close()
		return err
	}
	// Check the credentials now, so a wrong secret fails startup plainly
	// rather than as a reconnect loop.
	if _, err := d.api.accessToken(ctx); err != nil {
		_ = store.Close()
		return fmt.Errorf("qq: get an access token for app %s: %w", d.cfg.AppID, err)
	}
	wake := make(chan struct{}, 1)
	d.mu.Lock()
	d.store, d.self, d.username = store, self, username
	d.effects = &effects{
		api: d.api, store: store, account: otogi.AccountID(d.cfg.AccountKey), wake: wake, log: d.log, now: time.Now,
		self: d.selfUser, retries: 3, backoff: time.Second,
	}
	d.mu.Unlock()

	runCtx, cancel := context.WithCancel(context.Background())
	g, groupCtx := errgroup.WithContext(runCtx)
	dispatchCtx, stopDispatch := context.WithCancel(groupCtx)
	d.mu.Lock()
	d.cancel, d.drain = cancel, stopDispatch
	d.mu.Unlock()
	gw := &gateway{
		api: d.api, store: store, wake: wake, log: d.log, now: time.Now, record: d.record, backoff: d.backoff,
		ready: d.markReady, gap: func(kind, detail string) { d.recordGap(store, kind, detail) },
	}
	disp := &dispatcher{
		store: store, api: d.api, kernel: d.cfg.Kernel, wake: wake, poll: time.Second, log: d.log, now: time.Now, record: d.record,
		mapper: &mapper{account: otogi.AccountID(d.cfg.AccountKey), refs: store, self: d.selfID},
	}
	g.Go(guarded("gateway", func() error { return gw.run(groupCtx) }))
	g.Go(guarded("dispatcher", func() error {
		defer close(d.drained)
		return disp.run(dispatchCtx)
	}))
	g.Go(guarded("pruner", func() error { return prune(groupCtx, store, d.log) }))
	go func() { d.finish(runCtx, store, g.Wait()) }()

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

// markReady notes the bot's own user from READY and, the first time, that
// the driver receives events.
func (d *Driver) markReady(id, username string) {
	d.mu.Lock()
	if id != "" {
		d.self, d.username = id, username
	}
	d.ready = true
	d.mu.Unlock()
	d.readyOnce.Do(func() { close(d.readyCh) })
}

func (d *Driver) selfID() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.self
}

func (d *Driver) selfUser() (string, string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.self, d.username
}

func (d *Driver) finish(runCtx context.Context, store *qqstore.Store, err error) {
	if runCtx.Err() != nil && (err == nil || errors.Is(err, context.Canceled)) {
		err = nil // stopped on request
	} else if err == nil {
		err = errors.New("qq: driver stopped unexpectedly")
	}
	if closeErr := store.Close(); closeErr != nil && err == nil {
		err = fmt.Errorf("qq: close database: %w", closeErr)
	}
	d.mu.Lock()
	d.ready, d.err = false, err
	d.mu.Unlock()
	if err != nil {
		d.log.Error("qq: driver failed", "error", err)
		d.record("driver.state", "error", map[string]string{"state": "failed", "error": err.Error()})
	} else {
		d.record("driver.state", "ok", map[string]string{"state": "stopped"})
	}
	close(d.done)
}

func (d *Driver) recordGap(store *qqstore.Store, kind, detail string) {
	d.log.Warn("qq: events may have been lost", "gap", kind, "detail", detail)
	d.record("driver.gap", "", map[string]any{"gap": kind, "detail": detail})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.RecordGap(ctx, kind, detail, time.Now()); err != nil {
		d.log.Error("qq: record gap", "gap", kind, "error", err)
	}
}

func (d *Driver) record(kind, outcome string, payload any) {
	d.cfg.Recorder.Record(context.Background(), otogi.Record{
		At: time.Now(), Kind: kind, Component: "driver:qq/" + d.cfg.AccountKey,
		Platform: Platform, Account: otogi.AccountID(d.cfg.AccountKey), Outcome: outcome, Payload: payload,
	})
}

// Status describes the driver for the management API.
type Status struct {
	Platform    otogi.PlatformID `json:"platform"`
	Account     string           `json:"account"`
	Ready       bool             `json:"ready"`
	Error       string           `json:"error,omitempty"`
	Bot         string           `json:"bot,omitempty"` // the bot's own user ID and name
	Session     string           `json:"session,omitempty"`
	Seq         int64            `json:"seq,omitempty"`
	LastEvent   time.Time        `json:"last_event,omitzero"`
	TokenExpiry time.Time        `json:"token_expiry,omitzero"`
	qqstore.Counts
}

// Status returns the driver's state, its gateway session, and inbox counts.
func (d *Driver) Status(ctx context.Context) Status {
	d.mu.Lock()
	s := Status{Platform: Platform, Account: d.cfg.AccountKey, Ready: d.ready, TokenExpiry: d.api.tokenExpiry()}
	if d.self != "" {
		s.Bot = d.self + " " + d.username
	}
	if d.err != nil {
		s.Error = d.err.Error()
	}
	store := d.store
	d.mu.Unlock()
	select {
	case <-d.done: // the database is closed
	default:
		if store != nil {
			var errs []string
			if counts, err := store.Counts(ctx); err != nil {
				errs = append(errs, err.Error())
			} else {
				s.Counts = counts
			}
			if sess, ok, err := store.Session(ctx); err != nil {
				errs = append(errs, err.Error())
			} else if ok {
				s.Session, s.Seq, s.LastEvent = sess.ID, sess.Seq, sess.UpdatedAt
			}
			if len(errs) > 0 {
				s.Error = strings.TrimPrefix(s.Error+"; "+strings.Join(errs, "; "), "; ")
			}
		}
	}
	return s
}

// Ready reports whether the driver receives events.
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

// Drain stops dispatching new inbox rows to the kernel; dispatches are still
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

// OpenStream starts a streamed message; see otogi.Streamer. Only C2C
// replies stream; QQ cannot stream in groups.
func (d *Driver) OpenStream(ctx context.Context, e otogi.Effect) (otogi.DriverStream, error) {
	d.mu.Lock()
	x := d.effects
	d.mu.Unlock()
	if x == nil {
		return nil, fmt.Errorf("qq: not started: %w", otogi.ErrUnavailable)
	}
	return x.OpenStream(ctx, e)
}

// Apply performs an effect; see otogi.Effector.
func (d *Driver) Apply(ctx context.Context, e otogi.Effect) (otogi.EventRef, error) {
	d.mu.Lock()
	x := d.effects
	d.mu.Unlock()
	if x == nil {
		return otogi.EventRef{}, fmt.Errorf("qq: not started: %w", otogi.ErrUnavailable)
	}
	return x.Apply(ctx, e)
}

func prune(ctx context.Context, store *qqstore.Store, log *slog.Logger) error {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if err := store.Prune(ctx, time.Now()); err != nil && ctx.Err() == nil {
			log.Warn("qq: prune database", "error", err)
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
				err = fmt.Errorf("qq: %s panicked: %v\n%s", name, r, debug.Stack())
			}
		}()
		return fn()
	}
}

// FetchMedia downloads an attachment; see otogi.MediaFetcher. QQ's
// attachment URLs need no authorization, and expire within hours.
func (d *Driver) FetchMedia(ctx context.Context, _ otogi.Destination, src otogi.MediaSource) ([]byte, error) {
	if src.Kind != otogi.MediaSourceExternal || src.URL == "" {
		return nil, fmt.Errorf("qq: fetch %s media: %w", src.Kind, otogi.ErrUnsupported)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("qq: fetch media: %w", err)
	}
	resp, err := d.api.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qq: fetch media: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("qq: fetch media: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("qq: fetch media: HTTP %d: %s: %w", resp.StatusCode, data, otogi.ErrUnavailable)
	}
	return data, nil
}
