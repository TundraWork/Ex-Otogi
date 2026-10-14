package telegram

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/mapper"
	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	telegramupdates "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/updates"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
	"github.com/gotd/td/bin"
	gotdtelegram "github.com/gotd/td/telegram"
	gotdupdates "github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
)

// ProductionCompositionConfig contains the external policy and bounds needed to
// construct one concrete Telegram adapter graph. All Telegram-specific
// components and persistence are owned by the composition.
type ProductionCompositionConfig struct {
	// Driver contains credentials, paths, authorization, and the client factory.
	Driver Config
	// Admitter is the kernel boundary receiving durable inbox work.
	Admitter EventSubmitter
	// Dispatcher bounds durable delivery, retries, and shutdown cleanup.
	Dispatcher InboxDispatcherConfig
	// Recovery bounds Telegram reconciliation and mapper-upgrade replay.
	Recovery ResyncLimits
	// Now supplies the wall clock used for capture, delivery, replay, and sends.
	Now func() time.Time
}

// CompositionRuntime is the complete account-bound runtime owned by a Composition.
type CompositionRuntime struct {
	// RunManager owns update ordering and opens the startup journal gate before signaling ready.
	RunManager func(context.Context, func()) error
	// RunResynchronizer reconciles durable update state before inbox dispatch.
	RunResynchronizer func(context.Context) error
	// RunDispatcher owns durable inbox delivery and signals actual readiness.
	RunDispatcher func(context.Context, func()) error
	// DrainDispatcher stops new claims and waits for accepted work.
	DrainDispatcher func(context.Context) error
	// Sender is the account-bound durable outbound sender.
	Sender *Sender
	// Refetcher is the account-bound message and media metadata refetcher.
	Refetcher *Refetcher
}

type updateProxy struct {
	mu   sync.RWMutex
	next gotdtelegram.UpdateHandler
}

func (p *updateProxy) bind(next gotdtelegram.UpdateHandler) {
	p.mu.Lock()
	p.next = next
	p.mu.Unlock()
}
func (p *updateProxy) Handle(ctx context.Context, update tg.UpdatesClass) error {
	p.mu.RLock()
	next := p.next
	p.mu.RUnlock()
	if next == nil {
		return errors.New("telegram: update graph is not bound")
	}
	return next.Handle(ctx, update)
}

type middlewareProxy struct {
	mu   sync.RWMutex
	next gotdtelegram.Middleware
}

func (p *middlewareProxy) bind(next gotdtelegram.Middleware) {
	p.mu.Lock()
	p.next = next
	p.mu.Unlock()
}
func (p *middlewareProxy) Handle(invoker tg.Invoker) gotdtelegram.InvokeFunc {
	return func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		p.mu.RLock()
		next := p.next
		p.mu.RUnlock()
		if next == nil {
			return invoker.Invoke(ctx, input, output)
		}
		return next.Handle(invoker)(ctx, input, output)
	}
}

// Composition owns a Driver and the concrete account-bound Telegram graph.
type Composition struct {
	driver      *Driver
	config      ProductionCompositionConfig
	handler     *updateProxy
	middlewares [2]*middlewareProxy

	mu      sync.RWMutex
	runtime CompositionRuntime
	bound   bool
}

// NewComposition constructs an inert production Telegram composition. It
// installs forwarding update hooks before client construction and binds them to
// the concrete account graph only after authorization and store opening.
func NewComposition(config ProductionCompositionConfig) (*Composition, error) {
	if config.Admitter == nil || config.Now == nil {
		return nil, ErrInvalidConfig
	}
	c := &Composition{config: config, handler: &updateProxy{}, middlewares: [2]*middlewareProxy{{}, {}}}
	cfg := config.Driver
	cfg.ConfigureClientOptions = func(options *ClientOptions) error {
		if options == nil {
			return ErrInvalidConfig
		}
		options.UpdateHandler = c.handler
		options.Middlewares = append(options.Middlewares, c.middlewares[0], c.middlewares[1])
		return nil
	}
	cfg.OpenStore = func(ctx context.Context, storeConfig StoreConfig) (DriverStore, error) {
		return telegramstore.Open(ctx, storeConfig)
	}
	cfg.RunManager = c.runManager
	cfg.RunResynchronizer = c.runResynchronizer
	cfg.RunDispatcher = c.runDispatcher
	cfg.DrainDispatcher = c.drainDispatcher
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	c.driver = NewDriver(cfg)
	return c, nil
}

