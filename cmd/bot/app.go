package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"ex-otogi/internal/driver/telegram"
	"ex-otogi/internal/kernel"
	"ex-otogi/modules/demo"

	"github.com/gotd/td/session"
	gotdtelegram "github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
)

const (
	envConfigFile               = "OTOGI_CONFIG_FILE"
	envLogLevel                 = "OTOGI_LOG_LEVEL"
	envTelegramPublishTimeout   = "OTOGI_TELEGRAM_PUBLISH_TIMEOUT"
	envTelegramUpdateBuffer     = "OTOGI_TELEGRAM_UPDATE_BUFFER"
	envTelegramAuthTimeout      = "OTOGI_TELEGRAM_AUTH_TIMEOUT"
	envTelegramCode             = "OTOGI_TELEGRAM_CODE"
	envTelegramBotToken         = "OTOGI_TELEGRAM_BOT_TOKEN"
	envTelegramPhone            = "OTOGI_TELEGRAM_PHONE"
	envTelegramPassword         = "OTOGI_TELEGRAM_PASSWORD"
	envTelegramSessionFile      = "OTOGI_TELEGRAM_SESSION_FILE"
	defaultConfigFilePath       = "config/bot.json"
	defaultTelegramSessionFile  = ".cache/telegram/session.json"
	defaultModuleHookTimeout    = 3 * time.Second
	defaultShutdownTimeout      = 10 * time.Second
	defaultSubscriptionBuffer   = 256
	defaultSubscriptionWorkers  = 2
	defaultTelegramPublishDelay = 2 * time.Second
	defaultTelegramAuthTimeout  = 3 * time.Minute
)

type appConfig struct {
	logLevel slog.Level

	moduleHookTimeout   time.Duration
	shutdownTimeout     time.Duration
	subscriptionBuffer  int
	subscriptionWorkers int

	telegramPublishTimeout time.Duration
	telegramUpdateBuffer   int
	telegramAuthTimeout    time.Duration
	telegramCodeEnv        string
	telegramBotTokenEnv    string
	telegramPhone          string
	telegramPassword       string
	telegramSessionFile    string
}

type fileConfig struct {
	LogLevel string             `json:"log_level"`
	Kernel   fileKernelConfig   `json:"kernel"`
	Telegram fileTelegramConfig `json:"telegram"`
}

type fileKernelConfig struct {
	ModuleHookTimeout   string `json:"module_hook_timeout"`
	ShutdownTimeout     string `json:"shutdown_timeout"`
	SubscriptionBuffer  *int   `json:"subscription_buffer"`
	SubscriptionWorkers *int   `json:"subscription_workers"`
}

type fileTelegramConfig struct {
	PublishTimeout string `json:"publish_timeout"`
	UpdateBuffer   *int   `json:"update_buffer"`
	AuthTimeout    string `json:"auth_timeout"`
	CodeEnv        string `json:"code_env"`
	BotTokenEnv    string `json:"bot_token_env"`
	Phone          string `json:"phone"`
	Password       string `json:"password"`
	SessionFile    string `json:"session_file"`
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.logLevel}))

	kernelRuntime := buildKernelRuntime(logger, cfg)
	if err := registerRuntimeServices(kernelRuntime, logger); err != nil {
		return err
	}
	if err := registerRuntimeModules(context.Background(), kernelRuntime); err != nil {
		return err
	}
	if err := registerRuntimeDrivers(kernelRuntime, logger, cfg); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := kernelRuntime.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("run kernel: %w", err)
	}

	return nil
}

func loadConfig() (appConfig, error) {
	cfg := defaultAppConfig()
	configFile := strings.TrimSpace(os.Getenv(envConfigFile))
	if configFile == "" {
		configFile = defaultConfigFilePath
	}

	if err := applyConfigFile(&cfg, configFile, os.Getenv(envConfigFile) != ""); err != nil {
		return appConfig{}, err
	}
	if err := applyEnvOverrides(&cfg); err != nil {
		return appConfig{}, err
	}

	return cfg, nil
}

func loadConfigFromEnv() (appConfig, error) {
	cfg := defaultAppConfig()
	if err := applyEnvOverrides(&cfg); err != nil {
		return appConfig{}, err
	}

	return cfg, nil
}

