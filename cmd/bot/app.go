package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"ex-otogi/internal/driver"
	"ex-otogi/internal/kernel"
	"ex-otogi/modules/duel"
	"ex-otogi/modules/help"
	"ex-otogi/modules/llmchat"
	"ex-otogi/modules/memory"
	"ex-otogi/modules/nbnhhsh"
	"ex-otogi/modules/pingpong"
	"ex-otogi/modules/quotehelper"
	"ex-otogi/modules/sleep"
	"ex-otogi/modules/whoami"
	"ex-otogi/pkg/otogi"
)

const (
	envConfigFile                 = "OTOGI_CONFIG_FILE"
	defaultConfigFilePath         = "config/bot.json"
	alternateConfigFilePath       = "bin/config/bot.json"
	defaultModuleLifecycleTimeout = 3 * time.Second
	defaultModuleHandlerTimeout   = 3 * time.Second
	defaultShutdownTimeout        = 10 * time.Second
	defaultSubscriptionBuffer     = 256
	defaultSubscriptionWorker     = 2
	stdlibLogSource               = "stdlib"
	stdlibLogComponent            = "google_genai_sdk"
	stdlibLogKindContextCanceled  = "context_canceled"
)

var runtimeModules = []func() otogi.Module{
	func() otogi.Module { return memory.New() },
	func() otogi.Module { return quotehelper.New() },
	func() otogi.Module { return duel.New() },
	func() otogi.Module { return nbnhhsh.New() },
	func() otogi.Module { return pingpong.New() },
	func() otogi.Module { return help.New() },
	func() otogi.Module { return sleep.New() },
	func() otogi.Module { return llmchat.New() },
	func() otogi.Module { return whoami.New() },
}

type appConfig struct {
	logLevel slog.Level

	moduleLifecycleTimeout time.Duration
	moduleHandlerTimeout   time.Duration
	shutdownTimeout        time.Duration
	subscriptionBuffer     int
	subscriptionWorkers    int

	drivers        []driver.Definition
	routingDefault *kernel.ModuleRoute
	moduleRoutes   map[string]kernel.ModuleRoute

	allowlistConversationIDs []string
	allowlistBypassCommands  []string

	moduleConfigs map[string]json.RawMessage
}

type fileConfig struct {
	LogLevel string                     `json:"log_level"`
	Kernel   fileKernelConfig           `json:"kernel"`
	Drivers  []fileDriverEntry          `json:"drivers"`
	Routing  fileRoutingConfig          `json:"routing"`
	Modules  map[string]json.RawMessage `json:"modules"`
}

type fileKernelConfig struct {
	ModuleLifecycleTimeout string                   `json:"module_lifecycle_timeout"`
	ModuleHandlerTimeout   string                   `json:"module_handler_timeout"`
	ShutdownTimeout        string                   `json:"shutdown_timeout"`
	SubscriptionBuffer     *int                     `json:"subscription_buffer"`
	SubscriptionWorkers    *int                     `json:"subscription_workers"`
	ChatAllowlist          *fileChatAllowlistConfig `json:"chat_allowlist"`
}

type fileChatAllowlistConfig struct {
	ConversationIDs []string `json:"conversation_ids"`
	BypassCommands  []string `json:"bypass_commands"`
}

type fileDriverEntry struct {
	Name    string          `json:"name"`
	Type    string          `json:"type"`
	Enabled *bool           `json:"enabled"`
	Config  json.RawMessage `json:"config"`
}

type fileRoutingConfig struct {
	Default *fileModuleRoute           `json:"default"`
	Modules map[string]fileModuleRoute `json:"modules"`
}

type fileModuleRoute struct {
	Sources []fileSourceRef `json:"sources"`
	Sink    *fileSinkRef    `json:"sink"`
}

type fileSourceRef struct {
	Platform string `json:"platform"`
	ID       string `json:"id"`
}

type fileSinkRef struct {
	Platform string `json:"platform"`
	ID       string `json:"id"`
}

