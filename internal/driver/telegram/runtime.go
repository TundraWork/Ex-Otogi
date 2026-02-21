package telegram

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ex-otogi/pkg/otogi"

	"github.com/gotd/td/session"
	gotdtelegram "github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
)

const (
	defaultRuntimeSessionFile  = ".cache/telegram/session.json"
	defaultRuntimePublishDelay = 2 * time.Second
	defaultRuntimeAuthTimeout  = 3 * time.Minute
)

type runtimeConfig struct {
	AppID          int    `json:"app_id"`
	AppHash        string `json:"app_hash"`
	PublishTimeout string `json:"publish_timeout"`
	UpdateBuffer   int    `json:"update_buffer"`
	AuthTimeout    string `json:"auth_timeout"`
	Code           string `json:"code"`
	Phone          string `json:"phone"`
	Password       string `json:"password"`
	SessionFile    string `json:"session_file"`
}

type parsedRuntimeConfig struct {
	appID          int
	appHash        string
	publishTimeout time.Duration
	updateBuffer   int
	authTimeout    time.Duration
	code           string
	phone          string
	password       string
	sessionFile    string
}

// BuildRuntimeFromConfig builds one telegram driver runtime from config payload.
func BuildRuntimeFromConfig(
	name string,
	logger *slog.Logger,
	rawConfig []byte,
) (otogi.EventSource, otogi.Driver, otogi.SinkDispatcher, error) {
	cfg, err := parseRuntimeConfig(rawConfig)
	if err != nil {
		return otogi.EventSource{}, nil, nil, fmt.Errorf("parse telegram runtime config: %w", err)
	}
	if logger == nil {
		logger = slog.Default()
	}

	updateChannel, err := NewGotdUpdateChannel(cfg.updateBuffer)
	if err != nil {
		return otogi.EventSource{}, nil, nil, fmt.Errorf("new gotd update channel: %w", err)
	}

	sessionStorage, err := newGotdSessionStorage(cfg.sessionFile)
	if err != nil {
		return otogi.EventSource{}, nil, nil, fmt.Errorf("new gotd session storage: %w", err)
	}

	client := gotdtelegram.NewClient(cfg.appID, cfg.appHash, gotdtelegram.Options{
		UpdateHandler:  updateChannel,
		SessionStorage: sessionStorage,
	})

	peers := NewPeerCache()
	reactionResolver, err := NewGotdMessageReactionResolver(client.API())
	if err != nil {
		return otogi.EventSource{}, nil, nil, fmt.Errorf("new gotd message reaction resolver: %w", err)
	}
	source, err := NewGotdUserbotSource(
		gotdAuthenticatedClient{
			client: client,
			authenticate: func(ctx context.Context) error {
				return authenticateGotdClient(ctx, logger, client, cfg)
			},
		},
		updateChannel,
		NewDefaultGotdUpdateMapper(
			WithPeerCache(peers),
			WithMessageReactionResolver(reactionResolver),
			WithMapperLogger(logger),
		),
	)
	if err != nil {
		return otogi.EventSource{}, nil, nil, fmt.Errorf("new gotd userbot source: %w", err)
	}

	driver, err := NewDriver(
		source,
		NewDefaultDecoder(),
		WithName(name),
		WithPublishTimeout(cfg.publishTimeout),
		WithErrorHandler(func(_ context.Context, err error) {
			logger.Error("telegram driver async error", "error", err)
		}),
	)
	if err != nil {
		return otogi.EventSource{}, nil, nil, fmt.Errorf("new telegram driver: %w", err)
	}

	sink, err := NewOutboundDispatcher(
		client,
		peers,
		WithOutboundTimeout(cfg.publishTimeout),
		WithOutboundLogger(logger),
		WithSinkRef(otogi.EventSink{
			Platform: DriverPlatform,
			ID:       name,
		}),
	)
	if err != nil {
		return otogi.EventSource{}, nil, nil, fmt.Errorf("new telegram sink dispatcher: %w", err)
	}

	return otogi.EventSource{
		Platform: DriverPlatform,
		ID:       name,
	}, driver, sink, nil
}

