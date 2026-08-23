package memory

import (
	"fmt"
	"strings"
	"time"

	llmconfig "ex-otogi/pkg/llm/config"
)

const (
	defaultExtractionTimeout        = 30 * time.Second
	defaultExtractionMaxInputRunes  = 4000
	defaultConsolidationInterval    = time.Hour
	defaultRetrievalPlanningEnabled = true
	defaultRetrievalPlanningTimeout = 10 * time.Second
	defaultBufferQuietPeriod        = 2 * time.Minute
	defaultBufferMaxRunes           = 3000
	defaultBufferMaxArticles        = 30
	defaultBufferMaxAge             = 10 * time.Minute
	defaultBufferCheckInterval      = 15 * time.Second
	defaultRetrievalSearchLimit     = 20
	defaultMaxRetrievedMemories     = 5
	defaultMinMemorySimilarity      = 0.3
	defaultMaxMemoryRunes           = 2000
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
	// EmbeddingModel identifies the vector space model.
	EmbeddingModel string
	// EmbeddingDimensions fixes the vector space width.
	EmbeddingDimensions int
	// ExtractionTimeout bounds one extraction request lifecycle.
	ExtractionTimeout time.Duration
	// ExtractionMaxInputRunes caps the serialized context window passed to the
	// extractor.
	ExtractionMaxInputRunes int
	// ConsolidationInterval controls how often consolidation runs. Zero disables
	// background consolidation.
	ConsolidationInterval time.Duration
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
	// MaxRetrievedMemories caps records returned for one chat request.
	MaxRetrievedMemories int
	// MinMemorySimilarity filters weak semantic matches.
	MinMemorySimilarity float32
	// MaxMemoryRunes is the default serialized context budget.
	MaxMemoryRunes int
}

func defaultConfig() Config {
	return Config{
		Enabled:                  false,
		ExtractionTimeout:        defaultExtractionTimeout,
		ExtractionMaxInputRunes:  defaultExtractionMaxInputRunes,
		ConsolidationInterval:    defaultConsolidationInterval,
		RetrievalPlanningEnabled: defaultRetrievalPlanningEnabled,
		RetrievalPlanningTimeout: defaultRetrievalPlanningTimeout,
		BufferQuietPeriod:        defaultBufferQuietPeriod,
		BufferMaxRunes:           defaultBufferMaxRunes,
		BufferMaxArticles:        defaultBufferMaxArticles,
		BufferMaxAge:             defaultBufferMaxAge,
		BufferCheckInterval:      defaultBufferCheckInterval,
		RetrievalSearchLimit:     defaultRetrievalSearchLimit,
		MaxRetrievedMemories:     defaultMaxRetrievedMemories,
		MinMemorySimilarity:      defaultMinMemorySimilarity,
		MaxMemoryRunes:           defaultMaxMemoryRunes,
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
	if cfg.MaxRetrievedMemories <= 0 {
		return fmt.Errorf("validate memory config: max_retrieved_memories must be > 0")
	}
	if cfg.MinMemorySimilarity < 0 || cfg.MinMemorySimilarity > 1 {
		return fmt.Errorf("validate memory config: min_memory_similarity must be between 0 and 1")
	}
	if cfg.MaxMemoryRunes <= 0 {
		return fmt.Errorf("validate memory config: max_memory_runes must be > 0")
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
	if strings.TrimSpace(cfg.EmbeddingModel) == "" {
		return fmt.Errorf("validate memory config: embedding_model is required when enabled=true")
	}
	if cfg.EmbeddingDimensions <= 0 {
		return fmt.Errorf("validate memory config: embedding_dimensions must be > 0 when enabled=true")
	}

	return nil
}

func loadConfig(runtime llmconfig.Config) (Config, error) {
	cfg := defaultConfig()
	cfg = mergeMemoryConfig(cfg, runtime.Memory)
	if cfg.Enabled {
		provider, exists := runtime.Providers[cfg.EmbeddingProvider]
		if !exists {
			return Config{}, fmt.Errorf("validate memory config: embedding provider %s is not configured", cfg.EmbeddingProvider)
		}
		cfg.EmbeddingModel = strings.TrimSpace(provider.EmbeddingModel)
		cfg.EmbeddingDimensions = provider.EmbeddingDimensions
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func mergeMemoryConfig(base Config, raw *llmconfig.MemoryConfig) Config {
	if raw == nil {
		return base
	}

	cfg := base
	cfg.Enabled = true
	cfg.ExtractionProvider = strings.TrimSpace(raw.ExtractionProvider)
	cfg.ExtractionModel = strings.TrimSpace(raw.ExtractionModel)
	cfg.EmbeddingProvider = strings.TrimSpace(raw.EmbeddingProvider)

	return cfg
}
