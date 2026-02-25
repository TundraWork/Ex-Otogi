package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"ex-otogi/internal/driver"
	"ex-otogi/internal/kernel"
	"ex-otogi/modules/help"
	"ex-otogi/modules/llmchat"
	"ex-otogi/modules/memory"
	"ex-otogi/modules/pingpong"
	"ex-otogi/pkg/llm"
	openaillm "ex-otogi/pkg/llm/providers/openai"
	"ex-otogi/pkg/otogi"
)

const (
	envConfigFile             = "OTOGI_CONFIG_FILE"
	envLLMConfigFile          = "OTOGI_LLM_CONFIG_FILE"
	defaultConfigFilePath     = "config/bot.json"
	alternateConfigFilePath   = "bin/config/bot.json"
	defaultModuleHookTimeout  = 3 * time.Second
	defaultShutdownTimeout    = 10 * time.Second
	defaultSubscriptionBuffer = 256
	defaultSubscriptionWorker = 2
)

var baseRuntimeModuleNames = []string{"memory", "pingpong", "help"}

type appConfig struct {
	logLevel slog.Level

	moduleHookTimeout   time.Duration
	shutdownTimeout     time.Duration
	subscriptionBuffer  int
	subscriptionWorkers int

	drivers        []driver.Definition
	routingDefault *kernel.ModuleRoute
	moduleRoutes   map[string]kernel.ModuleRoute

	llmConfigFile string
	llmConfig     *llmchat.Config
}

type fileConfig struct {
	LogLevel string            `json:"log_level"`
	Kernel   fileKernelConfig  `json:"kernel"`
	Drivers  []fileDriverEntry `json:"drivers"`
	Routing  fileRoutingConfig `json:"routing"`
	LLM      fileLLMConfig     `json:"llm"`
}

