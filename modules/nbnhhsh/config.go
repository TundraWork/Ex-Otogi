package nbnhhsh

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"ex-otogi/pkg/otogi"
)

const (
	defaultAPIURL            = "https://lab.magiconch.com/api/nbnhhsh/guess"
	defaultRequestTimeout    = 2500 * time.Millisecond
	subscriptionTimeoutGrace = 500 * time.Millisecond
	moduleName               = "nbnhhsh"
)

type fileConfig struct {
	APIURL         string `json:"api_url"`
	RequestTimeout string `json:"request_timeout"`
}

type config struct {
	APIURL         string
	RequestTimeout time.Duration
}

func defaultConfig() config {
	return config{
		APIURL:         defaultAPIURL,
		RequestTimeout: defaultRequestTimeout,
	}
}

func loadConfig(registry otogi.ConfigRegistry) (config, error) {
	cfg := defaultConfig()
	if registry == nil {
		return cfg, nil
	}

	raw, err := registry.Resolve(moduleName)
	switch {
	case err == nil:
	case errors.Is(err, otogi.ErrConfigNotFound):
		return cfg, nil
	default:
		return config{}, fmt.Errorf("resolve config: %w", err)
	}

	var parsed fileConfig
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return config{}, fmt.Errorf("unmarshal config: %w", err)
	}

	if trimmed := strings.TrimSpace(parsed.APIURL); trimmed != "" {
		cfg.APIURL = trimmed
	}
	if trimmed := strings.TrimSpace(parsed.RequestTimeout); trimmed != "" {
		timeout, err := time.ParseDuration(trimmed)
		if err != nil {
			return config{}, fmt.Errorf("parse request_timeout: %w", err)
		}
		cfg.RequestTimeout = timeout
	}

	if err := cfg.Validate(); err != nil {
		return config{}, err
	}

	return cfg, nil
}

func (c config) Validate() error {
	if c.RequestTimeout <= 0 {
		return fmt.Errorf("validate config: request_timeout must be > 0")
	}

	parsedURL, err := url.Parse(strings.TrimSpace(c.APIURL))
	if err != nil {
		return fmt.Errorf("validate config: parse api_url: %w", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("validate config: api_url scheme must be http or https")
	}
	if strings.TrimSpace(parsedURL.Host) == "" {
		return fmt.Errorf("validate config: api_url host is required")
	}

	return nil
}
