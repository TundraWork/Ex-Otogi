package duel

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"ex-otogi/pkg/otogi/core"
)

const (
	defaultJoinTimeout       = 60 * time.Second
	defaultGameTimeout       = 60 * time.Second
	defaultLoserMuteDuration = 5 * time.Minute
	defaultMaxPlayers        = 4
	defaultMaxPlayersLimit   = 8
)

type fileConfig struct {
	JoinTimeout       string `json:"join_timeout"`
	GameTimeout       string `json:"game_timeout"`
	LoserMuteDuration string `json:"loser_mute_duration"`
	DefaultMaxPlayers int    `json:"default_max_players"`
	MaxPlayersLimit   int    `json:"max_players_limit"`
}

type config struct {
	JoinTimeout       time.Duration
	GameTimeout       time.Duration
	LoserMuteDuration time.Duration
	DefaultMaxPlayers int
	MaxPlayersLimit   int
}

func defaultConfig() config {
	return config{
		JoinTimeout:       defaultJoinTimeout,
		GameTimeout:       defaultGameTimeout,
		LoserMuteDuration: defaultLoserMuteDuration,
		DefaultMaxPlayers: defaultMaxPlayers,
		MaxPlayersLimit:   defaultMaxPlayersLimit,
	}
}

func loadConfig(registry core.ConfigRegistry) (config, error) {
	cfg := defaultConfig()
	if registry == nil {
		return cfg, nil
	}

	raw, err := registry.Resolve(moduleName)
	switch {
	case err == nil:
	case errors.Is(err, core.ErrConfigNotFound):
		return cfg, nil
	default:
		return config{}, fmt.Errorf("resolve config: %w", err)
	}

	var parsed fileConfig
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return config{}, fmt.Errorf("unmarshal config: %w", err)
	}

	if trimmed := strings.TrimSpace(parsed.JoinTimeout); trimmed != "" {
		duration, err := time.ParseDuration(trimmed)
		if err != nil {
			return config{}, fmt.Errorf("parse join_timeout: %w", err)
		}
		cfg.JoinTimeout = duration
	}
	if trimmed := strings.TrimSpace(parsed.GameTimeout); trimmed != "" {
		duration, err := time.ParseDuration(trimmed)
		if err != nil {
			return config{}, fmt.Errorf("parse game_timeout: %w", err)
		}
		cfg.GameTimeout = duration
	}
	if trimmed := strings.TrimSpace(parsed.LoserMuteDuration); trimmed != "" {
		duration, err := time.ParseDuration(trimmed)
		if err != nil {
			return config{}, fmt.Errorf("parse loser_mute_duration: %w", err)
		}
		cfg.LoserMuteDuration = duration
	}
	if parsed.DefaultMaxPlayers > 0 {
		cfg.DefaultMaxPlayers = parsed.DefaultMaxPlayers
	}
	if parsed.MaxPlayersLimit > 0 {
		cfg.MaxPlayersLimit = parsed.MaxPlayersLimit
	}

	if err := cfg.Validate(); err != nil {
		return config{}, err
	}

	return cfg, nil
}

func (c config) Validate() error {
	if c.JoinTimeout <= 0 {
		return fmt.Errorf("validate config: join_timeout must be > 0")
	}
	if c.GameTimeout <= 0 {
		return fmt.Errorf("validate config: game_timeout must be > 0")
	}
	if c.LoserMuteDuration < 0 {
		return fmt.Errorf("validate config: loser_mute_duration must be >= 0")
	}
	if c.MaxPlayersLimit < 2 {
		return fmt.Errorf("validate config: max_players_limit must be >= 2")
	}
	if c.DefaultMaxPlayers < 2 {
		return fmt.Errorf("validate config: default_max_players must be >= 2")
	}
	if c.DefaultMaxPlayers > c.MaxPlayersLimit {
		return fmt.Errorf("validate config: default_max_players exceeds max_players_limit")
	}

	return nil
}