func defaultAppConfig() appConfig {
	return appConfig{
		logLevel: slog.LevelInfo,

		moduleHookTimeout:   defaultModuleHookTimeout,
		shutdownTimeout:     defaultShutdownTimeout,
		subscriptionBuffer:  defaultSubscriptionBuffer,
		subscriptionWorkers: defaultSubscriptionWorkers,

		telegramPublishTimeout: defaultTelegramPublishDelay,
		telegramUpdateBuffer:   defaultSubscriptionBuffer,
		telegramAuthTimeout:    defaultTelegramAuthTimeout,
		telegramCodeEnv:        envTelegramCode,
		telegramBotTokenEnv:    envTelegramBotToken,
		telegramSessionFile:    defaultTelegramSessionFile,
	}
}

func applyEnvOverrides(cfg *appConfig) error {
	if cfg == nil {
		return fmt.Errorf("apply env overrides: nil config")
	}

	if rawLevel := strings.TrimSpace(os.Getenv(envLogLevel)); rawLevel != "" {
		level, err := parseLogLevel(rawLevel)
		if err != nil {
			return fmt.Errorf("parse %s: %w", envLogLevel, err)
		}
		cfg.logLevel = level
	}

	if rawTimeout := strings.TrimSpace(os.Getenv(envTelegramPublishTimeout)); rawTimeout != "" {
		timeout, err := time.ParseDuration(rawTimeout)
		if err != nil {
			return fmt.Errorf("parse %s: %w", envTelegramPublishTimeout, err)
		}
		if timeout <= 0 {
			return fmt.Errorf("parse %s: must be > 0", envTelegramPublishTimeout)
		}
		cfg.telegramPublishTimeout = timeout
	}

	if rawTimeout := strings.TrimSpace(os.Getenv(envTelegramAuthTimeout)); rawTimeout != "" {
		timeout, err := time.ParseDuration(rawTimeout)
		if err != nil {
			return fmt.Errorf("parse %s: %w", envTelegramAuthTimeout, err)
		}
		if timeout <= 0 {
			return fmt.Errorf("parse %s: must be > 0", envTelegramAuthTimeout)
		}
		cfg.telegramAuthTimeout = timeout
	}

	if rawBuffer := strings.TrimSpace(os.Getenv(envTelegramUpdateBuffer)); rawBuffer != "" {
		buffer, err := strconv.Atoi(rawBuffer)
		if err != nil {
			return fmt.Errorf("parse %s: %w", envTelegramUpdateBuffer, err)
		}
		if buffer <= 0 {
			return fmt.Errorf("parse %s: must be > 0", envTelegramUpdateBuffer)
		}
		cfg.telegramUpdateBuffer = buffer
	}

	if rawPhone := strings.TrimSpace(os.Getenv(envTelegramPhone)); rawPhone != "" {
		cfg.telegramPhone = rawPhone
	}
	if rawPassword := strings.TrimSpace(os.Getenv(envTelegramPassword)); rawPassword != "" {
		cfg.telegramPassword = rawPassword
	}
	if rawSessionFile := strings.TrimSpace(os.Getenv(envTelegramSessionFile)); rawSessionFile != "" {
		cfg.telegramSessionFile = rawSessionFile
	}

	return nil
}