func run() error {
	registry, err := driver.NewBuiltinRegistry()
	if err != nil {
		return fmt.Errorf("new builtin driver registry: %w", err)
	}

	cfg, err := loadConfig(registry)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.logLevel}))
	configureStdlibLogBridge(logger)
	kernelRuntime, err := buildKernelRuntime(logger, cfg)
	if err != nil {
		return fmt.Errorf("build kernel runtime: %w", err)
	}

	runtimes, err := buildDriverRuntime(context.Background(), logger, cfg, registry)
	if err != nil {
		return err
	}

	if err := registerRuntimeDrivers(kernelRuntime, runtimes.drivers); err != nil {
		return err
	}
	if err := registerRuntimeServices(kernelRuntime, logger, runtimes); err != nil {
		return err
	}
	if err := registerModuleConfigs(kernelRuntime, cfg.moduleConfigs); err != nil {
		return err
	}
	if err := registerRuntimeModules(context.Background(), kernelRuntime); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := kernelRuntime.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("run kernel: %w", err)
	}

	return nil
}

func configureStdlibLogBridge(logger *slog.Logger) {
	if logger == nil {
		return
	}

	bridgeLogger := logger.With(
		slog.String("source", stdlibLogSource),
		slog.String("component", stdlibLogComponent),
	)
	log.SetFlags(0)
	log.SetOutput(stdlibSlogWriter{logger: bridgeLogger})
}

type stdlibSlogWriter struct {
	logger *slog.Logger
}

func (w stdlibSlogWriter) Write(payload []byte) (int, error) {
	if w.logger == nil {
		return len(payload), nil
	}

	message := strings.TrimRight(string(payload), "\r\n")
	if message == "" {
		return len(payload), nil
	}

	if isKnownSDKContextCanceledLogLine(message) {
		w.logger.Warn(message, "sdk_log_kind", stdlibLogKindContextCanceled)
		return len(payload), nil
	}

	w.logger.Error(message)

	return len(payload), nil
}

func isKnownSDKContextCanceledLogLine(message string) bool {
	return strings.EqualFold(strings.TrimSpace(message), "Error context canceled")
}

func loadConfig(registry *driver.Registry) (appConfig, error) {
	cfg := defaultAppConfig()
	configFile, err := resolveConfigFilePath()
	if err != nil {
		return appConfig{}, err
	}

	if err := applyConfigFile(&cfg, configFile); err != nil {
		return appConfig{}, err
	}
	if err := validateAppConfig(&cfg, registry); err != nil {
		return appConfig{}, fmt.Errorf("validate config file %s: %w", configFile, err)
	}

	return cfg, nil
}

func resolveConfigFilePath() (string, error) {
	if configFile := strings.TrimSpace(os.Getenv(envConfigFile)); configFile != "" {
		return configFile, nil
	}

	candidates := []string{defaultConfigFilePath, alternateConfigFilePath}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil {
			if info.IsDir() {
				return "", fmt.Errorf("config file %s is a directory", candidate)
			}
			return candidate, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat config file %s: %w", candidate, err)
		}
	}

	return "", fmt.Errorf(
		"config file not found; create %s or %s, or set %s",
		defaultConfigFilePath,
		alternateConfigFilePath,
		envConfigFile,
	)
}

func defaultAppConfig() appConfig {
	return appConfig{
		logLevel: slog.LevelInfo,

		moduleLifecycleTimeout: defaultModuleLifecycleTimeout,
		moduleHandlerTimeout:   defaultModuleHandlerTimeout,
		shutdownTimeout:        defaultShutdownTimeout,
		subscriptionBuffer:     defaultSubscriptionBuffer,
		subscriptionWorkers:    defaultSubscriptionWorker,

		drivers:       make([]driver.Definition, 0),
		moduleRoutes:  make(map[string]kernel.ModuleRoute),
		moduleConfigs: make(map[string]json.RawMessage),
	}
}

