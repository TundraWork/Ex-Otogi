package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/TundraWork/Ex-Otogi/internal/kernel"
)

// Config is the otogi.yaml file. See docs/operations.md.
type Config struct {
	DataDir         string               `yaml:"data_dir"`
	ShutdownTimeout time.Duration        `yaml:"shutdown_timeout"`
	Log             LogConfig            `yaml:"log"`
	Kernel          KernelConfig         `yaml:"kernel"`
	Telegram        []TelegramAccount    `yaml:"telegram"`
	Modules         map[string]yaml.Node `yaml:"modules"`
}

// LogConfig selects process log verbosity and format.
type LogConfig struct {
	Level  string `yaml:"level"`  // debug | info | warn | error
	Format string `yaml:"format"` // json | text
}

// KernelConfig mirrors kernel.Config; zero values take the kernel defaults.
type KernelConfig struct {
	Workers        int           `yaml:"workers"`
	MaxInFlight    int           `yaml:"max_in_flight"`
	HandlerTimeout time.Duration `yaml:"handler_timeout"`
}

// TelegramAccount configures one Telegram user account.
type TelegramAccount struct {
	Account  string `yaml:"account"`
	AppID    int    `yaml:"app_id"`
	AppHash  string `yaml:"app_hash"`
	Session  string `yaml:"session"`
	Database string `yaml:"database"`
}

func (k KernelConfig) kernel() kernel.Config {
	return kernel.Config{Workers: k.Workers, MaxInFlight: k.MaxInFlight, HandlerTimeout: k.HandlerTimeout}
}

// loadConfig reads, expands, strictly decodes, defaults, and validates the
// config file.
func loadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := expandEnv(&root); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	var cfg Config
	if err := decodeStrict(&root, &cfg); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.DataDir == "" {
		c.DataDir = "data"
	}
	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = 30 * time.Second
	}
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Log.Format == "" {
		c.Log.Format = "json"
	}
	for i := range c.Telegram {
		a := &c.Telegram[i]
		if a.Session == "" {
			a.Session = filepath.Join(c.DataDir, "telegram", a.Account, "session.json")
		}
		if a.Database == "" {
			a.Database = filepath.Join(c.DataDir, "telegram", a.Account, "state.sqlite")
		}
	}
}

func (c Config) validate() error {
	var errs []error
	if c.ShutdownTimeout < 0 {
		errs = append(errs, errors.New("shutdown_timeout must not be negative"))
	}
	if _, err := logLevel(c.Log.Level); err != nil {
		errs = append(errs, err)
	}
	if c.Log.Format != "json" && c.Log.Format != "text" {
		errs = append(errs, fmt.Errorf("log.format %q: want json or text", c.Log.Format))
	}
	if c.Kernel.Workers < 0 || c.Kernel.MaxInFlight < 0 {
		errs = append(errs, errors.New("kernel.workers and kernel.max_in_flight must not be negative"))
	}
	seen := map[string]bool{}
	for i, a := range c.Telegram {
		field := fmt.Sprintf("telegram[%d]", i)
		switch {
		case a.Account == "":
			errs = append(errs, fmt.Errorf("%s.account is required", field))
		case seen[a.Account]:
			errs = append(errs, fmt.Errorf("%s.account %q is used twice", field, a.Account))
		}
		seen[a.Account] = true
		if a.AppID <= 0 || a.AppHash == "" {
			errs = append(errs, fmt.Errorf("%s: app_id and app_hash are required", field))
		}
	}
	for id := range c.Modules {
		if _, ok := moduleRegistry[id]; !ok {
			errs = append(errs, fmt.Errorf("modules.%s: unknown module", id))
		}
	}
	return errors.Join(errs...)
}

var envReference = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandEnv replaces ${VAR} in every scalar value with the environment
// variable's value. A reference to an unset variable is an error.
func expandEnv(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		original := n.Value
		var missing []string
		n.Value = envReference.ReplaceAllStringFunc(n.Value, func(ref string) string {
			name := envReference.FindStringSubmatch(ref)[1]
			value, ok := os.LookupEnv(name)
			if !ok {
				missing = append(missing, name)
			}
			return value
		})
		if len(missing) > 0 {
			return fmt.Errorf("line %d: environment variable %v is not set", n.Line, missing)
		}
		if n.Style == 0 && envReference.MatchString(original) {
			n.Tag = "" // let YAML infer the type of the substituted value, e.g. an int
		}
	}
	for _, child := range n.Content {
		if err := expandEnv(child); err != nil {
			return err
		}
	}
	return nil
}

// decodeStrict decodes n into v and rejects keys v does not define. Type
// errors carry the original file's line numbers. The unknown-key check
// re-encodes the tree, which shifts lines, so its errors name only the key.
func decodeStrict(n *yaml.Node, v any) error {
	if err := n.Decode(v); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	data, err := yaml.Marshal(n)
	if err != nil {
		return fmt.Errorf("re-encode config: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(reflect.New(reflect.TypeOf(v).Elem()).Interface()); err != nil {
		return errors.New(linePrefix.ReplaceAllString(err.Error(), ""))
	}
	return nil
}

var linePrefix = regexp.MustCompile(`line \d+: `)
