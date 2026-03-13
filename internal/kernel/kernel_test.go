package kernel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

// TestRegisterModuleDependencyValidation verifies capability-required service validation.
func TestRegisterModuleDependencyValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		registerLogger bool
		wantErr        bool
	}{
		{
			name:           "missing required service fails",
			registerLogger: false,
			wantErr:        true,
		},
		{
			name:           "present required service succeeds",
			registerLogger: true,
			wantErr:        false,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			kernelRuntime := newTestKernel(t)
			if testCase.registerLogger {
				if err := kernelRuntime.RegisterService("logger", struct{}{}); err != nil {
					t.Fatalf("register logger service failed: %v", err)
				}
			}

			module := &stubModule{
				name: "cap-module",
				spec: core.ModuleSpec{
					AdditionalCapabilities: []core.Capability{
						{Name: "needs-logger", RequiredServices: []string{"logger"}},
					},
				},
			}
			err := kernelRuntime.RegisterModule(context.Background(), module)
			if testCase.wantErr && err == nil {
				t.Fatal("expected module registration error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected module registration error: %v", err)
			}
		})
	}
}

func TestNewFailsWhenBootstrapServiceRegistrationFails(t *testing.T) {
	t.Parallel()

	_, err := New(withBootstrapServicesForTest(
		bootstrapServiceRegistration{
			name:    core.ServiceCommandCatalog,
			service: nil,
		},
	))
	if err == nil {
		t.Fatal("expected constructor error")
	}
	if !strings.Contains(err.Error(), "register bootstrap service") {
		t.Fatalf("error = %v, want bootstrap registration context", err)
	}
}

