package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"

	"github.com/bwmarrin/discordgo"
)

const (
	defaultRuntimePublishTimeout = 2 * time.Second
	defaultRuntimeUpdateBuffer   = 1024
	discordAPIVersion            = "10"

	// discordGatewayIntents combines required standard and privileged gateway intents.
	// IntentsMessageContent and IntentsGuildMembers are privileged and must be enabled
	// in the Discord Developer Portal for the bot application.
	discordGatewayIntents = discordgo.IntentsGuilds |
		discordgo.IntentsGuildMessages |
		discordgo.IntentsGuildMessageReactions |
		discordgo.IntentsDirectMessages |
		discordgo.IntentsMessageContent |
		discordgo.IntentsGuildMembers
)

// runtimeConfig is the JSON-serialisable Discord driver config payload.
type runtimeConfig struct {
	// BotToken is the raw Discord bot token (without the "Bot " prefix).
	BotToken string `json:"bot_token"`
	// PublishTimeout is an optional Go duration string (e.g. "5s") for outbound calls.
	PublishTimeout string `json:"publish_timeout"`
	// UpdateBuffer is the size of the internal gateway event channel.
	UpdateBuffer int `json:"update_buffer"`
	// DownloadTimeout is an optional Go duration string for media download requests.
	DownloadTimeout string `json:"download_timeout"`
}

type parsedRuntimeConfig struct {
	botToken        string
	publishTimeout  time.Duration
	updateBuffer    int
	downloadTimeout time.Duration
}

// BuildRuntimeFromConfig builds one Discord driver runtime from a JSON config payload.
//
// Required config field: bot_token (raw token, "Bot " prefix is added automatically).
// Optional config fields: publish_timeout (default 2s), update_buffer (default 1024),
// download_timeout (default 30s).
//
// IntentsMessageContent and IntentsGuildMembers are privileged gateway intents and must
// be enabled in the Discord Developer Portal before the bot can receive those events.
func BuildRuntimeFromConfig(
	name string,
	logger *slog.Logger,
	rawConfig []byte,
) (platform.EventSource, core.Driver, *MediaDownloader, *SinkDispatcher, *ModerationDispatcher, error) {
	cfg, err := parseRuntimeConfig(rawConfig)
	if err != nil {
		return platform.EventSource{}, nil, nil, nil, nil, fmt.Errorf("parse discord runtime config: %w", err)
	}
	if logger == nil {
		logger = slog.Default()
	}

	// Pin the Discord REST API version for the discordgo HTTP client.
	discordgo.APIVersion = discordAPIVersion

	session, err := discordgo.New("Bot " + cfg.botToken)
	if err != nil {
		return platform.EventSource{}, nil, nil, nil, nil, fmt.Errorf("new discordgo session: %w", err)
	}
	session.Identify.Intents = discordGatewayIntents

	mapper := NewDefaultDiscordgoMapper()
	source, err := NewDiscordgoSource(session, mapper, cfg.updateBuffer)
	if err != nil {
		return platform.EventSource{}, nil, nil, nil, nil, fmt.Errorf("new discord gateway source: %w", err)
	}

	driver, err := NewDriver(
		source,
		NewDefaultDecoder(),
		WithName(name),
		WithPublishTimeout(cfg.publishTimeout),
		WithErrorHandler(func(_ context.Context, driverErr error) {
			logger.Error("discord driver async error", "error", driverErr)
		}),
	)
	if err != nil {
		return platform.EventSource{}, nil, nil, nil, nil, fmt.Errorf("new discord driver: %w", err)
	}

	sinkRef := platform.EventSink{Platform: DriverPlatform, ID: name}

	sink, err := NewSinkDispatcher(session,
		WithOutboundTimeout(cfg.publishTimeout),
		WithOutboundSinkRef(sinkRef),
	)
	if err != nil {
		return platform.EventSource{}, nil, nil, nil, nil, fmt.Errorf("new discord sink dispatcher: %w", err)
	}

	moderation, err := NewModerationDispatcher(session,
		WithOutboundTimeout(cfg.publishTimeout),
		WithOutboundSinkRef(sinkRef),
	)
	if err != nil {
		return platform.EventSource{}, nil, nil, nil, nil, fmt.Errorf("new discord moderation dispatcher: %w", err)
	}

	mediaDownloader := NewMediaDownloader(
		WithMediaDownloadTimeout(cfg.downloadTimeout),
	)

	return platform.EventSource{
		Platform: DriverPlatform,
		ID:       name,
	}, driver, mediaDownloader, sink, moderation, nil
}

// parseRuntimeConfig unmarshals and validates the raw Discord runtime config payload.
func parseRuntimeConfig(raw []byte) (parsedRuntimeConfig, error) {
	if len(raw) == 0 {
		return parsedRuntimeConfig{}, fmt.Errorf("missing config")
	}

	var parsed runtimeConfig
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return parsedRuntimeConfig{}, fmt.Errorf("unmarshal: %w", err)
	}

	cfg := parsedRuntimeConfig{
		botToken:        strings.TrimSpace(parsed.BotToken),
		publishTimeout:  defaultRuntimePublishTimeout,
		updateBuffer:    parsed.UpdateBuffer,
		downloadTimeout: defaultDiscordMediaDownloadTimeout,
	}

	if cfg.botToken == "" {
		return parsedRuntimeConfig{}, fmt.Errorf("bot_token is required")
	}
	if cfg.updateBuffer <= 0 {
		cfg.updateBuffer = defaultRuntimeUpdateBuffer
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

	if timeout := strings.TrimSpace(parsed.DownloadTimeout); timeout != "" {
		parsedTimeout, err := time.ParseDuration(timeout)
		if err != nil {
			return parsedRuntimeConfig{}, fmt.Errorf("parse download_timeout: %w", err)
		}
		if parsedTimeout <= 0 {
			return parsedRuntimeConfig{}, fmt.Errorf("parse download_timeout: must be > 0")
		}
		cfg.downloadTimeout = parsedTimeout
	}

	return cfg, nil
}