func applyConfigFile(cfg *appConfig, path string) error {
	if cfg == nil {
		return fmt.Errorf("apply config file: nil config")
	}
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("config file path is required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config file %s: %w", path, err)
	}

	var parsed fileConfig
	if err := json.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("parse config file %s: %w", path, err)
	}
	if rawLevel := strings.TrimSpace(parsed.LogLevel); rawLevel != "" {
		level, err := parseLogLevel(rawLevel)
		if err != nil {
			return fmt.Errorf("parse log_level: %w", err)
		}
		cfg.logLevel = level
	}

	if rawTimeout := strings.TrimSpace(parsed.Kernel.ModuleLifecycleTimeout); rawTimeout != "" {
		timeout, err := time.ParseDuration(rawTimeout)
		if err != nil {
			return fmt.Errorf("parse kernel.module_lifecycle_timeout: %w", err)
		}
		if timeout <= 0 {
			return fmt.Errorf("parse kernel.module_lifecycle_timeout: must be > 0")
		}
		cfg.moduleLifecycleTimeout = timeout
	}
	if rawTimeout := strings.TrimSpace(parsed.Kernel.ModuleHandlerTimeout); rawTimeout != "" {
		timeout, err := time.ParseDuration(rawTimeout)
		if err != nil {
			return fmt.Errorf("parse kernel.module_handler_timeout: %w", err)
		}
		if timeout <= 0 {
			return fmt.Errorf("parse kernel.module_handler_timeout: must be > 0")
		}
		cfg.moduleHandlerTimeout = timeout
	}
	if rawTimeout := strings.TrimSpace(parsed.Kernel.ShutdownTimeout); rawTimeout != "" {
		timeout, err := time.ParseDuration(rawTimeout)
		if err != nil {
			return fmt.Errorf("parse kernel.shutdown_timeout: %w", err)
		}
		if timeout <= 0 {
			return fmt.Errorf("parse kernel.shutdown_timeout: must be > 0")
		}
		cfg.shutdownTimeout = timeout
	}
	if parsed.Kernel.SubscriptionBuffer != nil {
		if *parsed.Kernel.SubscriptionBuffer <= 0 {
			return fmt.Errorf("parse kernel.subscription_buffer: must be > 0")
		}
		cfg.subscriptionBuffer = *parsed.Kernel.SubscriptionBuffer
	}
	if parsed.Kernel.SubscriptionWorkers != nil {
		if *parsed.Kernel.SubscriptionWorkers <= 0 {
			return fmt.Errorf("parse kernel.subscription_workers: must be > 0")
		}
		cfg.subscriptionWorkers = *parsed.Kernel.SubscriptionWorkers
	}

	if parsed.Kernel.ChatAllowlist != nil {
		cfg.allowlistConversationIDs = append([]string(nil), parsed.Kernel.ChatAllowlist.ConversationIDs...)
		cfg.allowlistBypassCommands = append([]string(nil), parsed.Kernel.ChatAllowlist.BypassCommands...)
	}

	cfg.drivers = make([]driver.Definition, 0, len(parsed.Drivers))
	for index, entry := range parsed.Drivers {
		enabled := true
		if entry.Enabled != nil {
			enabled = *entry.Enabled
		}
		cfg.drivers = append(cfg.drivers, driver.Definition{
			Name:    strings.TrimSpace(entry.Name),
			Type:    strings.TrimSpace(entry.Type),
			Enabled: enabled,
			Config:  append([]byte(nil), entry.Config...),
		})
		if len(entry.Config) == 0 {
			return fmt.Errorf("parse drivers[%d].config: required", index)
		}
	}

	cfg.routingDefault = nil
	if parsed.Routing.Default != nil {
		route, err := parseModuleRoute(*parsed.Routing.Default, "routing.default")
		if err != nil {
			return err
		}
		cfg.routingDefault = &route
	}

	cfg.moduleRoutes = make(map[string]kernel.ModuleRoute, len(parsed.Routing.Modules))
	for moduleName, rawRoute := range parsed.Routing.Modules {
		route, err := parseModuleRoute(rawRoute, fmt.Sprintf("routing.modules.%s", moduleName))
		if err != nil {
			return err
		}
		cfg.moduleRoutes[moduleName] = route
	}

	cfg.moduleConfigs = make(map[string]json.RawMessage, len(parsed.Modules))
	for moduleName, raw := range parsed.Modules {
		cfg.moduleConfigs[moduleName] = append(json.RawMessage(nil), raw...)
	}

	return nil
}

func parseModuleRoute(raw fileModuleRoute, scope string) (kernel.ModuleRoute, error) {
	if len(raw.Sources) == 0 {
		return kernel.ModuleRoute{}, fmt.Errorf("%s.sources is required", scope)
	}
	if raw.Sink == nil {
		return kernel.ModuleRoute{}, fmt.Errorf("%s.sink is required", scope)
	}

	sources := make([]otogi.EventSource, 0, len(raw.Sources))
	for index, sourceRef := range raw.Sources {
		source := otogi.EventSource{
			Platform: otogi.Platform(strings.TrimSpace(sourceRef.Platform)),
			ID:       strings.TrimSpace(sourceRef.ID),
		}
		if source.Platform == "" && source.ID == "" {
			return kernel.ModuleRoute{}, fmt.Errorf("%s.sources[%d]: empty source reference", scope, index)
		}
		sources = append(sources, source)
	}

	sink := otogi.EventSink{
		Platform: otogi.Platform(strings.TrimSpace(raw.Sink.Platform)),
		ID:       strings.TrimSpace(raw.Sink.ID),
	}
	if sink.Platform == "" && sink.ID == "" {
		return kernel.ModuleRoute{}, fmt.Errorf("%s.sink: empty sink reference", scope)
	}

	return kernel.ModuleRoute{Sources: sources, Sink: &sink}, nil
}