func TestRegisterModuleExposesConfigRegistryToOnRegister(t *testing.T) {
	t.Parallel()

	kernelRuntime := newTestKernel(t)
	rawConfig := json.RawMessage(`{"value":"configured"}`)
	if err := kernelRuntime.RegisterModuleConfig("config-reader", rawConfig); err != nil {
		t.Fatalf("RegisterModuleConfig failed: %v", err)
	}

	configSeen := make(chan string, 1)
	module := &stubModule{
		name: "config-reader",
		onRegister: func(_ context.Context, runtime core.ModuleRuntime) error {
			resolved, err := runtime.Config().Resolve("config-reader")
			if err != nil {
				return fmt.Errorf("resolve module config: %w", err)
			}

			var cfg struct {
				Value string `json:"value"`
			}
			if err := json.Unmarshal(resolved, &cfg); err != nil {
				return fmt.Errorf("unmarshal module config: %w", err)
			}

			configSeen <- cfg.Value

			return nil
		},
	}

	if err := kernelRuntime.RegisterModule(context.Background(), module); err != nil {
		t.Fatalf("RegisterModule failed: %v", err)
	}

	select {
	case got := <-configSeen:
		if got != "configured" {
			t.Fatalf("config value = %q, want configured", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for OnRegister config read")
	}
}

// TestKernelRunCallsModuleLifecycle verifies lifecycle hook execution during run/shutdown.
func TestKernelRunCallsModuleLifecycle(t *testing.T) {
	t.Parallel()

	kernelRuntime := newTestKernel(t)
	if err := kernelRuntime.RegisterService("logger", struct{}{}); err != nil {
		t.Fatalf("register service failed: %v", err)
	}

	module := &stubModule{name: "lifecycle"}
	if err := kernelRuntime.RegisterModule(context.Background(), module); err != nil {
		t.Fatalf("register module failed: %v", err)
	}

	driver := &stubDriver{name: "stub-driver"}
	if err := kernelRuntime.RegisterDriver(driver); err != nil {
		t.Fatalf("register driver failed: %v", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- kernelRuntime.Run(runCtx)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-runDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("kernel run failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("kernel run did not exit")
	}

	if module.registered.Load() == 0 {
		t.Fatal("module OnRegister was not called")
	}
	if module.started.Load() == 0 {
		t.Fatal("module OnStart was not called")
	}
	if module.shutdown.Load() == 0 {
		t.Fatal("module OnShutdown was not called")
	}
	if driver.started.Load() == 0 {
		t.Fatal("driver Start was not called")
	}
	if driver.stopped.Load() == 0 {
		t.Fatal("driver Shutdown was not called")
	}
}

// TestRegisterModuleBindsDeclarativeHandlers verifies handlers in ModuleSpec are auto-subscribed.
func TestRegisterModuleBindsDeclarativeHandlers(t *testing.T) {
	t.Parallel()

	kernelRuntime := newTestKernel(t)
	t.Cleanup(func() {
		_ = kernelRuntime.EventBus().Close(context.Background())
	})

	handled := make(chan string, 1)
	module := &stubModule{
		name: "declarative",
		spec: core.ModuleSpec{
			Handlers: []core.ModuleHandler{
				{
					Capability: core.Capability{
						Name: "message-created",
						Interest: core.InterestSet{
							Kinds: []platform.EventKind{platform.EventKindArticleCreated},
						},
					},
					Subscription: core.SubscriptionSpec{
						Name:    "declarative-handler",
						Buffer:  1,
						Workers: 1,
					},
					Handler: func(_ context.Context, event *platform.Event) error {
						handled <- event.ID
						return nil
					},
				},
			},
		},
	}
	if err := kernelRuntime.RegisterModule(context.Background(), module); err != nil {
		t.Fatalf("register module failed: %v", err)
	}

	if err := kernelRuntime.EventBus().Publish(context.Background(), newTestEvent("e1", platform.EventKindArticleCreated)); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	select {
	case id := <-handled:
		if id != "e1" {
			t.Fatalf("handled event id = %s, want e1", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for declarative handler")
	}
}

func TestRegisterModuleDeclarativeHandlerTimeoutOverride(t *testing.T) {
	t.Parallel()

	kernelRuntime := newTestKernel(t, WithDefaultHandlerTimeout(30*time.Millisecond))
	t.Cleanup(func() {
		_ = kernelRuntime.EventBus().Close(context.Background())
	})

	type deadlineObservation struct {
		hasDeadline bool
		remaining   time.Duration
	}

	observed := make(chan deadlineObservation, 1)
	module := &stubModule{
		name: "declarative-timeout-override",
		spec: core.ModuleSpec{
			Handlers: []core.ModuleHandler{
				{
					Capability: core.Capability{
						Name: "timeout-observer",
						Interest: core.InterestSet{
							Kinds: []platform.EventKind{platform.EventKindArticleCreated},
						},
					},
					Subscription: core.SubscriptionSpec{
						Name:           "declarative-timeout-handler",
						Buffer:         1,
						Workers:        1,
						HandlerTimeout: 250 * time.Millisecond,
					},
					Handler: func(ctx context.Context, _ *platform.Event) error {
						deadline, hasDeadline := ctx.Deadline()
						remaining := time.Duration(0)
						if hasDeadline {
							remaining = time.Until(deadline)
						}
						observed <- deadlineObservation{
							hasDeadline: hasDeadline,
							remaining:   remaining,
						}

						return nil
					},
				},
			},
		},
	}

	if err := kernelRuntime.RegisterModule(context.Background(), module); err != nil {
		t.Fatalf("register module failed: %v", err)
	}

	if err := kernelRuntime.EventBus().Publish(
		context.Background(),
		newTestEvent("e-timeout-override", platform.EventKindArticleCreated),
	); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	select {
	case got := <-observed:
		if !got.hasDeadline {
			t.Fatal("handler context missing deadline")
		}
		if got.remaining < 150*time.Millisecond {
			t.Fatalf("handler deadline remaining = %s, want at least 150ms for declarative override", got.remaining)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler observation")
	}
}

// TestRegisterModuleImperativeSubscriptionCapabilityGate verifies imperative subscriptions
// remain possible, but only when capabilities are explicitly declared.
func TestRegisterModuleImperativeSubscriptionCapabilityGate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		spec    core.ModuleSpec
		wantErr bool
	}{
		{
			name:    "missing capability fails",
			spec:    core.ModuleSpec{},
			wantErr: true,
		},
		{
			name: "additional capability allows imperative subscribe",
			spec: core.ModuleSpec{
				AdditionalCapabilities: []core.Capability{
					{
						Name: "imperative-capability",
						Interest: core.InterestSet{
							Kinds: []platform.EventKind{platform.EventKindArticleCreated},
						},
					},
				},
			},
			wantErr: false,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			kernelRuntime := newTestKernel(t)
			t.Cleanup(func() {
				_ = kernelRuntime.EventBus().Close(context.Background())
			})

			module := &stubModule{
				name: "imperative",
				spec: testCase.spec,
				onRegister: func(ctx context.Context, runtime core.ModuleRuntime) error {
					_, err := runtime.Subscribe(ctx, core.InterestSet{
						Kinds: []platform.EventKind{platform.EventKindArticleCreated},
					}, core.SubscriptionSpec{
						Name: "imperative-handler",
					}, func(_ context.Context, _ *platform.Event) error {
						return nil
					})
					if err != nil {
						return fmt.Errorf("subscribe imperative handler: %w", err)
					}

					return nil
				},
			}

			err := kernelRuntime.RegisterModule(context.Background(), module)
			if testCase.wantErr && err == nil {
				t.Fatal("expected module registration error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected module registration error: %v", err)
			}
		})
	}
}

// TestRegisterModuleSpecValidation verifies declarative spec validation failures.
func TestRegisterModuleSpecValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		spec       core.ModuleSpec
		wantErrSub string
	}{
		{
			name: "empty handler capability name",
			spec: core.ModuleSpec{
				Handlers: []core.ModuleHandler{
					{
						Capability: core.Capability{
							Interest: core.InterestSet{
								Kinds: []platform.EventKind{platform.EventKindArticleCreated},
							},
						},
						Handler: func(_ context.Context, _ *platform.Event) error {
							return nil
						},
					},
				},
			},
			wantErrSub: "empty capability name",
		},
		{
			name: "duplicate capability name",
			spec: core.ModuleSpec{
				Handlers: []core.ModuleHandler{
					{
						Capability: core.Capability{
							Name: "dup",
							Interest: core.InterestSet{
								Kinds: []platform.EventKind{platform.EventKindArticleCreated},
							},
						},
						Handler: func(_ context.Context, _ *platform.Event) error {
							return nil
						},
					},
					{
						Capability: core.Capability{
							Name: "dup",
							Interest: core.InterestSet{
								Kinds: []platform.EventKind{platform.EventKindArticleEdited},
							},
						},
						Handler: func(_ context.Context, _ *platform.Event) error {
							return nil
						},
					},
				},
			},
			wantErrSub: "duplicate capability name",
		},
		{
			name: "nil handler",
			spec: core.ModuleSpec{
				Handlers: []core.ModuleHandler{
					{
						Capability: core.Capability{
							Name: "nil-handler",
							Interest: core.InterestSet{
								Kinds: []platform.EventKind{platform.EventKindArticleCreated},
							},
						},
					},
				},
			},
			wantErrSub: "nil handler",
		},
		{
			name: "duplicate subscription name",
			spec: core.ModuleSpec{
				Handlers: []core.ModuleHandler{
					{
						Capability: core.Capability{
							Name: "a",
							Interest: core.InterestSet{
								Kinds: []platform.EventKind{platform.EventKindArticleCreated},
							},
						},
						Subscription: core.SubscriptionSpec{Name: "dup-sub"},
						Handler: func(_ context.Context, _ *platform.Event) error {
							return nil
						},
					},
					{
						Capability: core.Capability{
							Name: "b",
							Interest: core.InterestSet{
								Kinds: []platform.EventKind{platform.EventKindArticleEdited},
							},
						},
						Subscription: core.SubscriptionSpec{Name: "dup-sub"},
						Handler: func(_ context.Context, _ *platform.Event) error {
							return nil
						},
					},
				},
			},
			wantErrSub: "duplicate subscription name",
		},
		{
			name: "duplicate additional capability name",
			spec: core.ModuleSpec{
				Handlers: []core.ModuleHandler{
					{
						Capability: core.Capability{
							Name: "cap",
							Interest: core.InterestSet{
								Kinds: []platform.EventKind{platform.EventKindArticleCreated},
							},
						},
						Handler: func(_ context.Context, _ *platform.Event) error {
							return nil
						},
					},
				},
				AdditionalCapabilities: []core.Capability{
					{Name: "cap"},
				},
			},
			wantErrSub: "duplicate capability name",
		},
		{
			name: "invalid command spec",
			spec: core.ModuleSpec{
				Commands: []platform.CommandSpec{
					{
						Prefix: platform.CommandPrefixOrdinary,
					},
				},
			},
			wantErrSub: "module command 0",
		},
		{
			name: "duplicate command declaration",
			spec: core.ModuleSpec{
				Commands: []platform.CommandSpec{
					{
						Prefix: platform.CommandPrefixOrdinary,
						Name:   "raw",
					},
					{
						Prefix: platform.CommandPrefixOrdinary,
						Name:   "raw",
					},
				},
			},
			wantErrSub: "duplicate command /raw",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			kernelRuntime := newTestKernel(t)
			module := &stubModule{
				name: "invalid",
				spec: testCase.spec,
			}

			err := kernelRuntime.RegisterModule(context.Background(), module)
			if err == nil {
				t.Fatal("expected module registration error")
			}
			if !strings.Contains(err.Error(), testCase.wantErrSub) {
				t.Fatalf("error = %v, want substring %q", err, testCase.wantErrSub)
			}
		})
	}
}

func TestKernelProvidesCommandCatalogService(t *testing.T) {
	t.Parallel()

	kernelRuntime := newTestKernel(t)
	catalog, err := core.ResolveAs[core.CommandCatalog](
		kernelRuntime.Services(),
		core.ServiceCommandCatalog,
	)
	if err != nil {
		t.Fatalf("resolve command catalog failed: %v", err)
	}

	module := &stubModule{
		name: "catalog-provider",
		spec: core.ModuleSpec{
			Commands: []platform.CommandSpec{
				{Prefix: platform.CommandPrefixSystem, Name: "raw"},
				{Prefix: platform.CommandPrefixOrdinary, Name: "ping"},
			},
		},
	}
	if err := kernelRuntime.RegisterModule(context.Background(), module); err != nil {
		t.Fatalf("register module failed: %v", err)
	}

	commands, err := catalog.ListCommands(context.Background())
	if err != nil {
		t.Fatalf("list commands failed: %v", err)
	}
	if len(commands) != 2 {
		t.Fatalf("commands len = %d, want 2", len(commands))
	}
	if commands[0].ModuleName != "catalog-provider" {
		t.Fatalf("commands[0].module_name = %q, want catalog-provider", commands[0].ModuleName)
	}
	if commands[0].Command.Prefix != platform.CommandPrefixOrdinary || commands[0].Command.Name != "ping" {
		t.Fatalf("commands[0] = %+v, want /ping", commands[0])
	}
	if commands[1].ModuleName != "catalog-provider" {
		t.Fatalf("commands[1].module_name = %q, want catalog-provider", commands[1].ModuleName)
	}
	if commands[1].Command.Prefix != platform.CommandPrefixSystem || commands[1].Command.Name != "raw" {
		t.Fatalf("commands[1] = %+v, want ~raw", commands[1])
	}
}

type stubModule struct {
	name string
	spec core.ModuleSpec

	onRegister func(ctx context.Context, runtime core.ModuleRuntime) error

	registered atomic.Int32
	started    atomic.Int32
	shutdown   atomic.Int32
}

func (m *stubModule) Name() string {
	return m.name
}

func (m *stubModule) Spec() core.ModuleSpec {
	return m.spec
}

func (m *stubModule) OnRegister(ctx context.Context, runtime core.ModuleRuntime) error {
	m.registered.Add(1)
	if m.onRegister != nil {
		if err := m.onRegister(ctx, runtime); err != nil {
			return err
		}
	}

	return nil
}

func (m *stubModule) OnStart(_ context.Context) error {
	m.started.Add(1)
	return nil
}

func (m *stubModule) OnShutdown(_ context.Context) error {
	m.shutdown.Add(1)
	return nil
}

type stubDriver struct {
	name string

	started atomic.Int32
	stopped atomic.Int32
}

func (d *stubDriver) Name() string {
	return d.name
}

func (d *stubDriver) Start(ctx context.Context, _ core.EventDispatcher) error {
	d.started.Add(1)
	<-ctx.Done()
	return nil
}

func (d *stubDriver) Shutdown(_ context.Context) error {
	d.stopped.Add(1)
	return nil
}
