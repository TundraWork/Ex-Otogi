package telegram

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/mapper"
	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	telegramupdates "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/updates"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
	"github.com/gotd/td/bin"
	gotdtelegram "github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

type recordingInvoker struct{ calls int }

func (i *recordingInvoker) Invoke(context.Context, bin.Encoder, bin.Decoder) error {
	i.calls++
	return nil
}

type forwardingMiddleware struct{ supplied, downstream tg.Invoker }

func (m *forwardingMiddleware) Handle(invoker tg.Invoker) gotdtelegram.InvokeFunc {
	m.supplied = invoker
	return func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		return m.downstream.Invoke(ctx, input, output)
	}
}

func TestMiddlewareProxyPassesThroughUntilBoundAndThenForwards(t *testing.T) {
	supplied := &recordingInvoker{}
	proxy := &middlewareProxy{}
	if err := proxy.Handle(supplied)(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if supplied.calls != 1 {
		t.Fatalf("unbound supplied invoker calls=%d", supplied.calls)
	}
	downstream := &recordingInvoker{}
	middleware := &forwardingMiddleware{downstream: downstream}
	proxy.bind(middleware)
	if err := proxy.Handle(supplied)(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if middleware.supplied != supplied || downstream.calls != 1 {
		t.Fatalf("supplied=%T downstream calls=%d", middleware.supplied, downstream.calls)
	}
}

func TestCompositionLifecycleSmoke(t *testing.T) {
	cfg := validDriverConfig(t)
	cfg.ConfigureClientOptions = nil
	cfg.OpenStore = nil
	cfg.RunManager = nil
	cfg.RunResynchronizer = nil
	cfg.RunDispatcher = nil
	cfg.DrainDispatcher = nil
	_, err := NewComposition(ProductionCompositionConfig{Driver: cfg})
	if err == nil {
		t.Fatal("NewComposition accepted missing production dependencies")
	}
}

type recordingUpdateHandler struct{ called bool }

func (h *recordingUpdateHandler) Handle(context.Context, tg.UpdatesClass) error {
	h.called = true
	return nil
}

func TestUpdateProxyForwardsOnlyAfterBinding(t *testing.T) {
	proxy := &updateProxy{}
	if err := proxy.Handle(context.Background(), &tg.Updates{}); err == nil {
		t.Fatal("unbound update proxy accepted an update")
	}
	handler := &recordingUpdateHandler{}
	proxy.bind(handler)
	if err := proxy.Handle(context.Background(), &tg.Updates{}); err != nil {
		t.Fatal(err)
	}
	if !handler.called {
		t.Fatal("bound update proxy did not forward")
	}
}

type compositionAdmitter struct{}

func (compositionAdmitter) SubmitTrusted(context.Context, otogi.EventID, otogi.EventInput) error {
	return nil
}
func (compositionAdmitter) SubscribeCompletion(context.Context, otogi.EventID) (<-chan otogi.EventID, error) {
	return make(chan otogi.EventID), nil
}

func validProductionCompositionConfig(t *testing.T) ProductionCompositionConfig {
	t.Helper()
	cfg := validDriverConfig(t)
	cfg.ConfigureClientOptions = nil
	cfg.OpenStore = nil
	cfg.RunManager = nil
	cfg.RunResynchronizer = nil
	cfg.RunDispatcher = nil
	cfg.DrainDispatcher = nil
	tenant, err := otogi.NewTenantID("tenant")
	if err != nil {
		t.Fatal(err)
	}
	return ProductionCompositionConfig{
		Driver: cfg, Tenant: tenant, Admitter: compositionAdmitter{}, Now: time.Now,
		Dispatcher: InboxDispatcherConfig{Owner: "test", Lease: time.Minute, ClaimTimeout: time.Second, AttemptTimeout: time.Second, CleanupTimeout: time.Second, PollInterval: time.Millisecond, RetryBase: time.Second, RetryMax: time.Minute, Now: time.Now},
		Recovery:   ResyncLimits{MaxDifferencePages: 1, MaxHistoryPages: 1, HistoryPageSize: 1, MaxReprocessRows: 1, CallTimeout: time.Second, MaxResponseItems: 1, MaxResponseBytes: 1024, MaxPeerPages: 1, PeerPageSize: 1},
	}
}

func TestNewCompositionInstallsPreClientProxies(t *testing.T) {
	composition, err := NewComposition(validProductionCompositionConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	var options ClientOptions
	if err := composition.driver.cfg.ConfigureClientOptions(&options); err != nil {
		t.Fatal(err)
	}
	if options.UpdateHandler != composition.handler || len(options.Middlewares) != 2 {
		t.Fatalf("handler=%T middlewares=%d", options.UpdateHandler, len(options.Middlewares))
	}
	if composition.Sender() != nil || composition.Refetcher() != nil {
		t.Fatal("account-bound surfaces published before authorization")
	}
}

func TestNewCompositionRejectsMissingProductionDependencies(t *testing.T) {
	cfg := validDriverConfig(t)
	cfg.ConfigureClientOptions = nil
	cfg.OpenStore = nil
	cfg.RunManager = nil
	cfg.RunResynchronizer = nil
	cfg.RunDispatcher = nil
	cfg.DrainDispatcher = nil
	_, err := NewComposition(ProductionCompositionConfig{Driver: cfg})
	if err == nil {
		t.Fatal("NewComposition accepted missing tenant, admitter, bounds, and clock")
	}
}

type compositionAPIFake struct {
	mu         sync.Mutex
	handler    func(context.Context, tg.UpdatesClass) error
	events     []string
	stateCalls int
	difference int
}

func (a *compositionAPIFake) record(event string) {
	a.mu.Lock()
	a.events = append(a.events, event)
	a.mu.Unlock()
}

func (a *compositionAPIFake) snapshot() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.events...)
}

func (a *compositionAPIFake) UpdatesGetState(ctx context.Context) (*tg.UpdatesState, error) {
	a.mu.Lock()
	a.stateCalls++
	first := a.stateCalls == 1
	a.events = append(a.events, "manager-state")
	a.mu.Unlock()
	if first {
		message := &tg.Message{ID: 11, PeerID: &tg.PeerUser{UserID: 9}, Date: 100, Message: "startup"}
		message.SetFromID(&tg.PeerUser{UserID: 9})
		if err := a.handler(ctx, &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateNewMessage{Message: message, Pts: 1, PtsCount: 1}}}); err != nil {
			return nil, err
		}
	}
	return &tg.UpdatesState{Pts: 1, Date: 100, Seq: 1}, nil
}

