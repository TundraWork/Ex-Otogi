package kernel

import (
	"context"
	"errors"
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
				capabilities: []otogi.Capability{
					{Name: "needs-logger", RequiredServices: []string{"logger"}},
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

type stubModule struct {
	name         string
	capabilities []otogi.Capability

	registered atomic.Int32
	started    atomic.Int32
	shutdown   atomic.Int32
}

func (m *stubModule) Name() string {
	return m.name
}

func (m *stubModule) Capabilities() []otogi.Capability {
	return m.capabilities
}

func (m *stubModule) OnRegister(_ context.Context, _ otogi.ModuleRuntime) error {
	m.registered.Add(1)
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

func (d *stubDriver) Start(ctx context.Context, _ otogi.EventSink) error {
	d.started.Add(1)
	<-ctx.Done()
	return nil
}

func (d *stubDriver) Shutdown(_ context.Context) error {
	d.stopped.Add(1)
	return nil
}
