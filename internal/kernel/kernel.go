package kernel

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"ex-otogi/pkg/otogi"
)

// Kernel is the framework core orchestrating modules, drivers, and the event bus.
type Kernel struct {
	cfg config

	bus      *EventBus
	services *ServiceRegistry

	mu          sync.RWMutex
	modules     map[string]*moduleRecord
	moduleOrder []string
	commands    map[string]commandRegistration
	drivers     map[string]otogi.Driver
	driverOrder []string

	runMu   sync.Mutex
	running bool
}

// New creates a new kernel runtime.
func New(options ...Option) *Kernel {
	cfg := defaultConfig()
	for _, option := range options {
		option(&cfg)
	}

	services := NewServiceRegistry()
	bus := NewEventBus(
		cfg.subscriptionBuffer,
		cfg.subscriptionWorker,
		cfg.handlerTimeout,
		cfg.onAsyncError,
	)

	kernelRuntime := &Kernel{
		cfg:         cfg,
		bus:         bus,
		services:    services,
		modules:     make(map[string]*moduleRecord),
		commands:    make(map[string]commandRegistration),
		drivers:     make(map[string]otogi.Driver),
		moduleOrder: make([]string, 0),
		driverOrder: make([]string, 0),
	}
	if err := kernelRuntime.services.Register(
		otogi.ServiceCommandCatalog,
		&kernelCommandCatalog{kernel: kernelRuntime},
	); err != nil {
		cfg.onAsyncError(context.Background(), "register command catalog service", err)
	}

	return kernelRuntime
}

// EventBus exposes the kernel event bus to integration code.
func (k *Kernel) EventBus() otogi.EventBus {
	return k.bus
}

// Services exposes the kernel service registry.
func (k *Kernel) Services() otogi.ServiceRegistry {
	return k.services
}

// RegisterService registers a runtime service singleton.
func (k *Kernel) RegisterService(name string, service any) error {
	if err := k.services.Register(name, service); err != nil {
		return fmt.Errorf("register service %s: %w", name, err)
	}

	return nil
}

// RegisterModule registers a lifecycle-aware module, runs optional registration,
// and wires declarative handlers.
func (k *Kernel) RegisterModule(ctx context.Context, module otogi.Module) error {
	if module == nil {
		return fmt.Errorf("register module: nil module")
	}
	name := module.Name()
	if name == "" {
		return fmt.Errorf("register module: empty module name")
	}
	moduleSpec := module.Spec()
	if err := validateModuleSpec(moduleSpec); err != nil {
		return fmt.Errorf("register module %s: %w", name, err)
	}

	record := &moduleRecord{
		name:         name,
		module:       module,
		capabilities: moduleSpec.Capabilities(),
	}
	if err := k.validateCapabilityDependencies(record.capabilities); err != nil {
		return fmt.Errorf("register module %s: %w", name, err)
	}

	k.mu.Lock()
	if _, exists := k.modules[name]; exists {
		k.mu.Unlock()
		return fmt.Errorf("register module %s: %w", name, otogi.ErrModuleAlreadyRegistered)
	}
	k.modules[name] = record
	k.moduleOrder = append(k.moduleOrder, name)
	k.mu.Unlock()

	runtime := &moduleRuntime{
		moduleName:    name,
		serviceLookup: k.services,
		bus:           k.bus,
		record:        record,
		defaultSink:   k.moduleRouteFor(name).Sink,
	}

	if err := k.registerModuleCommands(ctx, name, moduleSpec.Commands); err != nil {
		k.rollbackModuleRegistration(ctx, name, record)
		return fmt.Errorf("register module %s: %w", name, err)
	}

	hookCtx, cancel := context.WithTimeout(ctx, k.cfg.moduleHookTimeout)
	defer cancel()

	registrar, hasRegistrar := module.(otogi.ModuleRegistrar)
	if hasRegistrar {
		if err := runSafely("module "+name+" OnRegister", func() error {
			return registrar.OnRegister(hookCtx, runtime)
		}); err != nil {
			k.rollbackModuleRegistration(ctx, name, record)
			return fmt.Errorf("register module %s: %w", name, err)
		}
	}

	if err := k.registerDeclaredHandlers(hookCtx, name, runtime, moduleSpec.Handlers); err != nil {
		k.rollbackModuleRegistration(ctx, name, record)
		return fmt.Errorf("register module %s: %w", name, err)
	}

	return nil
}

