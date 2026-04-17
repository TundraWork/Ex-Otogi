package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"ex-otogi/pkg/otogi/core"

	llmconfig "ex-otogi/pkg/llm/config"
)

const (
	defaultExtractionTimeout            = 30 * time.Second
	defaultExtractionMaxInputRunes      = 4000
	defaultConsolidationInterval        = time.Hour
	defaultMaxMemoriesPerScope          = 200
	defaultDecayFactor                  = 0.995
	defaultMinImportance                = 3
	defaultDuplicateSimilarityThreshold = 0.85
	defaultRetrievalPlanningEnabled     = true
	defaultRetrievalPlanningTimeout     = 10 * time.Second
	defaultBufferQuietPeriod            = 2 * time.Minute
	defaultBufferMaxRunes               = 3000
	defaultBufferMaxArticles            = 30
	defaultBufferMaxAge                 = 10 * time.Minute
	defaultBufferCheckInterval          = 15 * time.Second
	defaultRetrievalSearchLimit         = 20
)

// Config configures memory module behavior.
type Config struct {
	// Enabled turns on natural memory formation.
	Enabled bool
	// ExtractionProvider identifies which provider profile handles extraction.
	ExtractionProvider string
	// ExtractionModel identifies which model handles extraction.
	ExtractionModel string
	// EmbeddingProvider identifies which embedding profile powers dedup and
	// retrieval-related processing.
	EmbeddingProvider string
	// ExtractionTimeout bounds one extraction request lifecycle.
	ExtractionTimeout time.Duration
	// ExtractionMaxInputRunes caps the serialized context window passed to the
	// extractor.
	ExtractionMaxInputRunes int
	// ConsolidationInterval controls how often consolidation runs. Zero disables
	// background consolidation.
	ConsolidationInterval time.Duration
	// MaxMemoriesPerScope caps the number of retained memories per scope.
	MaxMemoriesPerScope int
	// DecayFactor controls recency decay and must be in (0, 1].
	DecayFactor float64
	// MinImportance is the minimum importance score kept during pruning.
	MinImportance int
	// DuplicateSimilarityThreshold controls when two memories are considered the
	// same fact and must be in (0, 1].
	DuplicateSimilarityThreshold float32
	// RetrievalPlanningEnabled turns on adaptive retrieval planning in llmchat.
	RetrievalPlanningEnabled bool
	// RetrievalPlanningTimeout bounds one retrieval planning request lifecycle.
	RetrievalPlanningTimeout time.Duration
	// BufferQuietPeriod is the debounce duration after the last article before
	// the window is flushed to extraction.
	BufferQuietPeriod time.Duration
	// BufferMaxRunes caps how many runes of article text can accumulate in one
	// window before a forced flush.
	BufferMaxRunes int
	// BufferMaxArticles caps how many articles can accumulate in one window
	// before a forced flush.
	BufferMaxArticles int
	// BufferMaxAge caps the maximum age of the oldest article in a window
	// before a forced flush.
	BufferMaxAge time.Duration
	// BufferCheckInterval controls how often the flush worker scans for ready
	// windows.
	BufferCheckInterval time.Duration
	// RetrievalSearchLimit caps how many existing memories are shown to the
	// extraction LLM as candidates for UPDATE/DELETE actions.
	RetrievalSearchLimit int
}

type fileModuleConfig struct {
	ConfigFile string `json:"config_file"`
}

func defaultConfig() Config {
	return Config{
		Enabled:                      false,
		ExtractionTimeout:            defaultExtractionTimeout,
		ExtractionMaxInputRunes:      defaultExtractionMaxInputRunes,
		ConsolidationInterval:        defaultConsolidationInterval,
		MaxMemoriesPerScope:          defaultMaxMemoriesPerScope,
		DecayFactor:                  defaultDecayFactor,
		MinImportance:                defaultMinImportance,
		DuplicateSimilarityThreshold: defaultDuplicateSimilarityThreshold,
		RetrievalPlanningEnabled:     defaultRetrievalPlanningEnabled,
		RetrievalPlanningTimeout:     defaultRetrievalPlanningTimeout,
		BufferQuietPeriod:            defaultBufferQuietPeriod,
		BufferMaxRunes:               defaultBufferMaxRunes,
		BufferMaxArticles:            defaultBufferMaxArticles,
		BufferMaxAge:                 defaultBufferMaxAge,
		BufferCheckInterval:          defaultBufferCheckInterval,
		RetrievalSearchLimit:         defaultRetrievalSearchLimit,
	}
}