func (c *Composition) bindRuntime(client ClientSession, driverStore DriverStore, authorizedUserID int64) (CompositionRuntime, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.bound {
		return c.runtime, nil
	}
	store, ok := driverStore.(*telegramstore.Store)
	if !ok {
		return CompositionRuntime{}, fmt.Errorf("%w: production store type", ErrInvalidConfig)
	}
	api := client.API()
	if api == nil {
		return CompositionRuntime{}, fmt.Errorf("%w: Telegram client API", ErrInvalidConfig)
	}
	if authorizedUserID <= 0 {
		return CompositionRuntime{}, fmt.Errorf("%w: authorized Telegram user", ErrInvalidConfig)
	}
	valueMapper := mapper.New(mapper.Config{Platform: "telegram", Account: otogi.AccountID(c.config.Driver.AccountKey), AuthorizedUserID: authorizedUserID, IngestedAt: c.config.Now})
	health := telegramupdates.NewHealth()
	downstream := telegramupdates.NewDownstream(store, valueMapper, EncodeEvent, health)
	manager, gate, _, _ := telegramupdates.NewManager(telegramupdates.ManagerConfig{APIStorage: store, AccessHasher: store, UserAccessHasher: store, Journal: store, Downstream: downstream, GapStore: store, Health: health})
	middlewares := telegramupdates.Middlewares(gate, manager)
	c.handler.bind(gate)
	c.middlewares[0].bind(middlewares[0])
	c.middlewares[1].bind(middlewares[1])

	raw := NewDurableReprocessor(store,
		func(ctx context.Context, payload []byte) error {
			box := bin.Buffer{Buf: payload}
			update, decodeErr := tg.DecodeUpdates(&box)
			if decodeErr != nil {
				return decodeErr
			}
			if box.Len() != 0 {
				return fmt.Errorf("trailing journal bytes: %d", box.Len())
			}
			return downstream.Handle(ctx, update)
		},
		func(ctx context.Context, payload []byte) error {
			update, decodeErr := mapper.DecodeQuarantinedUpdate(payload)
			if decodeErr != nil {
				return decodeErr
			}
			return downstream.Handle(ctx, &tg.Updates{Updates: []tg.UpdateClass{update}})
		}, c.config.Now)
	replay := &DurableReprocessorReplay{
		DifferenceFunc: func(ctx context.Context, difference tg.UpdatesDifferenceClass) error {
			updates, convertErr := updatesFromDifference(difference)
			if convertErr != nil {
				return convertErr
			}
			return downstream.Handle(ctx, updates)
		},
		UpdatesFunc: func(ctx context.Context, _ string, updates tg.UpdatesClass) error {
			return downstream.Handle(ctx, updates)
		},
		HistoryFunc: func(ctx context.Context, _ string, history tg.MessagesMessagesClass) error {
			return downstream.Handle(ctx, updatesFromHistory(history))
		},
		Raw: raw,
	}
	resynchronizer := NewResynchronizer(api, StoreRecovery{Store: store, UserID: authorizedUserID}, replay, c.config.Recovery)
	dispatcher := NewInboxDispatcher(store, c.config.Admitter, DecodeEvent, c.config.Dispatcher)
	sender := NewSender(api, store, gate, c.config.Now)
	refetcher := NewRefetcher(api, store)
	var dispatchMu sync.Mutex
	var dispatchDone chan struct{}
	runtime := CompositionRuntime{
		RunManager: func(ctx context.Context, ready func()) error {
			return telegramupdates.RunManager(ctx, manager, gate, api, authorizedUserID, gotdupdates.AuthOptions{}, ready)
		},
		RunResynchronizer: func(ctx context.Context) error {
			if err := sender.Reconcile(ctx); err != nil {
				return err
			}
			return resynchronizer.Run(ctx)
		},
		RunDispatcher: func(ctx context.Context, ready func()) error {
			dispatchMu.Lock()
			dispatchDone = make(chan struct{})
			done := dispatchDone
			dispatchMu.Unlock()
			defer close(done)
			return dispatcher.Run(ctx, ready)
		},
		DrainDispatcher: func(ctx context.Context) error {
			dispatchMu.Lock()
			done := dispatchDone
			dispatchMu.Unlock()
			if done == nil {
				return ErrInvalidLifecycle
			}
			return dispatcher.Drain(ctx)
		},
		Sender:    sender,
		Refetcher: refetcher,
	}
	c.runtime, c.bound = runtime, true
	return runtime, nil
}