func parseRuntimeConfig(raw []byte) (parsedRuntimeConfig, error) {
	if len(raw) == 0 {
		return parsedRuntimeConfig{}, fmt.Errorf("missing config")
	}

	var parsed runtimeConfig
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return parsedRuntimeConfig{}, fmt.Errorf("unmarshal: %w", err)
	}

	cfg := parsedRuntimeConfig{
		appID:          parsed.AppID,
		appHash:        strings.TrimSpace(parsed.AppHash),
		publishTimeout: defaultRuntimePublishDelay,
		updateBuffer:   parsed.UpdateBuffer,
		authTimeout:    defaultRuntimeAuthTimeout,
		code:           strings.TrimSpace(parsed.Code),
		phone:          strings.TrimSpace(parsed.Phone),
		password:       strings.TrimSpace(parsed.Password),
		sessionFile:    strings.TrimSpace(parsed.SessionFile),
	}

	if cfg.updateBuffer <= 0 {
		cfg.updateBuffer = 256
	}
	if cfg.sessionFile == "" {
		cfg.sessionFile = defaultRuntimeSessionFile
	}

	if timeout := strings.TrimSpace(parsed.PublishTimeout); timeout != "" {
		parsedTimeout, err := time.ParseDuration(timeout)
		if err != nil {
			return parsedRuntimeConfig{}, fmt.Errorf("parse publish_timeout: %w", err)
		}
		if parsedTimeout <= 0 {
			return parsedRuntimeConfig{}, fmt.Errorf("parse publish_timeout: must be > 0")
		}
		cfg.publishTimeout = parsedTimeout
	}
	if timeout := strings.TrimSpace(parsed.AuthTimeout); timeout != "" {
		parsedTimeout, err := time.ParseDuration(timeout)
		if err != nil {
			return parsedRuntimeConfig{}, fmt.Errorf("parse auth_timeout: %w", err)
		}
		if parsedTimeout <= 0 {
			return parsedRuntimeConfig{}, fmt.Errorf("parse auth_timeout: must be > 0")
		}
		cfg.authTimeout = parsedTimeout
	}

	if cfg.appID <= 0 {
		return parsedRuntimeConfig{}, fmt.Errorf("app_id must be > 0")
	}
	if cfg.appHash == "" {
		return parsedRuntimeConfig{}, fmt.Errorf("app_hash is required")
	}

	return cfg, nil
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

// Run executes client runtime and performs authentication before invoking fn.
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
	cfg parsedRuntimeConfig,
) error {
	if client == nil {
		return fmt.Errorf("authenticate gotd client: nil client")
	}

	authCtx := ctx
	cancel := func() {}
	if cfg.authTimeout > 0 {
		timeoutCtx, timeoutCancel := context.WithTimeout(ctx, cfg.authTimeout)
		authCtx = timeoutCtx
		cancel = timeoutCancel
	}
	defer cancel()

	status, err := client.Auth().Status(authCtx)
	if err != nil {
		return fmt.Errorf("check auth status: %w", err)
	}
	if status.Authorized {
		logger.Info("telegram session restored from local storage", "session_file", cfg.sessionFile)
		return nil
	}

	phone := strings.TrimSpace(cfg.phone)
	if phone == "" {
		return fmt.Errorf("telegram phone number is required for userbot login; configure telegram.phone")
	}

	codeAuthenticator := auth.CodeAuthenticatorFunc(func(_ context.Context, _ *tg.AuthSentCode) (string, error) {
		code, err := telegramAuthCode(cfg.code)
		if err != nil {
			return "", fmt.Errorf("resolve login code: %w", err)
		}
		return code, nil
	})

	var authenticator auth.UserAuthenticator = auth.CodeOnly(phone, codeAuthenticator)
	if password := strings.TrimSpace(cfg.password); password != "" {
		authenticator = auth.Constant(phone, password, codeAuthenticator)
	}

	flow := auth.NewFlow(authenticator, auth.SendCodeOptions{})

	if err := client.Auth().IfNecessary(authCtx, flow); err != nil {
		return fmt.Errorf("authenticate user: %w", err)
	}
	logger.Info("telegram authorized with user flow", "session_file", cfg.sessionFile)

	return nil
}

func telegramAuthCode(configuredCode string) (string, error) {
	if code := strings.TrimSpace(configuredCode); code != "" {
		return code, nil
	}

	stdinInfo, err := os.Stdin.Stat()
	if err != nil {
		return "", fmt.Errorf("read stdin status: %w", err)
	}
	if stdinInfo.Mode()&os.ModeCharDevice == 0 {
		return "", fmt.Errorf("telegram.code is empty and stdin is not interactive")
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
