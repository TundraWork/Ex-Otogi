package kernel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ex-otogi/pkg/otogi"
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

			kernelRuntime := New()
			if testCase.registerLogger {
				if err := kernelRuntime.RegisterService("logger", struct{}{}); err != nil {
					t.Fatalf("register logger service failed: %v", err)
				}
			}

			module := &stubModule{
				name: "cap-module",
				spec: otogi.ModuleSpec{
					AdditionalCapabilities: []otogi.Capability{
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

// TestKernelRunCallsModuleLifecycle verifies lifecycle hook execution during run/shutdown.
func TestKernelRunCallsModuleLifecycle(t *testing.T) {
	t.Parallel()

	kernelRuntime := New()
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

	kernelRuntime := New()
	t.Cleanup(func() {
		_ = kernelRuntime.EventBus().Close(context.Background())
	})

	handled := make(chan string, 1)
	module := &stubModule{
		name: "declarative",
		spec: otogi.ModuleSpec{
			Handlers: []otogi.ModuleHandler{
				{
					Capability: otogi.Capability{
						Name: "message-created",
						Interest: otogi.InterestSet{
							Kinds: []otogi.EventKind{otogi.EventKindArticleCreated},
						},
					},
					Subscription: otogi.SubscriptionSpec{
						Name:    "declarative-handler",
						Buffer:  1,
						Workers: 1,
					},
					Handler: func(_ context.Context, event *otogi.Event) error {
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

	if err := kernelRuntime.EventBus().Publish(context.Background(), newTestEvent("e1", otogi.EventKindArticleCreated)); err != nil {
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

// TestRegisterModuleImperativeSubscriptionCapabilityGate verifies imperative subscriptions
// remain possible, but only when capabilities are explicitly declared.
func TestRegisterModuleImperativeSubscriptionCapabilityGate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		spec    otogi.ModuleSpec
		wantErr bool
	}{
		{
			name:    "missing capability fails",
			spec:    otogi.ModuleSpec{},
			wantErr: true,
		},
		{
			name: "additional capability allows imperative subscribe",
			spec: otogi.ModuleSpec{
				AdditionalCapabilities: []otogi.Capability{
					{
						Name: "imperative-capability",
						Interest: otogi.InterestSet{
							Kinds: []otogi.EventKind{otogi.EventKindArticleCreated},
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

			kernelRuntime := New()
			t.Cleanup(func() {
				_ = kernelRuntime.EventBus().Close(context.Background())
			})

			module := &stubModule{
				name: "imperative",
				spec: testCase.spec,
				onRegister: func(ctx context.Context, runtime otogi.ModuleRuntime) error {
					_, err := runtime.Subscribe(ctx, otogi.InterestSet{
						Kinds: []otogi.EventKind{otogi.EventKindArticleCreated},
					}, otogi.SubscriptionSpec{
						Name: "imperative-handler",
					}, func(_ context.Context, _ *otogi.Event) error {
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
		spec       otogi.ModuleSpec
		wantErrSub string
	}{
		{
			name: "empty handler capability name",
			spec: otogi.ModuleSpec{
				Handlers: []otogi.ModuleHandler{
					{
						Capability: otogi.Capability{
							Interest: otogi.InterestSet{
								Kinds: []otogi.EventKind{otogi.EventKindArticleCreated},
							},
						},
						Handler: func(_ context.Context, _ *otogi.Event) error {
							return nil
						},
					},
				},
			},
			wantErrSub: "empty capability name",
		},
		{
			name: "duplicate capability name",
			spec: otogi.ModuleSpec{
				Handlers: []otogi.ModuleHandler{
					{
						Capability: otogi.Capability{
							Name: "dup",
							Interest: otogi.InterestSet{
								Kinds: []otogi.EventKind{otogi.EventKindArticleCreated},
							},
						},
						Handler: func(_ context.Context, _ *otogi.Event) error {
							return nil
						},
					},
					{
						Capability: otogi.Capability{
							Name: "dup",
							Interest: otogi.InterestSet{
								Kinds: []otogi.EventKind{otogi.EventKindArticleEdited},
							},
						},
						Handler: func(_ context.Context, _ *otogi.Event) error {
							return nil
						},
					},
				},
			},
			wantErrSub: "duplicate capability name",
		},
		{
			name: "nil handler",
			spec: otogi.ModuleSpec{
				Handlers: []otogi.ModuleHandler{
					{
						Capability: otogi.Capability{
							Name: "nil-handler",
							Interest: otogi.InterestSet{
								Kinds: []otogi.EventKind{otogi.EventKindArticleCreated},
							},
						},
					},
				},
			},
			wantErrSub: "nil handler",
		},
		{
			name: "duplicate subscription name",
			spec: otogi.ModuleSpec{
				Handlers: []otogi.ModuleHandler{
					{
						Capability: otogi.Capability{
							Name: "a",
							Interest: otogi.InterestSet{
								Kinds: []otogi.EventKind{otogi.EventKindArticleCreated},
							},
						},
						Subscription: otogi.SubscriptionSpec{Name: "dup-sub"},
						Handler: func(_ context.Context, _ *otogi.Event) error {
							return nil
						},
					},
					{
						Capability: otogi.Capability{
							Name: "b",
							Interest: otogi.InterestSet{
								Kinds: []otogi.EventKind{otogi.EventKindArticleEdited},
							},
						},
						Subscription: otogi.SubscriptionSpec{Name: "dup-sub"},
						Handler: func(_ context.Context, _ *otogi.Event) error {
							return nil
						},
					},
				},
			},
			wantErrSub: "duplicate subscription name",
		},
		{
			name: "duplicate additional capability name",
			spec: otogi.ModuleSpec{
				Handlers: []otogi.ModuleHandler{
					{
						Capability: otogi.Capability{
							Name: "cap",
							Interest: otogi.InterestSet{
								Kinds: []otogi.EventKind{otogi.EventKindArticleCreated},
							},
						},
						Handler: func(_ context.Context, _ *otogi.Event) error {
							return nil
						},
					},
				},
				AdditionalCapabilities: []otogi.Capability{
					{Name: "cap"},
				},
			},
			wantErrSub: "duplicate capability name",
		},
		{
			name: "invalid command spec",
			spec: otogi.ModuleSpec{
				Commands: []otogi.CommandSpec{
					{
						Prefix: otogi.CommandPrefixOrdinary,
					},
				},
			},
			wantErrSub: "module command 0",
		},
		{
			name: "duplicate command declaration",
			spec: otogi.ModuleSpec{
				Commands: []otogi.CommandSpec{
					{
						Prefix: otogi.CommandPrefixOrdinary,
						Name:   "raw",
					},
					{
						Prefix: otogi.CommandPrefixOrdinary,
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

			kernelRuntime := New()
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

	kernelRuntime := New()
	catalog, err := otogi.ResolveAs[otogi.CommandCatalog](
		kernelRuntime.Services(),
		otogi.ServiceCommandCatalog,
	)
	if err != nil {
		t.Fatalf("resolve command catalog failed: %v", err)
	}

	module := &stubModule{
		name: "catalog-provider",
		spec: otogi.ModuleSpec{
			Commands: []otogi.CommandSpec{
				{Prefix: otogi.CommandPrefixSystem, Name: "raw"},
				{Prefix: otogi.CommandPrefixOrdinary, Name: "ping"},
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
	if commands[0].Command.Prefix != otogi.CommandPrefixOrdinary || commands[0].Command.Name != "ping" {
		t.Fatalf("commands[0] = %+v, want /ping", commands[0])
	}
	if commands[1].ModuleName != "catalog-provider" {
		t.Fatalf("commands[1].module_name = %q, want catalog-provider", commands[1].ModuleName)
	}
	if commands[1].Command.Prefix != otogi.CommandPrefixSystem || commands[1].Command.Name != "raw" {
		t.Fatalf("commands[1] = %+v, want ~raw", commands[1])
	}
}

type stubModule struct {
	name string
	spec otogi.ModuleSpec

	onRegister func(ctx context.Context, runtime otogi.ModuleRuntime) error

	registered atomic.Int32
	started    atomic.Int32
	shutdown   atomic.Int32
}

func (m *stubModule) Name() string {
	return m.name
}

func (m *stubModule) Spec() otogi.ModuleSpec {
	return m.spec
}

func (m *stubModule) OnRegister(ctx context.Context, runtime otogi.ModuleRuntime) error {
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

func (d *stubDriver) Start(ctx context.Context, _ otogi.EventDispatcher) error {
	d.started.Add(1)
	<-ctx.Done()
	return nil
}

func (d *stubDriver) Shutdown(_ context.Context) error {
	d.stopped.Add(1)
	return nil
}