func updatesFromDifference(value tg.UpdatesDifferenceClass) (tg.UpdatesClass, error) {
	var messages []tg.MessageClass
	var other []tg.UpdateClass
	var users []tg.UserClass
	var chats []tg.ChatClass
	switch difference := value.(type) {
	case *tg.UpdatesDifferenceEmpty, *tg.UpdatesDifferenceTooLong:
		return &tg.Updates{}, nil
	case *tg.UpdatesDifference:
		messages, other, users, chats = difference.NewMessages, difference.OtherUpdates, difference.Users, difference.Chats
	case *tg.UpdatesDifferenceSlice:
		messages, other, users, chats = difference.NewMessages, difference.OtherUpdates, difference.Users, difference.Chats
	default:
		return nil, fmt.Errorf("unsupported common difference %T", value)
	}
	result := make([]tg.UpdateClass, 0, len(messages)+len(other))
	for _, message := range messages {
		result = append(result, &tg.UpdateNewMessage{Message: message})
	}
	result = append(result, other...)
	return &tg.Updates{Updates: result, Users: users, Chats: chats}, nil
}

func updatesFromHistory(value tg.MessagesMessagesClass) tg.UpdatesClass {
	modified, ok := value.AsModified()
	if !ok {
		return &tg.Updates{}
	}
	messages := modified.GetMessages()
	result := make([]tg.UpdateClass, 0, len(messages))
	for _, message := range messages {
		result = append(result, &tg.UpdateNewMessage{Message: message})
	}
	return &tg.Updates{Updates: result, Users: modified.GetUsers(), Chats: modified.GetChats()}
}

func (c *Composition) runManager(ctx context.Context, client ClientSession, store DriverStore, authorizedUserID int64, ready func()) error {
	runtime, err := c.bindRuntime(client, store, authorizedUserID)
	if err != nil {
		return err
	}
	return runtime.RunManager(ctx, ready)
}
func (c *Composition) runResynchronizer(ctx context.Context, client ClientSession, store DriverStore) error {
	runtime, err := c.bindRuntime(client, store, c.driver.authorizedUserID())
	if err != nil {
		return err
	}
	return runtime.RunResynchronizer(ctx)
}
func (c *Composition) runDispatcher(ctx context.Context, _ DriverStore, ready func()) error {
	c.mu.RLock()
	runtime, bound := c.runtime, c.bound
	c.mu.RUnlock()
	if !bound {
		return errors.New("telegram: composition runtime is not bound")
	}
	return runtime.RunDispatcher(ctx, ready)
}
func (c *Composition) drainDispatcher(ctx context.Context) error {
	c.mu.RLock()
	runtime, bound := c.runtime, c.bound
	c.mu.RUnlock()
	if !bound {
		return ErrInvalidLifecycle
	}
	return runtime.DrainDispatcher(ctx)
}

// Driver returns the single Driver owned by this composition.
func (c *Composition) Driver() *Driver { return c.driver }

// Sender returns the account-bound sender after successful binding.
func (c *Composition) Sender() *Sender { c.mu.RLock(); defer c.mu.RUnlock(); return c.runtime.Sender }

// Refetcher returns the account-bound refetcher after successful binding.
func (c *Composition) Refetcher() *Refetcher {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.runtime.Refetcher
}

// Start starts the owned Driver and waits for readiness or failure.
func (c *Composition) Start(ctx context.Context) error { return c.driver.Start(ctx) }

// Ready reports whether the owned Driver is ready.
func (c *Composition) Ready() bool { return c.driver.Ready() }

// Drain drains the owned Driver dispatcher.
func (c *Composition) Drain(ctx context.Context) error { return c.driver.Drain(ctx) }

// Stop stops the owned Driver and its children in reverse dependency order.
func (c *Composition) Stop(ctx context.Context) error { return c.driver.Stop(ctx) }
