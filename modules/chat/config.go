package chat

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Config is the module's section of otogi.yaml.
type Config struct {
	// Retention is how long threads and the chat transcript are kept;
	// default 30 days.
	Retention time.Duration `yaml:"retention"`
	// Timeout bounds one reply, tool calls included; default 5 minutes.
	Timeout time.Duration `yaml:"timeout"`
	// Profiles are the agents.
	Profiles []ProfileConfig `yaml:"profiles"`
}

// ProfileConfig configures one agent.
type ProfileConfig struct {
	// Name identifies the agent; it is stored with its threads.
	Name string `yaml:"name"`
	// Label, if set, starts each reply in bold.
	Label string `yaml:"label"`
	// Keywords trigger the agent when a message starts with one.
	Keywords []string `yaml:"keywords"`
	// Chats lists where the agent answers, as /whoami prints them
	// ("telegram:chat:123").
	Chats []string `yaml:"chats"`
	// Provider names a provider configured under llm.
	Provider string `yaml:"provider"`
	// Model is the provider's model ID.
	Model string `yaml:"model"`
	// Thinking is minimal, low, medium, or high; empty is the model default.
	Thinking string `yaml:"thinking"`
	// MaxOutput limits reply tokens; zero is the model default.
	MaxOutput int `yaml:"max_output"`
	// System is the system prompt.
	System string `yaml:"system"`
	// Context is how many recent chat messages the agent sees; default 30,
	// zero for none.
	Context *int `yaml:"context"`
	// MaxTurns is how many thread turns are sent, newest kept; default 40.
	MaxTurns int `yaml:"max_turns"`
	// Tools are built-ins (google_search, url_context, code_execution) and
	// tool groups, by module ID.
	Tools []string `yaml:"tools"`
	// MaxToolRounds bounds rounds of function calls per reply; default 8.
	MaxToolRounds int `yaml:"max_tool_rounds"`
}

// profile is a validated agent.
type profile struct {
	ProfileConfig
	keywords []string
	chats    map[string]bool
	context  int
	builtins []llm.ToolSpec
	groups   []otogi.ModuleID    // tool groups, by the module offering them
	tools    map[string]llm.Tool // the groups' functions, by name; set in Start
	provider llm.Provider
}

var builtinTools = []string{llm.BuiltinGoogleSearch, llm.BuiltinURLContext, llm.BuiltinCodeExecution}

// profiles validates the configuration and resolves providers.
func (c *Config) profiles(providers func(string) (llm.Provider, error)) ([]*profile, error) {
	if c.Retention == 0 {
		c.Retention = 30 * 24 * time.Hour
	}
	if c.Timeout == 0 {
		c.Timeout = 5 * time.Minute
	}
	var errs []error
	if c.Retention < 0 || c.Timeout < 0 {
		errs = append(errs, errors.New("retention and timeout must not be negative"))
	}
	if len(c.Profiles) == 0 {
		errs = append(errs, errors.New("profiles: at least one is required"))
	}
	var out []*profile
	names, keywords := map[string]bool{}, map[string]string{}
	for i, pc := range c.Profiles {
		field := fmt.Sprintf("profiles[%d]", i)
		p := &profile{ProfileConfig: pc, chats: map[string]bool{}, context: 30}
		if pc.Context != nil {
			p.context = *pc.Context
		}
		if p.MaxTurns == 0 {
			p.MaxTurns = 40
		}
		if p.MaxToolRounds == 0 {
			p.MaxToolRounds = 8
		}
		switch {
		case pc.Name == "":
			errs = append(errs, fmt.Errorf("%s.name is required", field))
		case names[pc.Name]:
			errs = append(errs, fmt.Errorf("%s.name %q is used twice", field, pc.Name))
		}
		names[pc.Name] = true
		if len(pc.Keywords) == 0 {
			errs = append(errs, fmt.Errorf("%s.keywords: at least one is required", field))
		}
		for _, k := range pc.Keywords {
			k = strings.TrimSpace(k)
			folded := strings.ToLower(k)
			switch {
			case k == "":
				errs = append(errs, fmt.Errorf("%s.keywords: empty keyword", field))
			case keywords[folded] != "" && keywords[folded] != pc.Name:
				errs = append(errs, fmt.Errorf("%s.keywords: %q also belongs to profile %q", field, k, keywords[folded]))
			}
			keywords[folded] = pc.Name
			p.keywords = append(p.keywords, k)
		}
		if len(pc.Chats) == 0 {
			errs = append(errs, fmt.Errorf("%s.chats: at least one is required", field))
		}
		for _, chat := range pc.Chats {
			if platform, id, ok := strings.Cut(chat, ":"); !ok || platform == "" || id == "" {
				errs = append(errs, fmt.Errorf("%s.chats: %q: want <platform>:<chat id>, as /whoami prints it", field, chat))
			}
			p.chats[chat] = true
		}
		if pc.Model == "" {
			errs = append(errs, fmt.Errorf("%s.model is required", field))
		}
		if !slices.Contains([]string{"", "minimal", "low", "medium", "high"}, pc.Thinking) {
			errs = append(errs, fmt.Errorf("%s.thinking %q: want minimal, low, medium, or high", field, pc.Thinking))
		}
		if p.context < 0 || p.MaxTurns < 0 || p.MaxOutput < 0 || p.MaxToolRounds < 0 {
			errs = append(errs, fmt.Errorf("%s: context, max_turns, max_output, and max_tool_rounds must not be negative", field))
		}
		for _, t := range pc.Tools {
			switch {
			case slices.Contains(builtinTools, t):
				p.builtins = append(p.builtins, llm.ToolSpec{Builtin: t})
			case t == "" || strings.ContainsAny(t, " :/"):
				errs = append(errs, fmt.Errorf("%s.tools: %q: want a built-in (%s) or a module ID", field, t, strings.Join(builtinTools, ", ")))
			default:
				p.groups = append(p.groups, otogi.ModuleID(t))
			}
		}
		provider, err := providers(pc.Provider)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s.provider: %w", field, err))
		}
		p.provider = provider
		out = append(out, p)
	}
	return out, errors.Join(errs...)
}

// keyword reports whether text starts with one of the profile's keywords: as
// a word of its own, or directly followed by anything when the keyword ends
// in a CJK character, which needs no space.
func (p *profile) keyword(text string) bool {
	text = strings.TrimLeftFunc(text, unicode.IsSpace)
	for _, k := range p.keywords {
		n := utf8.RuneCountInString(k)
		end := 0
		for i := 0; i < n && end < len(text); i++ {
			_, size := utf8.DecodeRuneInString(text[end:])
			end += size
		}
		if !strings.EqualFold(text[:end], k) {
			continue
		}
		next, _ := utf8.DecodeRuneInString(text[end:])
		last, _ := utf8.DecodeLastRuneInString(k)
		if end == len(text) || unicode.IsSpace(next) || unicode.IsPunct(next) || unicode.IsSymbol(next) || cjk(last) {
			return true
		}
	}
	return false
}

func cjk(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}