// RegisterDriver registers a platform driver.
func (k *Kernel) RegisterDriver(driver otogi.Driver) error {
	if driver == nil {
		return fmt.Errorf("register driver: nil driver")
	}
	name := driver.Name()
	if name == "" {
		return fmt.Errorf("register driver: empty name")
	}

	k.mu.Lock()
	defer k.mu.Unlock()

	if _, exists := k.drivers[name]; exists {
		return fmt.Errorf("register driver %s: %w", name, otogi.ErrDriverAlreadyRegistered)
	}

	k.drivers[name] = driver
	k.driverOrder = append(k.driverOrder, name)

	return nil
}

// Run starts modules, runs drivers, and blocks until cancellation or fatal driver error.
func (k *Kernel) Run(ctx context.Context) error {
	if err := k.startRun(); err != nil {
		return err
	}
	defer k.finishRun()

	if err := k.startModules(ctx); err != nil {
		return err
	}

	runCtx, runCancel := context.WithCancel(ctx)
	driverErr, waitDrivers := k.startDrivers(runCtx)

	var runErr error
	select {
	case <-ctx.Done():
		runErr = ctx.Err()
	case err := <-driverErr:
		runErr = err
	}

	runCancel()
	waitDrivers()

	shutdownErr := k.shutdownAll(ctx)

	if isContextCancellation(runErr) {
		runErr = nil
	}
	if runErr != nil && shutdownErr != nil {
		return errors.Join(runErr, shutdownErr)
	}
	if runErr != nil {
		return runErr
	}
	if shutdownErr != nil {
		return shutdownErr
	}

	return nil
}

// startRun serializes Run invocations and rejects concurrent starts.
func (k *Kernel) startRun() error {
	k.runMu.Lock()
	defer k.runMu.Unlock()

	if k.running {
		return fmt.Errorf("kernel run: already running")
	}
	k.running = true

	return nil
}

// finishRun releases the single-run guard set by startRun.
func (k *Kernel) finishRun() {
	k.runMu.Lock()
	k.running = false
	k.runMu.Unlock()
}

// startModules invokes OnStart in registration order with per-module timeouts.
func (k *Kernel) startModules(ctx context.Context) error {
	k.mu.RLock()
	order := append([]string(nil), k.moduleOrder...)
	modules := make(map[string]*moduleRecord, len(k.modules))
	for name, module := range k.modules {
		modules[name] = module
	}
	k.mu.RUnlock()

	for _, name := range order {
		record, exists := modules[name]
		if !exists {
			continue
		}
		hookCtx, cancel := context.WithTimeout(ctx, k.cfg.moduleHookTimeout)
		err := runSafely("module "+name+" OnStart", func() error {
			return record.module.OnStart(hookCtx)
		})
		cancel()
		if err != nil {
			return fmt.Errorf("start module %s: %w", name, err)
		}
	}

	return nil
}

// startDrivers runs all registered drivers concurrently and returns:
// - an error channel delivering the first fatal driver error, and
// - a wait function that blocks for driver completion up to shutdown timeout.
func (k *Kernel) startDrivers(ctx context.Context) (<-chan error, func()) {
	errChannel := make(chan error, 1)
	done := make(chan struct{})
	workerWG := &sync.WaitGroup{}

	k.mu.RLock()
	order := append([]string(nil), k.driverOrder...)
	drivers := make(map[string]otogi.Driver, len(k.drivers))
	for name, driver := range k.drivers {
		drivers[name] = driver
	}
	k.mu.RUnlock()

	driverSink := k.newDriverEventSink()

	for _, name := range order {
		driver := drivers[name]
		if driver == nil {
			continue
		}

		workerWG.Add(1)
		go func(driverName string, adapter otogi.Driver) {
			defer workerWG.Done()
			err := runSafely("driver "+driverName+" Start", func() error {
				return adapter.Start(ctx, driverSink)
			})
			if err == nil || isContextCancellation(err) {
				return
			}
			select {
			case errChannel <- fmt.Errorf("run driver %s: %w", driverName, err):
			default:
			}
		}(name, driver)
	}

	go func() {
		workerWG.Wait()
		close(done)
	}()

	wait := func() {
		select {
		case <-done:
		case <-time.After(k.cfg.shutdownTimeout):
		}
	}

	go func() {
		<-done
		select {
		case errChannel <- context.Canceled:
		default:
		}
	}()

	return errChannel, wait
}

