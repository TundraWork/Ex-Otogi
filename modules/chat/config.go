package chat

import (
	"cmp"
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

// Config is the module's settings.
type Config struct {
	Retention   otogi.Duration  `json:"retention,omitempty" default:"720h" doc:"How long threads and the chat transcript are kept; 720h is 30 days."`
	Timeout     otogi.Duration  `json:"timeout,omitempty" default:"5m" doc:"The longest one reply may take, tool calls included."`
	ToolReserve *float64        `json:"tool_reserve,omitempty" minimum:"0" default:"2" doc:"When less of a reply's time is left than this many of its slowest model rounds, the agent stops calling tools and answers with what it has found. 0 calls tools until the time is up."`
	MaxMediaMB  int             `json:"max_media_mb,omitempty" minimum:"1" default:"20" doc:"The largest picture or file the agent is shown, in megabytes; a larger one is named instead."`
	Profiles    []ProfileConfig `json:"profiles" minItems:"1" doc:"The agents. Each answers in the chats on its allowlist."`
	Tools       []string        `json:"tools,omitempty" enum:"notes,reminders,todou" doc:"Modules whose tools agents may use. Each gets a list of the chats where agents are offered its tools; a chat on none of them gets none."`
	People      []PersonConfig  `json:"people,omitempty" doc:"People agents know by more than the name the platform gives them. Everyone else is called by that name, has no note, and a threshold factor of 1."`
}

// PersonConfig is what agents know about one person.
type PersonConfig struct {
	User            string  `json:"user" minLength:"1" doc:"The person: the platform (qq or telegram), a colon, and their sender ID as a chat's page lists it under People, below their name; for example qq:user:7C2E04D1A9B35F86E0C4D2B17A9F3E58. A person's ID on QQ is the same in every group and private chat. The same person on another platform is another entry."`
	Name            string  `json:"name,omitempty" doc:"What agents call them, in place of the name the platform gives; that name follows in parentheses where it differs, since people mention them by it."`
	Note            string  `json:"note,omitempty" doc:"Who they are, told to agents where they appear in the conversation: for example the owner, or a friend of the owner's who talks about VRChat. Agents may repeat it, in any chat the person is in."`
	ThresholdFactor float64 `json:"threshold_factor,omitempty" minimum:"0.1" maximum:"10" default:"1" doc:"Multiplies the judgment thresholds of their messages: below 1 their messages wake agents more easily, above 1 less. Past 1, judgments are not asked about their messages and never wake agents for them; mentions and replies still do."`
}

// person is what agents know about one person, from the people list.
type person struct {
	name   string  // what agents call them; "" for the name the platform gives
	note   string  // who they are
	factor float64 // multiplies the judgment thresholds of their messages
}

// people validates the people list and keys it by user.
func (c *Config) people() (map[string]person, error) {
	out := map[string]person{}
	var errs []error
	for i, pc := range c.People {
		field := fmt.Sprintf("people[%d].user", i)
		platform, id, ok := strings.Cut(pc.User, ":")
		switch {
		case !ok || platform == "" || id == "" || strings.ContainsFunc(pc.User, unicode.IsSpace):
			errs = append(errs, &otogi.SettingError{Path: field, Err: fmt.Errorf("%q: want the platform, a colon, and the sender ID, such as qq:user:7C2E04D1A9B35F86E0C4D2B17A9F3E58", pc.User)})
			continue
		case slices.ContainsFunc(c.People[:i], func(o PersonConfig) bool { return o.User == pc.User }):
			errs = append(errs, &otogi.SettingError{Path: field, Err: fmt.Errorf("%q is listed twice", pc.User)})
			continue
		}
		// The schema keeps a factor within 0.1 to 10; unset is 1. One line
		// each: a note is a line of a list the model reads.
		out[pc.User] = person{name: strings.Join(strings.Fields(pc.Name), " "), note: strings.Join(strings.Fields(pc.Note), " "), factor: cmp.Or(pc.ThresholdFactor, 1)}
	}
	return out, errors.Join(errs...)
}

// ProfileConfig configures one agent.
type ProfileConfig struct {
	Name          string       `json:"name" minLength:"1" doc:"Identifies the agent and names its allowlist. Its threads are stored under it: keep it stable."`
	Label         string       `json:"label,omitempty" doc:"When set, starts each reply in bold."`
	Keywords      []string     `json:"keywords" minItems:"1" doc:"A message starting with one of them triggers the agent; so does replying to it. In a group, with judge.wake on, they only tell the judgment the agent's names: it decides whether such a message is talking to the agent, and when it fails, the keyword triggers the agent as without it."`
	Mention       bool         `json:"mention,omitempty" doc:"A message mentioning the bot account triggers the agent too. A reply to a message the account sent for another module, such as a card or a reminder, does not, though Telegram and QQ count it as a mention. When several agents in a chat set it, the first answers."`
	Provider      string       `json:"provider" minLength:"1" format:"llm-provider" doc:"A language model provider set in the panel under Language models: gemini, or the name of an OpenAI-compatible service such as deepseek."`
	Model         string       `json:"model" minLength:"1" format:"llm-model" doc:"The provider's model ID."`
	Thinking      string       `json:"thinking,omitempty" enum:"minimal,low,medium,high" format:"llm-thinking" doc:"How much the model thinks before answering; unset is the model's default."`
	MaxOutput     int          `json:"max_output,omitempty" minimum:"1" doc:"The most tokens a reply may have; unset is the model's default."`
	Temperature   *float64     `json:"temperature,omitempty" minimum:"0" maximum:"2" doc:"Sampling temperature, 0 to 2; unset is the model's default (1 for Gemini). Higher is livelier, lower more predictable."`
	System        string       `json:"system,omitempty" format:"markdown" doc:"The system prompt: who the agent is and how it talks, in Markdown."`
	Context       *int         `json:"context,omitempty" minimum:"0" default:"30" doc:"How many recent chat messages the agent sees; 0 for none."`
	MaxTurns      int          `json:"max_turns,omitempty" minimum:"1" default:"40" doc:"How many turns of a thread are sent, newest kept."`
	Tools         []string     `json:"tools,omitempty" enum:"web_search,url_context,code_execution" doc:"The model's built-in tools the agent uses everywhere it answers. web_search is the provider's own search: Google Search on Gemini, cited; on DeepSeek, searching and opening pages over several steps. url_context reads the pages the conversation links (Gemini, DeepSeek). code_execution runs code the model writes (Gemini). Modules' tools are offered per chat: see Tools, after the profiles."`
	MaxToolRounds int          `json:"max_tool_rounds,omitempty" minimum:"1" default:"8" doc:"The most rounds of tool calls in one reply."`
	Judge         *JudgeConfig `json:"judge,omitempty" doc:"Has the decision service judge, in groups, the messages no keyword, reply, or mention triggers. Needs the decision module."`
	Media         *MediaConfig `json:"media,omitempty" doc:"Use the media module's descriptions: in the recent messages, the quoted message, and the wake judgment, and, with as_text, in place of the current message's media. Needs the media module, with the agent's chats on its describe list."`
}

// MediaConfig has an agent read the media module's descriptions.
type MediaConfig struct {
	AsText   bool  `json:"as_text,omitempty" doc:"Give the model descriptions instead of the current and the quoted message's media, for a model that sees images poorly or not at all. Media left undescribed is named by its kind, not shown."`
	Stickers []int `json:"stickers,omitempty" doc:"The numbers of the stickers, as /sticker list shows them, this agent is shown when it may send one. Empty gives it no stickers and no send_sticker. The list goes before each turn, at full price on every reply since it comes after the cached part of the request; the agent picks one with send_sticker and it is sent after its reply, at most one per reply. It may also send a kept sticker not listed here when someone names its number. Verified on QQ only so far."`
}

// JudgeConfig has the decision service judge group messages for an agent.
// Each block turns one judgment on.
type JudgeConfig struct {
	Rules string      `json:"rules,omitempty" doc:"What else counts, or does not, as talking to the agent: for example that its name is also a product people discuss."`
	Wake  *WakeConfig `json:"wake,omitempty" doc:"Answer group messages addressed to the agent that no trigger caught, such as its name in the middle of a sentence."`
}

// WakeConfig configures answering messages judged to be addressed to the
// agent.
type WakeConfig struct {
	Threshold float64 `json:"threshold,omitempty" minimum:"0" maximum:"1" default:"0.7" doc:"How sure the judgment must be that a message is addressed to the agent. When several agents pass, the surest answers."`
}

// defaultWake is the wake threshold of a wake block that sets none.
const defaultWake = 0.7

// profile is a validated agent.
type profile struct {
	ProfileConfig
	keywords []string
	context  int
	builtins []llm.ToolSpec
	provider llm.Provider
	wake     float64 // how sure a judgment must be to wake it; 0 when it is not judged
	rules    string  // what else counts as talking to it, for judgments
}

// toolsList names the allowlist of the chats where agents get a module's
// tools. Profile lists are named as their profiles, which may not start
// so.
func toolsList(module otogi.ModuleID) string { return "tools:" + string(module) }

// toolModules validates the modules whose tools agents may use.
func (c *Config) toolModules() ([]otogi.ModuleID, error) {
	var out []otogi.ModuleID
	var errs []error
	for i, t := range c.Tools {
		field := fmt.Sprintf("tools[%d]", i)
		switch {
		case slices.Contains(builtinTools, t):
			errs = append(errs, &otogi.SettingError{Path: field, Err: fmt.Errorf("%q is a built-in tool: set it on the profiles that use it", t)})
		case t == "" || strings.ContainsAny(t, " :/"):
			errs = append(errs, &otogi.SettingError{Path: field, Err: fmt.Errorf("%q: want a module ID", t)})
		case slices.Contains(out, otogi.ModuleID(t)):
			errs = append(errs, &otogi.SettingError{Path: field, Err: fmt.Errorf("%q is listed twice", t)})
		default:
			out = append(out, otogi.ModuleID(t))
		}
	}
	return out, errors.Join(errs...)
}

var builtinTools = []string{llm.BuiltinWebSearch, llm.BuiltinURLContext, llm.BuiltinCodeExecution}

// profiles validates the configuration and resolves providers.
func (c *Config) profiles(providers func(string) (llm.Provider, error)) ([]*profile, error) {
	if c.Retention == 0 {
		c.Retention = otogi.Duration(30 * 24 * time.Hour)
	}
	if c.Timeout == 0 {
		c.Timeout = otogi.Duration(5 * time.Minute)
	}
	if c.ToolReserve == nil {
		reserve := 2.0
		c.ToolReserve = &reserve
	}
	if c.MaxMediaMB == 0 {
		c.MaxMediaMB = 20
	}
	var errs []error
	if c.Retention < 0 || c.Timeout < 0 {
		errs = append(errs, errors.New("retention and timeout must not be negative"))
	}
	if len(c.Profiles) == 0 {
		errs = append(errs, &otogi.SettingError{Path: "profiles", Err: errors.New("at least one is required")})
	}
	var out []*profile
	names, keywords := map[string]bool{}, map[string]string{}
	for i, pc := range c.Profiles {
		field := fmt.Sprintf("profiles[%d]", i)
		refuse := func(path string, err error) { errs = append(errs, &otogi.SettingError{Path: field + path, Err: err}) }
		p := &profile{ProfileConfig: pc, context: 30}
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
			refuse(".name", errors.New("required"))
		case strings.HasPrefix(pc.Name, "tools:"):
			refuse(".name", fmt.Errorf("%q: names starting with tools: are the lists of modules' tools", pc.Name))
		case names[pc.Name]:
			refuse(".name", fmt.Errorf("%q is used twice", pc.Name))
		}
		names[pc.Name] = true
		if len(pc.Keywords) == 0 {
			refuse(".keywords", errors.New("at least one is required"))
		}
		for j, k := range pc.Keywords {
			k = strings.TrimSpace(k)
			folded := strings.ToLower(k)
			switch {
			case k == "":
				refuse(fmt.Sprintf(".keywords[%d]", j), errors.New("empty keyword"))
			case keywords[folded] != "" && keywords[folded] != pc.Name:
				refuse(fmt.Sprintf(".keywords[%d]", j), fmt.Errorf("%q also belongs to profile %q", k, keywords[folded]))
			}
			keywords[folded] = pc.Name
			p.keywords = append(p.keywords, k)
		}
		if pc.Model == "" {
			refuse(".model", errors.New("required"))
		}
		if pc.Temperature != nil && (*pc.Temperature < 0 || *pc.Temperature > 2) {
			refuse(".temperature", fmt.Errorf("%v: want 0 to 2", *pc.Temperature))
		}
		if !slices.Contains([]string{"", "minimal", "low", "medium", "high"}, pc.Thinking) {
			refuse(".thinking", fmt.Errorf("%q: want minimal, low, medium, or high", pc.Thinking))
		}
		if p.context < 0 || p.MaxTurns < 0 || p.MaxOutput < 0 || p.MaxToolRounds < 0 {
			refuse("", errors.New("context, max_turns, max_output, and max_tool_rounds must not be negative"))
		}
		for j, t := range pc.Tools {
			if !slices.Contains(builtinTools, t) {
				refuse(fmt.Sprintf(".tools[%d]", j), fmt.Errorf("%q: want a built-in (%s); modules' tools are set in tools, per chat", t, strings.Join(builtinTools, ", ")))
				continue
			}
			p.builtins = append(p.builtins, llm.ToolSpec{Builtin: t})
		}
		if pc.Media != nil {
			for j, n := range pc.Media.Stickers {
				if n <= 0 {
					refuse(fmt.Sprintf(".media.stickers[%d]", j), fmt.Errorf("%d: want a sticker's number, as /sticker list shows it", n))
				}
			}
		}
		if pc.Judge != nil {
			p.rules = strings.TrimSpace(pc.Judge.Rules)
			if pc.Judge.Wake != nil {
				p.wake = cmp.Or(pc.Judge.Wake.Threshold, defaultWake)
			}
		}
		provider, err := providers(pc.Provider)
		if err != nil {
			refuse(".provider", err)
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