func validateAppConfig(cfg *appConfig, registry *driver.Registry) error {
	if cfg == nil {
		return fmt.Errorf("nil config")
	}
	if registry == nil {
		return fmt.Errorf("nil driver registry")
	}

	enabledDrivers := make([]driver.Definition, 0, len(cfg.drivers))
	enabledByName := make(map[string]driver.Definition, len(cfg.drivers))
	for _, definition := range cfg.drivers {
		if definition.Name == "" {
			return fmt.Errorf("drivers[].name is required")
		}
		if definition.Type == "" {
			return fmt.Errorf("drivers[%s].type is required", definition.Name)
		}
		if _, exists := enabledByName[definition.Name]; exists {
			return fmt.Errorf("drivers[%s]: duplicate name", definition.Name)
		}
		if !definition.Enabled {
			continue
		}
		if _, err := registry.PlatformForType(definition.Type); err != nil {
			return fmt.Errorf("drivers[%s].type: %w", definition.Name, err)
		}
		enabledDrivers = append(enabledDrivers, definition)
		enabledByName[definition.Name] = definition
	}
	if len(enabledDrivers) == 0 {
		return fmt.Errorf("at least one enabled driver is required")
	}

	moduleNames := configuredRuntimeModuleNames()
	knownModules := make(map[string]struct{}, len(moduleNames))
	for _, moduleName := range moduleNames {
		knownModules[moduleName] = struct{}{}
	}
	for moduleName := range cfg.moduleRoutes {
		if _, known := knownModules[moduleName]; !known {
			return fmt.Errorf("routing.modules.%s: unknown module", moduleName)
		}
	}

	for moduleName, route := range cfg.moduleRoutes {
		if err := validateRouteRefs(route, enabledByName, fmt.Sprintf("routing.modules.%s", moduleName)); err != nil {
			return err
		}
	}
	if cfg.routingDefault != nil {
		if err := validateRouteRefs(*cfg.routingDefault, enabledByName, "routing.default"); err != nil {
			return err
		}
	}

	if len(enabledDrivers) == 1 && cfg.routingDefault == nil {
		sole := enabledDrivers[0]
		platform, err := registry.PlatformForType(sole.Type)
		if err != nil {
			return fmt.Errorf("derive default route from driver %s: %w", sole.Name, err)
		}
		cfg.routingDefault = &kernel.ModuleRoute{
			Sources: []otogi.EventSource{{Platform: platform, ID: sole.Name}},
			Sink:    &otogi.EventSink{Platform: platform, ID: sole.Name},
		}
	}

	if len(enabledDrivers) >= 2 && cfg.routingDefault == nil {
		for _, moduleName := range moduleNames {
			if _, exists := cfg.moduleRoutes[moduleName]; !exists {
				return fmt.Errorf("routing.default is required in multi-driver mode unless all modules override")
			}
		}
	}

	return nil
}

func configuredRuntimeModuleNames() []string {
	names := make([]string, 0, len(runtimeModules))
	for _, factory := range runtimeModules {
		names = append(names, factory().Name())
	}

	return names
}

func validateRouteRefs(
	route kernel.ModuleRoute,
	enabledByName map[string]driver.Definition,
	scope string,
) error {
	for index, source := range route.Sources {
		if source.ID != "" {
			if _, exists := enabledByName[source.ID]; !exists {
				return fmt.Errorf("%s.sources[%d]: unknown driver id %s", scope, index, source.ID)
			}
		}
	}
	if route.Sink != nil && route.Sink.ID != "" {
		if _, exists := enabledByName[route.Sink.ID]; !exists {
			return fmt.Errorf("%s.sink: unknown driver id %s", scope, route.Sink.ID)
		}
	}

	return nil
}

func parseLogLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unsupported level %q", raw)
	}
}

