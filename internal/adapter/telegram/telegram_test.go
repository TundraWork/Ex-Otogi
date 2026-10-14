package telegram

import (
	"context"
	"crypto/rand"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	gotdtelegram "github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/TundraWork/Ex-Otogi/internal/kernel"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

type fakeClient struct {
	api     *tg.Client
	options gotdtelegram.Options
}

func (c *fakeClient) Run(ctx context.Context, f func(context.Context) error) error { return f(ctx) }
func (c *fakeClient) API() *tg.Client                                              { return c.api }
func (c *fakeClient) Auth() *auth.Client                                           { return auth.NewClient(c.api, rand.Reader, 1, "hash") }
func (c *fakeClient) Self(context.Context) (*tg.User, error) {
	return &tg.User{ID: 42, Self: true}, nil
}

type telegramAPI struct {
	authorized bool
	stateErr   error
}

func (a telegramAPI) rpc() rpc {
	return func(_ context.Context, request bin.Encoder) (bin.Encoder, error) {
		switch request.(type) {
		case *tg.UsersGetUsersRequest:
			if !a.authorized {
				return nil, tgerr.New(401, "AUTH_KEY_UNREGISTERED")
			}
			return &tg.UserClassVector{Elems: []tg.UserClass{&tg.User{ID: 42, Self: true}}}, nil
		case *tg.UpdatesGetStateRequest:
			if a.stateErr != nil {
				return nil, a.stateErr
			}
			return &tg.UpdatesState{Pts: 1, Date: 1}, nil
		}
		return nil, unexpected(request)
	}
}

type recordingModule struct{ seen chan otogi.Event }

func (recordingModule) Descriptor() otogi.ModuleDescriptor {
	return otogi.ModuleDescriptor{ID: "recorder", Subscribes: []otogi.EventAction{otogi.ActionMessageCreated}}
}

func (m recordingModule) Handle(_ context.Context, e otogi.Event, _ otogi.Invocation) error {
	m.seen <- e
	return nil
}

type driverHarness struct {
	driver *Driver
	client *fakeClient
	kernel *kernel.Kernel
	seen   chan otogi.Event
	dbPath string
}

func newDriverHarness(t *testing.T, api telegramAPI) *driverHarness {
	t.Helper()
	dir := t.TempDir()
	h := &driverHarness{client: &fakeClient{api: tg.NewClient(api.rpc())}, seen: make(chan otogi.Event, 8), dbPath: filepath.Join(dir, "state.sqlite")}
	h.driver = New(Config{AccountKey: "main", SessionPath: filepath.Join(dir, "session", "session.json"), DatabasePath: h.dbPath, Logger: quiet})
	h.driver.newClient = func(opts gotdtelegram.Options) client {
		h.client.options = opts
		return h.client
	}
	k, err := kernel.New(kernel.Config{}, []otogi.Module{recordingModule{seen: h.seen}}, nil, h.driver, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.kernel = k
	h.driver.cfg.Kernel = k
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = h.driver.Stop(ctx)
		_ = k.Stop(ctx)
	})
	return h
}

func (h *driverHarness) deliver(t *testing.T, pts int, text string) {
	t.Helper()
	update := &tg.UpdateNewMessage{Message: newMessage(pts, &tg.PeerUser{UserID: 2}, text), Pts: pts, PtsCount: 1}
	users := []tg.UserClass{&tg.User{ID: 2, AccessHash: 20}, &tg.User{ID: 5, AccessHash: 50}}
	if err := h.client.options.UpdateHandler.Handle(context.Background(), &tg.Updates{Updates: []tg.UpdateClass{update}, Users: users, Date: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestDriverDeliversUpdatesToModulesAndStops(t *testing.T) {
	h := newDriverHarness(t, telegramAPI{authorized: true})
	if err := h.driver.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !h.driver.Ready() {
		t.Fatal("driver not ready after Start")
	}
	h.deliver(t, 2, "hello")
	select {
	case e := <-h.seen:
		if e.Platform != Platform || e.Account != "main" || e.Message.PlainText() != "hello" {
			t.Fatalf("module saw %+v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("update did not reach the module")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.driver.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.driver.Err(); err != nil || h.driver.Ready() {
		t.Fatalf("after Stop: err = %v, ready = %v", err, h.driver.Ready())
	}
	// The row was finished before stopping.
	store, err := telegramstore.Open(context.Background(), h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if n := pendingCount(t, store); n != 0 {
		t.Fatalf("pending rows after processing = %d", n)
	}
}

func TestDriverDrainLeavesNewRowsPending(t *testing.T) {
	h := newDriverHarness(t, telegramAPI{authorized: true})
	if err := h.driver.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := h.driver.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A second connection reads alongside the driver's (WAL allows it).
	store, err := telegramstore.Open(context.Background(), h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	h.deliver(t, 2, "after drain")
	eventually(t, "update persisted", func() bool { return pendingCount(t, store) == 1 })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.driver.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-h.seen:
		t.Fatalf("event dispatched after Drain: %+v", e)
	default:
	}
	if n := pendingCount(t, store); n != 1 {
		t.Fatalf("pending rows = %d, want the persisted update", n)
	}
}

func TestDriverStartFailures(t *testing.T) {
	t.Run("not logged in", func(t *testing.T) {
		h := newDriverHarness(t, telegramAPI{authorized: false})
		if err := h.driver.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "otogi login") {
			t.Fatalf("Start = %v", err)
		}
		<-h.driver.Done()
	})
	t.Run("update state unavailable", func(t *testing.T) {
		h := newDriverHarness(t, telegramAPI{authorized: true, stateErr: tgerr.New(500, "INTERNAL")})
		if err := h.driver.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "INTERNAL") {
			t.Fatalf("Start = %v", err)
		}
		if h.driver.Err() == nil {
			t.Fatal("Err() is nil after failed start")
		}
	})
	t.Run("database of another account", func(t *testing.T) {
		h := newDriverHarness(t, telegramAPI{authorized: true})
		store, err := telegramstore.Open(context.Background(), h.dbPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Bind(context.Background(), "other", 42); err != nil {
			t.Fatal(err)
		}
		_ = store.Close()
		if err := h.driver.Start(context.Background()); !errors.Is(err, telegramstore.ErrAccountMismatch) {
			t.Fatalf("Start = %v", err)
		}
	})
}