func applyConfigFile(cfg *appConfig, path string, required bool) error {
	if cfg == nil {
		return fmt.Errorf("apply config file: nil config")
	}
	if strings.TrimSpace(path) == "" {
		if required {
			return fmt.Errorf("config file path is required")
		}
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !required {
			return nil
		}
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

	if rawTimeout := strings.TrimSpace(parsed.Telegram.PublishTimeout); rawTimeout != "" {
		timeout, err := time.ParseDuration(rawTimeout)
		if err != nil {
			return fmt.Errorf("parse telegram.publish_timeout: %w", err)
		}
		if timeout <= 0 {
			return fmt.Errorf("parse telegram.publish_timeout: must be > 0")
		}
		cfg.telegramPublishTimeout = timeout
	}

	if parsed.Telegram.UpdateBuffer != nil {
		if *parsed.Telegram.UpdateBuffer <= 0 {
			return fmt.Errorf("parse telegram.update_buffer: must be > 0")
		}
		cfg.telegramUpdateBuffer = *parsed.Telegram.UpdateBuffer
	}

	if rawTimeout := strings.TrimSpace(parsed.Telegram.AuthTimeout); rawTimeout != "" {
		timeout, err := time.ParseDuration(rawTimeout)
		if err != nil {
			return fmt.Errorf("parse telegram.auth_timeout: %w", err)
		}
		if timeout <= 0 {
			return fmt.Errorf("parse telegram.auth_timeout: must be > 0")
		}
		cfg.telegramAuthTimeout = timeout
	}

	if codeEnv := strings.TrimSpace(parsed.Telegram.CodeEnv); codeEnv != "" {
		cfg.telegramCodeEnv = codeEnv
	}
	if botTokenEnv := strings.TrimSpace(parsed.Telegram.BotTokenEnv); botTokenEnv != "" {
		cfg.telegramBotTokenEnv = botTokenEnv
	}
	if phone := strings.TrimSpace(parsed.Telegram.Phone); phone != "" {
		cfg.telegramPhone = phone
	}
	if password := strings.TrimSpace(parsed.Telegram.Password); password != "" {
		cfg.telegramPassword = password
	}
	if sessionFile := strings.TrimSpace(parsed.Telegram.SessionFile); sessionFile != "" {
		cfg.telegramSessionFile = sessionFile
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
	)
}

func registerRuntimeServices(kernelRuntime *kernel.Kernel, logger *slog.Logger) error {
	if err := kernelRuntime.RegisterService(demo.ServiceLogger, logger); err != nil {
		return fmt.Errorf("register logger service: %w", err)
	}

	return nil
}

func registerRuntimeModules(ctx context.Context, kernelRuntime *kernel.Kernel) error {
	demoModule := demo.New()
	if err := kernelRuntime.RegisterModule(ctx, demoModule); err != nil {
		return fmt.Errorf("register demo module: %w", err)
	}

	return nil
}

func registerRuntimeDrivers(kernelRuntime *kernel.Kernel, logger *slog.Logger, cfg appConfig) error {
	telegramDriver, err := buildTelegramDriver(logger, cfg)
	if err != nil {
		return fmt.Errorf("build telegram driver: %w", err)
	}
	if err := kernelRuntime.RegisterDriver(telegramDriver); err != nil {
		return fmt.Errorf("register telegram driver: %w", err)
	}

	return nil
}

func buildTelegramDriver(logger *slog.Logger, cfg appConfig) (*telegram.Driver, error) {
	source, err := buildGotdSource(logger, cfg)
	if err != nil {
		return nil, err
	}

	driver, err := telegram.NewDriver(
		source,
		telegram.NewDefaultDecoder(),
		telegram.WithPublishTimeout(cfg.telegramPublishTimeout),
		telegram.WithErrorHandler(func(_ context.Context, err error) {
			logger.Error("telegram driver async error", "error", err)
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("new telegram driver: %w", err)
	}

	return driver, nil
}

func buildGotdSource(logger *slog.Logger, cfg appConfig) (*telegram.GotdUserbotSource, error) {
	updateChannel, err := telegram.NewGotdUpdateChannel(cfg.telegramUpdateBuffer)
	if err != nil {
		return nil, fmt.Errorf("new gotd update channel: %w", err)
	}

	sessionStorage, err := newGotdSessionStorage(cfg.telegramSessionFile)
	if err != nil {
		return nil, fmt.Errorf("new gotd session storage: %w", err)
	}

	client, err := gotdtelegram.ClientFromEnvironment(gotdtelegram.Options{
		UpdateHandler:  updateChannel,
		SessionStorage: sessionStorage,
	})
	if err != nil {
		return nil, fmt.Errorf("new gotd client from environment: %w", err)
	}

	source, err := telegram.NewGotdUserbotSource(
		gotdAuthenticatedClient{
			client: client,
			authenticate: func(ctx context.Context) error {
				return authenticateGotdClient(ctx, logger, client, cfg)
			},
		},
		updateChannel,
		telegram.NewDefaultGotdUpdateMapper(),
	)
	if err != nil {
		return nil, fmt.Errorf("new gotd userbot source: %w", err)
	}

	return source, nil
}

func newGotdSessionStorage(path string) (*session.FileStorage, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return nil, fmt.Errorf("empty session file path")
	}

	absPath, err := filepath.Abs(trimmedPath)
	if err != nil {
		return nil, fmt.Errorf("resolve absolute session file path: %w", err)
	}
	sessionDir := filepath.Dir(absPath)
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		return nil, fmt.Errorf("create session directory %s: %w", sessionDir, err)
	}

	return &session.FileStorage{Path: absPath}, nil
}

type gotdAuthenticatedClient struct {
	client       *gotdtelegram.Client
	authenticate func(ctx context.Context) error
}

func (c gotdAuthenticatedClient) Run(ctx context.Context, fn func(runCtx context.Context) error) error {
	if c.client == nil {
		return fmt.Errorf("run gotd authenticated client: nil client")
	}
	if c.authenticate == nil {
		return fmt.Errorf("run gotd authenticated client: nil authenticate callback")
	}
	if fn == nil {
		return fmt.Errorf("run gotd authenticated client: nil run callback")
	}

	if err := c.client.Run(ctx, func(runCtx context.Context) error {
		if err := c.authenticate(runCtx); err != nil {
			return fmt.Errorf("authenticate gotd client: %w", err)
		}
		if err := fn(runCtx); err != nil {
			return fmt.Errorf("run gotd client callback: %w", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("run gotd authenticated client: %w", err)
	}

	return nil
}

func authenticateGotdClient(
	ctx context.Context,
	logger *slog.Logger,
	client *gotdtelegram.Client,
	cfg appConfig,
) error {
	if client == nil {
		return fmt.Errorf("authenticate gotd client: nil client")
	}

	authCtx := ctx
	cancel := func() {}
	if cfg.telegramAuthTimeout > 0 {
		timeoutCtx, timeoutCancel := context.WithTimeout(ctx, cfg.telegramAuthTimeout)
		authCtx = timeoutCtx
		cancel = timeoutCancel
	}
	defer cancel()

	status, err := client.Auth().Status(authCtx)
	if err != nil {
		return fmt.Errorf("check auth status: %w", err)
	}
	if status.Authorized {
		logger.Info("telegram session restored from local storage", "session_file", cfg.telegramSessionFile)
		return nil
	}

	if botToken := strings.TrimSpace(os.Getenv(cfg.telegramBotTokenEnv)); botToken != "" {
		if _, err := client.Auth().Bot(authCtx, botToken); err != nil {
			return fmt.Errorf("authenticate bot: %w", err)
		}
		logger.Info("telegram authorized with bot token")
		return nil
	}

	phone := strings.TrimSpace(cfg.telegramPhone)
	if phone == "" {
		return fmt.Errorf("telegram phone number is required for user login; configure telegram.phone in %s or set %s", defaultConfigFilePath, envTelegramPhone)
	}

	codeAuthenticator := auth.CodeAuthenticatorFunc(func(ctx context.Context, _ *tg.AuthSentCode) (string, error) {
		code, err := telegramAuthCode(cfg.telegramCodeEnv)
		if err != nil {
			return "", fmt.Errorf("resolve login code: %w", err)
		}
		return code, nil
	})

	var authenticator auth.UserAuthenticator = auth.CodeOnly(phone, codeAuthenticator)
	if password := strings.TrimSpace(cfg.telegramPassword); password != "" {
		authenticator = auth.Constant(phone, password, codeAuthenticator)
	}

	flow := auth.NewFlow(authenticator, auth.SendCodeOptions{})

	if err := client.Auth().IfNecessary(authCtx, flow); err != nil {
		return fmt.Errorf("authenticate user: %w", err)
	}
	logger.Info("telegram authorized with user flow", "session_file", cfg.telegramSessionFile)

	return nil
}

func telegramAuthCode(codeEnv string) (string, error) {
	if code := strings.TrimSpace(os.Getenv(codeEnv)); code != "" {
		return code, nil
	}

	stdinInfo, err := os.Stdin.Stat()
	if err != nil {
		return "", fmt.Errorf("read stdin status: %w", err)
	}
	if stdinInfo.Mode()&os.ModeCharDevice == 0 {
		return "", fmt.Errorf(
			"%s is not set and stdin is not interactive; set %s to proceed",
			codeEnv,
			codeEnv,
		)
	}

	fmt.Fprint(os.Stdout, "Enter Telegram login code: ")
	code, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read login code: %w", err)
	}

	code = strings.TrimSpace(code)
	if code == "" {
		return "", fmt.Errorf("empty login code")
	}

	return code, nil
}