func buildKernelRuntime(logger *slog.Logger, cfg appConfig) (*kernel.Kernel, error) {
	kernelRuntime, err := kernel.New(
		kernel.WithLogger(logger),
		kernel.WithModuleHookTimeout(cfg.moduleLifecycleTimeout),
		kernel.WithDefaultHandlerTimeout(cfg.moduleHandlerTimeout),
		kernel.WithShutdownTimeout(cfg.shutdownTimeout),
		kernel.WithDefaultSubscriptionBuffer(cfg.subscriptionBuffer),
		kernel.WithDefaultSubscriptionWorkers(cfg.subscriptionWorkers),
		kernel.WithModuleRouting(cfg.routingDefault, cfg.moduleRoutes),
		kernel.WithChatAllowlist(cfg.allowlistConversationIDs, cfg.allowlistBypassCommands),
	)
	if err != nil {
		return nil, fmt.Errorf("build kernel runtime: %w", err)
	}

	return kernelRuntime, nil
}

type driverRuntimes struct {
	drivers              []otogi.Driver
	mediaDownloader      otogi.MediaDownloader
	sinkDispatcher       otogi.SinkDispatcher
	moderationDispatcher otogi.ModerationDispatcher
}

func buildDriverRuntime(
	ctx context.Context,
	logger *slog.Logger,
	cfg appConfig,
	registry *driver.Registry,
) (driverRuntimes, error) {
	if registry == nil {
		return driverRuntimes{}, fmt.Errorf("build drivers: nil driver registry")
	}

	runtimes, err := registry.BuildEnabled(ctx, cfg.drivers, logger)
	if err != nil {
		return driverRuntimes{}, fmt.Errorf("build drivers: %w", err)
	}

	drivers := make([]otogi.Driver, 0, len(runtimes))
	for _, runtime := range runtimes {
		drivers = append(drivers, runtime.Driver)
	}

	mediaDownloader, err := driver.NewMediaDownloader(runtimes)
	if err != nil {
		return driverRuntimes{}, fmt.Errorf("build media downloader: %w", err)
	}

	sinkDispatcher, err := driver.NewSinkDispatcher(runtimes)
	if err != nil {
		return driverRuntimes{}, fmt.Errorf("build sink dispatcher: %w", err)
	}

	moderationDispatcher, err := driver.NewModerationDispatcher(runtimes)
	if err != nil {
		return driverRuntimes{}, fmt.Errorf("build moderation dispatcher: %w", err)
	}

	return driverRuntimes{
		drivers:              drivers,
		mediaDownloader:      mediaDownloader,
		sinkDispatcher:       sinkDispatcher,
		moderationDispatcher: moderationDispatcher,
	}, nil
}

func registerRuntimeServices(
	kernelRuntime *kernel.Kernel,
	logger *slog.Logger,
	dr driverRuntimes,
) error {
	if err := kernelRuntime.RegisterService("logger", logger); err != nil {
		return fmt.Errorf("register logger service: %w", err)
	}
	if dr.sinkDispatcher == nil {
		return fmt.Errorf("register sink dispatcher service: nil dispatcher")
	}
	if err := kernelRuntime.RegisterService(otogi.ServiceSinkDispatcher, dr.sinkDispatcher); err != nil {
		return fmt.Errorf("register sink dispatcher service: %w", err)
	}
	if dr.mediaDownloader != nil {
		if err := kernelRuntime.RegisterService(otogi.ServiceMediaDownloader, dr.mediaDownloader); err != nil {
			return fmt.Errorf("register media downloader service: %w", err)
		}
	}
	if dr.moderationDispatcher != nil {
		if err := kernelRuntime.RegisterService(otogi.ServiceModerationDispatcher, dr.moderationDispatcher); err != nil {
			return fmt.Errorf("register moderation dispatcher service: %w", err)
		}
	}

	return nil
}

func registerModuleConfigs(kernelRuntime *kernel.Kernel, configs map[string]json.RawMessage) error {
	for moduleName, raw := range configs {
		if err := kernelRuntime.RegisterModuleConfig(moduleName, raw); err != nil {
			return fmt.Errorf("register module config %s: %w", moduleName, err)
		}
	}

	return nil
}

func registerRuntimeModules(ctx context.Context, kernelRuntime *kernel.Kernel) error {
	for _, factory := range runtimeModules {
		module := factory()
		if err := kernelRuntime.RegisterModule(ctx, module); err != nil {
			return fmt.Errorf("register module %s: %w", module.Name(), err)
		}
	}

	return nil
}

func registerRuntimeDrivers(kernelRuntime *kernel.Kernel, drivers []otogi.Driver) error {
	for _, runtimeDriver := range drivers {
		if err := kernelRuntime.RegisterDriver(runtimeDriver); err != nil {
			return fmt.Errorf("register driver %s: %w", runtimeDriver.Name(), err)
		}
	}

	return nil
}