// shutdownAll tears down drivers, modules, and bus in a bounded timeout window.
// It uses WithoutCancel to ensure cleanup still runs after parent cancellation.
func (k *Kernel) shutdownAll(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), k.cfg.shutdownTimeout)
	defer cancel()

	var shutdownErr error
	if err := k.shutdownDrivers(shutdownCtx); err != nil {
		shutdownErr = errors.Join(shutdownErr, err)
	}
	if err := k.shutdownModules(shutdownCtx); err != nil {
		shutdownErr = errors.Join(shutdownErr, err)
	}
	if err := k.bus.Close(shutdownCtx); err != nil {
		shutdownErr = errors.Join(shutdownErr, err)
	}

	if shutdownErr != nil {
		return fmt.Errorf("kernel shutdown: %w", shutdownErr)
	}

	return nil
}

// shutdownDrivers executes driver Shutdown in reverse registration order.
func (k *Kernel) shutdownDrivers(ctx context.Context) error {
	k.mu.RLock()
	order := append([]string(nil), k.driverOrder...)
	drivers := make(map[string]otogi.Driver, len(k.drivers))
	for name, driver := range k.drivers {
		drivers[name] = driver
	}
	k.mu.RUnlock()

	var shutdownErr error
	for idx := len(order) - 1; idx >= 0; idx-- {
		name := order[idx]
		driver := drivers[name]
		if driver == nil {
			continue
		}
		err := runSafely("driver "+name+" Shutdown", func() error {
			return driver.Shutdown(ctx)
		})
		if err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("shutdown driver %s: %w", name, err))
		}
	}

	return shutdownErr
}

// shutdownModules closes module subscriptions and invokes OnShutdown in reverse order.
func (k *Kernel) shutdownModules(ctx context.Context) error {
	k.mu.RLock()
	order := append([]string(nil), k.moduleOrder...)
	modules := make(map[string]*moduleRecord, len(k.modules))
	for name, module := range k.modules {
		modules[name] = module
	}
	k.mu.RUnlock()

	var shutdownErr error
	for idx := len(order) - 1; idx >= 0; idx-- {
		name := order[idx]
		record := modules[name]
		if record == nil {
			continue
		}
		if err := record.closeSubscriptions(ctx); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("shutdown module %s subscriptions: %w", name, err))
		}
		hookCtx, cancel := context.WithTimeout(ctx, k.cfg.moduleHookTimeout)
		err := runSafely("module "+name+" OnShutdown", func() error {
			return record.module.OnShutdown(hookCtx)
		})
		cancel()
		if err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("shutdown module %s: %w", name, err))
		}
	}

	return shutdownErr
}

// rollbackModuleRegistration removes a partially registered module after OnRegister failure.
// It attempts best-effort subscription cleanup before removing registry entries.
func (k *Kernel) rollbackModuleRegistration(ctx context.Context, name string, record *moduleRecord) {
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), k.cfg.moduleHookTimeout)
	defer cancel()

	if err := record.closeSubscriptions(rollbackCtx); err != nil {
		k.cfg.onAsyncError(rollbackCtx, "rollback_module_registration", err)
	}
	k.unregisterModuleCommands(name)

	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.modules, name)
	k.moduleOrder = removeOrderedName(k.moduleOrder, name)
}

