package naturalmemory

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
	defaultConsolidationTimeout         = 60 * time.Second
	defaultMaxMemoriesPerScope          = 200
	defaultDecayFactor                  = 0.995
	defaultMinImportance                = 3
	defaultDuplicateSimilarityThreshold = 0.85
	defaultContextWindowSize            = 5
	defaultSynthesisMatchLimit          = 5
	defaultReflectionMinSourceMemories  = 8
	defaultReflectionSourceLimit        = 20
	defaultReflectionMaxGenerated       = 3
	defaultRetrievalPlanningEnabled     = true
	defaultRetrievalPlanningTimeout     = 10 * time.Second
)

// Config configures naturalmemory module behavior.
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
	// ConsolidationProvider identifies which provider profile handles
	// consolidation requests.
	ConsolidationProvider string
	// ConsolidationModel identifies which model handles consolidation.
	ConsolidationModel string
	// ConsolidationTimeout bounds one consolidation request lifecycle.
	ConsolidationTimeout time.Duration
	// MaxMemoriesPerScope caps the number of retained memories per scope.
	MaxMemoriesPerScope int
	// DecayFactor controls recency decay and must be in (0, 1].
	DecayFactor float64
	// MinImportance is the minimum importance score kept during pruning.
	MinImportance int
	// DuplicateSimilarityThreshold controls when two memories are considered the
	// same fact and must be in (0, 1].
	DuplicateSimilarityThreshold float32
	// ContextWindowSize controls how many earlier conversation entries are
	// included before the current article during extraction.
	ContextWindowSize int
	// SynthesisMatchLimit caps how many nearby memories participate in one
	// write-time synthesis decision.
	SynthesisMatchLimit int
	// ReflectionMinSourceMemories is the minimum surviving memory count required
	// before reflection generation runs.
	ReflectionMinSourceMemories int
	// ReflectionSourceLimit caps how many memories feed one reflection request.
	ReflectionSourceLimit int
	// ReflectionMaxGenerated caps how many reflection memories are generated per
	// consolidation cycle.
	ReflectionMaxGenerated int
	// RetrievalPlanningEnabled turns on adaptive retrieval planning in llmchat.
	RetrievalPlanningEnabled bool
	// RetrievalPlanningTimeout bounds one retrieval planning request lifecycle.
	RetrievalPlanningTimeout time.Duration
}

type fileModuleConfig struct {
	ConfigFile        string `json:"config_file"`
	ContextWindowSize *int   `json:"context_window_size"`
}

func defaultConfig() Config {
	return Config{
		Enabled:                      false,
		ExtractionTimeout:            defaultExtractionTimeout,
		ExtractionMaxInputRunes:      defaultExtractionMaxInputRunes,
		ConsolidationInterval:        defaultConsolidationInterval,
		ConsolidationTimeout:         defaultConsolidationTimeout,
		MaxMemoriesPerScope:          defaultMaxMemoriesPerScope,
		DecayFactor:                  defaultDecayFactor,
		MinImportance:                defaultMinImportance,
		DuplicateSimilarityThreshold: defaultDuplicateSimilarityThreshold,
		ContextWindowSize:            defaultContextWindowSize,
		SynthesisMatchLimit:          defaultSynthesisMatchLimit,
		ReflectionMinSourceMemories:  defaultReflectionMinSourceMemories,
		ReflectionSourceLimit:        defaultReflectionSourceLimit,
		ReflectionMaxGenerated:       defaultReflectionMaxGenerated,
		RetrievalPlanningEnabled:     defaultRetrievalPlanningEnabled,
		RetrievalPlanningTimeout:     defaultRetrievalPlanningTimeout,
	}
}