func (a *compositionAPIFake) UpdatesGetDifference(context.Context, *tg.UpdatesGetDifferenceRequest) (tg.UpdatesDifferenceClass, error) {
	a.mu.Lock()
	a.difference++
	a.events = append(a.events, "recovery-difference")
	a.mu.Unlock()
	return &tg.UpdatesDifferenceEmpty{Date: 100, Seq: 1}, nil
}

func (*compositionAPIFake) UpdatesGetChannelDifference(context.Context, *tg.UpdatesGetChannelDifferenceRequest) (tg.UpdatesChannelDifferenceClass, error) {
	return &tg.UpdatesChannelDifferenceEmpty{Final: true}, nil
}

func (*compositionAPIFake) MessagesGetHistory(context.Context, *tg.MessagesGetHistoryRequest) (tg.MessagesMessagesClass, error) {
	return &tg.MessagesMessages{}, nil
}

func (*compositionAPIFake) MessagesSendMessage(context.Context, *tg.MessagesSendMessageRequest) (tg.UpdatesClass, error) {
	return &tg.Updates{}, nil
}

func (*compositionAPIFake) MessagesGetMessages(context.Context, []tg.InputMessageClass) (tg.MessagesMessagesClass, error) {
	return &tg.MessagesMessages{Messages: []tg.MessageClass{&tg.Message{ID: 11, PeerID: &tg.PeerUser{UserID: 9}, Date: 100, Message: "startup"}}}, nil
}

func (*compositionAPIFake) ChannelsGetMessages(context.Context, *tg.ChannelsGetMessagesRequest) (tg.MessagesMessagesClass, error) {
	return &tg.MessagesMessages{}, nil
}

type compositionClientSession struct {
	api     API
	stopped chan struct{}
}

func (c *compositionClientSession) Run(ctx context.Context, fn func(context.Context) error) error {
	err := fn(ctx)
	close(c.stopped)
	return err
}
func (*compositionClientSession) Authorized(context.Context) (bool, error)  { return true, nil }
func (*compositionClientSession) Authorize(context.Context, AuthFlow) error { return nil }
func (*compositionClientSession) Self(context.Context) (*tg.User, error) {
	return &tg.User{ID: 42}, nil
}
func (c *compositionClientSession) API() API { return c.api }

type compositionClientFactory struct {
	client  *compositionClientSession
	api     *compositionAPIFake
	created bool
}

func (f *compositionClientFactory) New(_ int, _ string, options ClientOptions) ClientSession {
	f.created = true
	f.api.handler = options.UpdateHandler.Handle
	return f.client
}

type compositionSmokeAdmitter struct {
	api       *compositionAPIFake
	submitted chan otogi.EventInput
	mu        sync.Mutex
	complete  map[otogi.EventID]chan otogi.EventID
}

func (a *compositionSmokeAdmitter) SubscribeCompletion(_ context.Context, id otogi.EventID) (<-chan otogi.EventID, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	completion := make(chan otogi.EventID, 1)
	a.complete[id] = completion
	return completion, nil
}

func (a *compositionSmokeAdmitter) SubmitTrusted(_ context.Context, id otogi.EventID, input otogi.EventInput) error {
	a.api.record("dispatch-submit")
	a.submitted <- input
	a.mu.Lock()
	completion := a.complete[id]
	a.mu.Unlock()
	completion <- id
	return nil
}