type fileKernelConfig struct {
	ModuleHookTimeout   string `json:"module_hook_timeout"`
	ShutdownTimeout     string `json:"shutdown_timeout"`
	SubscriptionBuffer  *int   `json:"subscription_buffer"`
	SubscriptionWorkers *int   `json:"subscription_workers"`
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

type fileLLMConfig struct {
	ConfigFile string `json:"config_file"`
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
	kernelRuntime := buildKernelRuntime(logger, cfg)

	drivers, sinkDispatcher, err := buildDriverRuntime(context.Background(), logger, cfg, registry)
	if err != nil {
		return err
	}

	if err := registerRuntimeDrivers(kernelRuntime, drivers); err != nil {
		return err
	}
	if err := registerRuntimeServices(kernelRuntime, logger, sinkDispatcher, cfg); err != nil {
		return err
	}
	if err := registerRuntimeModules(context.Background(), kernelRuntime, cfg); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := kernelRuntime.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("run kernel: %w", err)
	}

	return nil
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
	if envPath := strings.TrimSpace(os.Getenv(envLLMConfigFile)); envPath != "" {
		cfg.llmConfigFile = envPath
	}
	if cfg.llmConfigFile != "" {
		llmConfig, err := llmchat.LoadConfigFile(cfg.llmConfigFile)
		if err != nil {
			return appConfig{}, fmt.Errorf("load llm config file %s: %w", cfg.llmConfigFile, err)
		}
		cfg.llmConfig = &llmConfig
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

		moduleHookTimeout:   defaultModuleHookTimeout,
		shutdownTimeout:     defaultShutdownTimeout,
		subscriptionBuffer:  defaultSubscriptionBuffer,
		subscriptionWorkers: defaultSubscriptionWorker,

		drivers:      make([]driver.Definition, 0),
		moduleRoutes: make(map[string]kernel.ModuleRoute),
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

	if rawTimeout := strings.TrimSpace(parsed.Kernel.ModuleHookTimeout); rawTimeout != "" {
		timeout, err := time.ParseDuration(rawTimeout)
		if err != nil {
			return fmt.Errorf("parse kernel.module_hook_timeout: %w", err)
		}
		if timeout <= 0 {
			return fmt.Errorf("parse kernel.module_hook_timeout: must be > 0")
		}
		cfg.moduleHookTimeout = timeout
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
	cfg.llmConfigFile = strings.TrimSpace(parsed.LLM.ConfigFile)

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

	moduleNames := configuredRuntimeModuleNames(cfg)
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

func configuredRuntimeModuleNames(cfg *appConfig) []string {
	moduleNames := append([]string(nil), baseRuntimeModuleNames...)
	if cfg != nil && cfg.llmConfig != nil {
		moduleNames = append(moduleNames, "llmchat")
	}

	return moduleNames
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

func buildKernelRuntime(logger *slog.Logger, cfg appConfig) *kernel.Kernel {
	return kernel.New(
		kernel.WithLogger(logger),
		kernel.WithModuleHookTimeout(cfg.moduleHookTimeout),
		kernel.WithShutdownTimeout(cfg.shutdownTimeout),
		kernel.WithDefaultSubscriptionBuffer(cfg.subscriptionBuffer),
		kernel.WithDefaultSubscriptionWorkers(cfg.subscriptionWorkers),
		kernel.WithModuleRouting(cfg.routingDefault, cfg.moduleRoutes),
	)
}

func buildDriverRuntime(
	ctx context.Context,
	logger *slog.Logger,
	cfg appConfig,
	registry *driver.Registry,
) ([]otogi.Driver, otogi.SinkDispatcher, error) {
	if registry == nil {
		return nil, nil, fmt.Errorf("build drivers: nil driver registry")
	}

	runtimes, err := registry.BuildEnabled(ctx, cfg.drivers, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("build drivers: %w", err)
	}

	drivers := make([]otogi.Driver, 0, len(runtimes))
	for _, runtime := range runtimes {
		drivers = append(drivers, runtime.Driver)
	}

	dispatcher, err := driver.NewSinkDispatcher(runtimes)
	if err != nil {
		return nil, nil, fmt.Errorf("build sink dispatcher: %w", err)
	}

	return drivers, dispatcher, nil
}

func registerRuntimeServices(
	kernelRuntime *kernel.Kernel,
	logger *slog.Logger,
	sinkDispatcher otogi.SinkDispatcher,
	cfg appConfig,
) error {
	if err := kernelRuntime.RegisterService(memory.ServiceLogger, logger); err != nil {
		return fmt.Errorf("register logger service: %w", err)
	}
	if sinkDispatcher == nil {
		return fmt.Errorf("register sink dispatcher service: nil dispatcher")
	}
	if err := kernelRuntime.RegisterService(otogi.ServiceSinkDispatcher, sinkDispatcher); err != nil {
		return fmt.Errorf("register sink dispatcher service: %w", err)
	}
	if cfg.llmConfig != nil {
		providers, err := buildLLMProviders(*cfg.llmConfig)
		if err != nil {
			return fmt.Errorf("build llm providers: %w", err)
		}

		registry, err := llm.NewRegistry(providers)
		if err != nil {
			return fmt.Errorf("new llm provider registry: %w", err)
		}
		if err := kernelRuntime.RegisterService(otogi.ServiceLLMProviderRegistry, registry); err != nil {
			return fmt.Errorf("register llm provider registry service: %w", err)
		}
	}

	return nil
}

func buildLLMProviders(cfg llmchat.Config) (map[string]otogi.LLMProvider, error) {
	providers := make(map[string]otogi.LLMProvider, len(cfg.Providers))
	for profileKey, profile := range cfg.Providers {
		providerType := strings.ToLower(strings.TrimSpace(profile.Type))
		switch providerType {
		case "openai":
			openAICfg := openaillm.ProviderConfig{
				APIKey:       profile.APIKey,
				BaseURL:      profile.BaseURL,
				Organization: profile.Organization,
				Project:      profile.Project,
				MaxRetries:   cloneOptionalInt(profile.MaxRetries),
			}
			if profile.Timeout != nil {
				openAICfg.Timeout = *profile.Timeout
			}

			provider, err := openaillm.New(openAICfg)
			if err != nil {
				return nil, fmt.Errorf("provider profile %s: %w", profileKey, err)
			}
			providers[profileKey] = provider
		default:
			return nil, fmt.Errorf("provider profile %s: unsupported type %q", profileKey, profile.Type)
		}
	}

	return providers, nil
}

func cloneOptionalInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func registerRuntimeModules(ctx context.Context, kernelRuntime *kernel.Kernel, cfg appConfig) error {
	memoryModule := memory.New()
	if err := kernelRuntime.RegisterModule(ctx, memoryModule); err != nil {
		return fmt.Errorf("register memory module: %w", err)
	}
	pingPongModule := pingpong.New()
	if err := kernelRuntime.RegisterModule(ctx, pingPongModule); err != nil {
		return fmt.Errorf("register pingpong module: %w", err)
	}
	helpModule := help.New()
	if err := kernelRuntime.RegisterModule(ctx, helpModule); err != nil {
		return fmt.Errorf("register help module: %w", err)
	}
	if cfg.llmConfig != nil {
		llmModule, err := llmchat.New(*cfg.llmConfig)
		if err != nil {
			return fmt.Errorf("new llmchat module: %w", err)
		}
		if err := kernelRuntime.RegisterModule(ctx, llmModule); err != nil {
			return fmt.Errorf("register llmchat module: %w", err)
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