// Validate checks whether the module configuration is internally coherent.
func (cfg Config) Validate() error {
	if cfg.ExtractionTimeout <= 0 {
		return fmt.Errorf("validate memory config: extraction_timeout must be > 0")
	}
	if cfg.ExtractionMaxInputRunes <= 0 {
		return fmt.Errorf("validate memory config: extraction_max_input_runes must be > 0")
	}
	if cfg.ConsolidationInterval < 0 {
		return fmt.Errorf("validate memory config: consolidation_interval must be >= 0")
	}
	if cfg.MaxMemoriesPerScope <= 0 {
		return fmt.Errorf("validate memory config: max_memories_per_scope must be > 0")
	}
	if cfg.DecayFactor <= 0 || cfg.DecayFactor > 1 {
		return fmt.Errorf("validate memory config: decay_factor must be between 0 and 1")
	}
	if cfg.MinImportance < 1 || cfg.MinImportance > 10 {
		return fmt.Errorf("validate memory config: min_importance must be between 1 and 10")
	}
	if cfg.DuplicateSimilarityThreshold <= 0 || cfg.DuplicateSimilarityThreshold > 1 {
		return fmt.Errorf("validate memory config: duplicate_similarity_threshold must be between 0 and 1")
	}
	if cfg.RetrievalPlanningTimeout <= 0 {
		return fmt.Errorf("validate memory config: retrieval_planning_timeout must be > 0")
	}
	if cfg.BufferQuietPeriod <= 0 {
		return fmt.Errorf("validate memory config: buffer_quiet_period must be > 0")
	}
	if cfg.BufferMaxRunes <= 0 {
		return fmt.Errorf("validate memory config: buffer_max_runes must be > 0")
	}
	if cfg.BufferMaxRunes > cfg.ExtractionMaxInputRunes {
		return fmt.Errorf("validate memory config: buffer_max_runes must be <= extraction_max_input_runes")
	}
	if cfg.BufferMaxArticles <= 0 {
		return fmt.Errorf("validate memory config: buffer_max_articles must be > 0")
	}
	if cfg.BufferMaxAge < cfg.BufferQuietPeriod {
		return fmt.Errorf("validate memory config: buffer_max_age must be >= buffer_quiet_period")
	}
	if cfg.BufferCheckInterval <= 0 {
		return fmt.Errorf("validate memory config: buffer_check_interval must be > 0")
	}
	if cfg.RetrievalSearchLimit <= 0 {
		return fmt.Errorf("validate memory config: retrieval_search_limit must be > 0")
	}
	if !cfg.Enabled {
		return nil
	}
	if strings.TrimSpace(cfg.ExtractionProvider) == "" {
		return fmt.Errorf("validate memory config: extraction_provider is required when enabled=true")
	}
	if strings.TrimSpace(cfg.ExtractionModel) == "" {
		return fmt.Errorf("validate memory config: extraction_model is required when enabled=true")
	}
	if strings.TrimSpace(cfg.EmbeddingProvider) == "" {
		return fmt.Errorf("validate memory config: embedding_provider is required when enabled=true")
	}

	return nil
}

func loadConfig(registry core.ConfigRegistry) (Config, error) {
	cfg := defaultConfig()
	if registry == nil {
		return Config{}, fmt.Errorf("nil config registry")
	}

	raw, err := registry.Resolve("memory")
	switch {
	case err == nil:
	case errors.Is(err, core.ErrConfigNotFound):
		return cfg, nil
	default:
		return Config{}, fmt.Errorf("resolve module config: %w", err)
	}

	var parsed fileModuleConfig
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Config{}, fmt.Errorf("unmarshal: %w", err)
	}
	configFile := strings.TrimSpace(parsed.ConfigFile)
	if envPath := strings.TrimSpace(os.Getenv("OTOGI_LLM_CONFIG_FILE")); envPath != "" {
		configFile = envPath
	}
	if configFile == "" {
		return Config{}, fmt.Errorf("config_file is required")
	}

	llmCfg, err := llmconfig.LoadFile(configFile)
	if err != nil {
		return Config{}, fmt.Errorf("load llm config file %s: %w", configFile, err)
	}

	cfg = mergeNaturalMemoryConfig(cfg, llmCfg.NaturalMemory)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func mergeNaturalMemoryConfig(base Config, raw *llmconfig.NaturalMemoryConfig) Config {
	if raw == nil {
		return base
	}

	cfg := base
	cfg.Enabled = raw.Enabled
	cfg.ExtractionProvider = strings.TrimSpace(raw.ExtractionProvider)
	cfg.ExtractionModel = strings.TrimSpace(raw.ExtractionModel)
	cfg.EmbeddingProvider = strings.TrimSpace(raw.EmbeddingProvider)
	cfg.ExtractionTimeout = raw.ExtractionTimeout
	cfg.ExtractionMaxInputRunes = raw.ExtractionMaxInputRunes
	cfg.ConsolidationInterval = raw.ConsolidationInterval
	cfg.MaxMemoriesPerScope = raw.MaxMemoriesPerScope
	cfg.DecayFactor = raw.DecayFactor
	cfg.MinImportance = raw.MinImportance
	cfg.DuplicateSimilarityThreshold = raw.DuplicateSimilarityThreshold
	cfg.RetrievalPlanningEnabled = raw.RetrievalPlanningEnabled
	cfg.RetrievalPlanningTimeout = raw.RetrievalPlanningTimeout
	cfg.BufferQuietPeriod = raw.BufferQuietPeriod
	cfg.BufferMaxRunes = raw.BufferMaxRunes
	cfg.BufferMaxArticles = raw.BufferMaxArticles
	cfg.BufferMaxAge = raw.BufferMaxAge
	cfg.BufferCheckInterval = raw.BufferCheckInterval
	cfg.RetrievalSearchLimit = raw.RetrievalSearchLimit

	return cfg
}