func TestCompositionStartsRealGraphAndStopsInReverseOrder(t *testing.T) {
	api := &compositionAPIFake{}
	client := &compositionClientSession{api: api, stopped: make(chan struct{})}
	factory := &compositionClientFactory{client: client, api: api}
	admitter := &compositionSmokeAdmitter{api: api, submitted: make(chan otogi.EventInput, 1), complete: make(map[otogi.EventID]chan otogi.EventID)}
	config := validProductionCompositionConfig(t)
	config.Driver.ClientFactory = factory
	config.Admitter = admitter
	composition, err := NewComposition(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := composition.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !factory.created || !composition.Ready() {
		t.Fatalf("created=%v ready=%v", factory.created, composition.Ready())
	}
	select {
	case input := <-admitter.submitted:
		if len(input.Message().Parts()) == 0 {
			t.Fatalf("dispatched input=%+v", input)
		}
	case <-time.After(time.Second):
		t.Fatal("real dispatcher did not publish replayed startup update")
	}
	if composition.Sender() == nil || composition.Refetcher() == nil {
		t.Fatal("account-bound sender/refetcher not published")
	}
	events := api.snapshot()
	manager, recovery, dispatch := eventIndex(events, "manager-state"), eventIndex(events, "recovery-difference"), eventIndex(events, "dispatch-submit")
	if manager < 0 || recovery < 0 || dispatch < 0 || manager >= recovery || recovery >= dispatch {
		t.Fatalf("startup order=%v", events)
	}
	store := composition.driver.store
	if err := composition.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.stopped:
	default:
		t.Fatal("client child was not canceled before Stop returned")
	}
	if composition.Ready() || composition.Driver().State() != StateStopped {
		t.Fatalf("ready=%v state=%s", composition.Ready(), composition.Driver().State())
	}
	if _, _, err := store.RecoveryReady(context.Background()); err == nil {
		t.Fatal("real SQLite store remained open after children stopped")
	}
}

func TestCompositionPostReadyCaptureFailureFailsDriverAndStopsLaterDelivery(t *testing.T) {
	api := &compositionAPIFake{}
	client := &compositionClientSession{api: api, stopped: make(chan struct{})}
	factory := &compositionClientFactory{client: client, api: api}
	admitter := &compositionSmokeAdmitter{api: api, submitted: make(chan otogi.EventInput, 2), complete: make(map[otogi.EventID]chan otogi.EventID)}
	config := validProductionCompositionConfig(t)
	config.Driver.ClientFactory = factory
	config.Admitter = admitter
	composition, err := NewComposition(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := composition.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-admitter.submitted:
	case <-time.After(time.Second):
		t.Fatal("startup update was not dispatched")
	}
	failing := &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateDeleteMessages{Messages: make([]int, mapper.MaxDeleteMessages+1), Pts: 2, PtsCount: 1}}}
	if err := api.handler(context.Background(), failing); err != nil {
		t.Fatalf("gotd ingress unexpectedly returned synchronously: %v", err)
	}
	select {
	case <-composition.Driver().Done():
	case <-time.After(time.Second):
		t.Fatal("driver did not fail after post-ready degradation")
	}
	if composition.Driver().State() != StateFailed || composition.Ready() || composition.Driver().Err() == nil {
		t.Fatalf("state=%s ready=%v error=%v", composition.Driver().State(), composition.Ready(), composition.Driver().Err())
	}
	select {
	case <-client.stopped:
	default:
		t.Fatal("manager failure did not cancel client and sibling children")
	}
	store, ok := composition.driver.store.(*telegramstore.Store)
	if !ok {
		t.Fatalf("store=%T", composition.driver.store)
	}
	ids, err := store.StartupJournalIDs(context.Background(), telegramupdates.StartupJournalLimit)
	if err != nil || len(ids) != 1 {
		t.Fatalf("pending journal=%v error=%v", ids, err)
	}
	_, degraded, reason, err := store.Consistency(context.Background(), "common")
	if err != nil || !degraded || reason == "" {
		t.Fatalf("degraded=%v reason=%q error=%v", degraded, reason, err)
	}
	later := &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateUserTyping{UserID: 9, Action: &tg.SendMessageTypingAction{}}}}
	if err := api.handler(context.Background(), later); err == nil {
		t.Fatal("later packet passed failed gate")
	}
	idsAfter, err := store.StartupJournalIDs(context.Background(), telegramupdates.StartupJournalLimit)
	if err != nil || len(idsAfter) != 1 || idsAfter[0] != ids[0] {
		t.Fatalf("later packet changed pending journal: before=%v after=%v error=%v", ids, idsAfter, err)
	}
	if err := composition.Stop(context.Background()); err != nil && !errors.Is(err, composition.Driver().Err()) {
		t.Fatal(err)
	}
}

func eventIndex(events []string, target string) int {
	for i, event := range events {
		if event == target {
			return i
		}
	}
	return -1
}