// validateCapabilityDependencies checks required services declared by capabilities.
func (k *Kernel) validateCapabilityDependencies(capabilities []otogi.Capability) error {
	for _, capability := range capabilities {
		for _, serviceName := range capability.RequiredServices {
			_, err := k.services.Resolve(serviceName)
			if err != nil {
				return fmt.Errorf(
					"capability %s requires service %s: %w",
					capability.Name,
					serviceName,
					err,
				)
			}
		}
	}

	return nil
}

// registerDeclaredHandlers binds all declarative handlers from ModuleSpec.
func (k *Kernel) registerDeclaredHandlers(
	ctx context.Context,
	moduleName string,
	runtime *moduleRuntime,
	handlers []otogi.ModuleHandler,
) error {
	route := k.moduleRouteFor(moduleName)
	for idx, declared := range handlers {
		capabilityName := declared.Capability.Name
		spec := declared.Subscription
		interest := declared.Capability.Interest
		if len(route.Sources) > 0 {
			interest.Sources = append([]otogi.EventSource(nil), route.Sources...)
		}
		if spec.Name == "" {
			spec.Name = fmt.Sprintf("%s-handler-%d", moduleName, idx+1)
		}
		if _, err := runtime.Subscribe(ctx, interest, spec, declared.Handler); err != nil {
			return fmt.Errorf("register handler %s for capability %s: %w", spec.Name, capabilityName, err)
		}
	}

	return nil
}

func (k *Kernel) moduleRouteFor(moduleName string) ModuleRoute {
	if route, exists := k.cfg.routing.moduleRoutes[moduleName]; exists {
		return route
	}
	if k.cfg.routing.defaultRoute != nil {
		return *k.cfg.routing.defaultRoute
	}

	return ModuleRoute{}
}

// validateModuleSpec ensures declarative module definitions are coherent.
func validateModuleSpec(spec otogi.ModuleSpec) error {
	seenCapabilities := make(map[string]struct{}, len(spec.Handlers)+len(spec.AdditionalCapabilities))
	seenSubscriptions := make(map[string]struct{}, len(spec.Handlers))
	seenCommands := make(map[string]struct{}, len(spec.Commands))

	for idx, handler := range spec.Handlers {
		if handler.Capability.Name == "" {
			return fmt.Errorf("module handler %d: empty capability name", idx)
		}
		if _, exists := seenCapabilities[handler.Capability.Name]; exists {
			return fmt.Errorf("module handler %d: duplicate capability name %s", idx, handler.Capability.Name)
		}
		seenCapabilities[handler.Capability.Name] = struct{}{}

		if handler.Handler == nil {
			return fmt.Errorf("module handler %s: nil handler", handler.Capability.Name)
		}
		if handler.Subscription.Name != "" {
			if _, exists := seenSubscriptions[handler.Subscription.Name]; exists {
				return fmt.Errorf("module handler %s: duplicate subscription name %s", handler.Capability.Name, handler.Subscription.Name)
			}
			seenSubscriptions[handler.Subscription.Name] = struct{}{}
		}
	}

	for idx, capability := range spec.AdditionalCapabilities {
		if capability.Name == "" {
			return fmt.Errorf("additional capability %d: empty capability name", idx)
		}
		if _, exists := seenCapabilities[capability.Name]; exists {
			return fmt.Errorf("additional capability %d: duplicate capability name %s", idx, capability.Name)
		}
		seenCapabilities[capability.Name] = struct{}{}
	}

	for idx, command := range spec.Commands {
		if err := command.Validate(); err != nil {
			return fmt.Errorf("module command %d: %w", idx, err)
		}
		key := commandRegistryKey(command.Prefix, command.Name)
		if _, exists := seenCommands[key]; exists {
			return fmt.Errorf(
				"module command %d: duplicate command %s",
				idx,
				formatCommandKey(command.Prefix, command.Name),
			)
		}
		seenCommands[key] = struct{}{}
	}

	return nil
}

// removeOrderedName removes one name while preserving remaining order.
func removeOrderedName(ordered []string, target string) []string {
	filtered := make([]string, 0, len(ordered))
	for _, item := range ordered {
		if item != target {
			filtered = append(filtered, item)
		}
	}

	return filtered
}

// isContextCancellation reports whether err is a context-driven termination signal.
func isContextCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