// Validate checks whether the module configuration is internally coherent.
func (cfg Config) Validate() error {
	if cfg.ExtractionTimeout <= 0 {
		return fmt.Errorf("validate naturalmemory config: extraction_timeout must be > 0")
	}
	if cfg.ExtractionMaxInputRunes <= 0 {
		return fmt.Errorf("validate naturalmemory config: extraction_max_input_runes must be > 0")
	}
	if cfg.ConsolidationInterval < 0 {
		return fmt.Errorf("validate naturalmemory config: consolidation_interval must be >= 0")
	}
	if cfg.ConsolidationTimeout <= 0 {
		return fmt.Errorf("validate naturalmemory config: consolidation_timeout must be > 0")
	}
	if cfg.MaxMemoriesPerScope <= 0 {
		return fmt.Errorf("validate naturalmemory config: max_memories_per_scope must be > 0")
	}
	if cfg.DecayFactor <= 0 || cfg.DecayFactor > 1 {
		return fmt.Errorf("validate naturalmemory config: decay_factor must be between 0 and 1")
	}
	if cfg.MinImportance < 1 || cfg.MinImportance > 10 {
		return fmt.Errorf("validate naturalmemory config: min_importance must be between 1 and 10")
	}
	if cfg.DuplicateSimilarityThreshold <= 0 || cfg.DuplicateSimilarityThreshold > 1 {
		return fmt.Errorf("validate naturalmemory config: duplicate_similarity_threshold must be between 0 and 1")
	}
	if cfg.ContextWindowSize <= 0 {
		return fmt.Errorf("validate naturalmemory config: context_window_size must be > 0")
	}
	if cfg.SynthesisMatchLimit <= 0 {
		return fmt.Errorf("validate naturalmemory config: synthesis_match_limit must be > 0")
	}
	if cfg.ReflectionMinSourceMemories <= 0 {
		return fmt.Errorf("validate naturalmemory config: reflection_min_source_memories must be > 0")
	}
	if cfg.ReflectionSourceLimit <= 0 {
		return fmt.Errorf("validate naturalmemory config: reflection_source_limit must be > 0")
	}
	if cfg.ReflectionMaxGenerated <= 0 {
		return fmt.Errorf("validate naturalmemory config: reflection_max_generated must be > 0")
	}
	if cfg.RetrievalPlanningTimeout <= 0 {
		return fmt.Errorf("validate naturalmemory config: retrieval_planning_timeout must be > 0")
	}
	if !cfg.Enabled {
		return nil
	}
	if strings.TrimSpace(cfg.ExtractionProvider) == "" {
		return fmt.Errorf("validate naturalmemory config: extraction_provider is required when enabled=true")
	}
	if strings.TrimSpace(cfg.ExtractionModel) == "" {
		return fmt.Errorf("validate naturalmemory config: extraction_model is required when enabled=true")
	}
	if strings.TrimSpace(cfg.EmbeddingProvider) == "" {
		return fmt.Errorf("validate naturalmemory config: embedding_provider is required when enabled=true")
	}
	if cfg.ConsolidationInterval > 0 {
		if strings.TrimSpace(cfg.ConsolidationProvider) == "" {
			return fmt.Errorf("validate naturalmemory config: consolidation_provider is required when consolidation_interval > 0")
		}
		if strings.TrimSpace(cfg.ConsolidationModel) == "" {
			return fmt.Errorf("validate naturalmemory config: consolidation_model is required when consolidation_interval > 0")
		}
	}

	return nil
}

func loadConfig(registry core.ConfigRegistry) (Config, error) {
	cfg := defaultConfig()
	if registry == nil {
		return Config{}, fmt.Errorf("nil config registry")
	}

	raw, err := registry.Resolve("naturalmemory")
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
	if parsed.ContextWindowSize != nil {
		cfg.ContextWindowSize = *parsed.ContextWindowSize
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
	cfg.ConsolidationProvider = strings.TrimSpace(raw.ConsolidationProvider)
	cfg.ConsolidationModel = strings.TrimSpace(raw.ConsolidationModel)
	cfg.ConsolidationTimeout = raw.ConsolidationTimeout
	cfg.MaxMemoriesPerScope = raw.MaxMemoriesPerScope
	cfg.DecayFactor = raw.DecayFactor
	cfg.MinImportance = raw.MinImportance
	cfg.DuplicateSimilarityThreshold = raw.DuplicateSimilarityThreshold
	cfg.ContextWindowSize = raw.ContextWindowSize
	cfg.SynthesisMatchLimit = raw.SynthesisMatchLimit
	cfg.ReflectionMinSourceMemories = raw.ReflectionMinSourceMemories
	cfg.ReflectionSourceLimit = raw.ReflectionSourceLimit
	cfg.ReflectionMaxGenerated = raw.ReflectionMaxGenerated
	cfg.RetrievalPlanningEnabled = raw.RetrievalPlanningEnabled
	cfg.RetrievalPlanningTimeout = raw.RetrievalPlanningTimeout

	return cfg
}
