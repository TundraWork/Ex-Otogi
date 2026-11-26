package chat

import (
	"cmp"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	mediasvc "github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Config is the module's settings.
type Config struct {
	Retention     otogi.Duration  `json:"retention,omitempty" default:"720h" doc:"How long threads and the chat transcript are kept."`
	Timeout       otogi.Duration  `json:"timeout,omitempty" default:"5m" doc:"The longest one reply may take, tool calls included."`
	ToolReserve   *float64        `json:"tool_reserve,omitempty" minimum:"0" default:"2" doc:"When less of a reply's time is left than this many of its slowest model rounds, the agent stops calling tools and answers with what it has found. 0 calls tools until the time is up."`
	MaxMediaMB    int             `json:"max_media_mb,omitempty" minimum:"1" default:"20" unit:"MB" doc:"The largest picture or file the agent is shown, in megabytes; a larger one is named instead."`
	DescribedText int             `json:"described_text,omitempty" minimum:"1" default:"1000" unit:"characters" doc:"How many characters of a picture's or video's text the recent chat, quotes, and judgments keep; with as_text, the message being answered gets it all."`
	DescribeWait  DescribeWait    `json:"describe_wait,omitzero" doc:"How long a message waits for the media module to describe its media, for agents that read descriptions. Only a limit: a message waits no longer than its descriptions take, and one without media does not wait."`
	Profiles      []ProfileConfig `json:"profiles" minItems:"1" ordered:"true" doc:"The agents. Each answers in the chats on its allowlist."`
	Tools         []string        `json:"tools,omitempty" format:"tool-module" doc:"Modules whose tools agents may use. Each gets a list of the chats where agents are offered its tools; a chat on none of them gets none."`
	People        []PersonConfig  `json:"people,omitempty" doc:"People agents know by more than the name the platform gives them. Everyone else is called by that name, has no note, and a threshold factor of 1."`
}

// PersonConfig is what agents know about one person.
type PersonConfig struct {
	User            string  `json:"user" minLength:"1" format:"actor" doc:"The person: the platform (qq or telegram), a colon, and their sender ID as a chat's People list shows it, such as telegram:user:123456789. A QQ ID is the same in every chat; the same person on another platform is another entry."`
	Name            string  `json:"name,omitempty" doc:"What agents call them, in place of the name the platform gives; that name follows in parentheses where it differs, since people mention them by it."`
	Note            string  `json:"note,omitempty" format:"text" doc:"Who they are, told to agents where they appear in the conversation: for example the owner, or a friend of the owner's who talks about VRChat. Agents may repeat it, in any chat the person is in."`
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
	Name          string       `json:"name" minLength:"1" doc:"Identifies the agent and names its allowlist. Its threads are stored under it, so it cannot be changed once saved. To replace a profile, add the other in one save and remove it in another."`
	Label         string       `json:"label,omitempty" doc:"When set, starts each reply in bold."`
	Keywords      []string     `json:"keywords" minItems:"1" doc:"A message starting with one, or a reply to the agent, triggers it. In a group with judge.wake on, they name the agent for the judgment, which decides whether the message talks to it; when the judgment fails, the keyword triggers it."`
	Mention       bool         `json:"mention,omitempty" doc:"A message mentioning the bot account triggers the agent too; a reply to a card or reminder the account sent for another module does not, though Telegram and QQ count it as one. Of several agents in a chat that set it, the first answers."`
	Provider      string       `json:"provider" minLength:"1" format:"llm-provider" doc:"A language model provider set in the panel under Language models: gemini, or the name of an OpenAI-compatible service such as deepseek."`
	Model         string       `json:"model" minLength:"1" format:"llm-model" doc:"The provider's model ID."`
	Thinking      string       `json:"thinking,omitempty" enum:"minimal,low,medium,high" format:"llm-thinking" doc:"How much the model thinks before answering; unset is the model's default."`
	MaxOutput     int          `json:"max_output,omitempty" minimum:"1" doc:"The most tokens a reply may have; unset is the model's default."`
	Temperature   *float64     `json:"temperature,omitempty" minimum:"0" maximum:"2" doc:"Sampling temperature, 0 to 2; unset is the model's default (1 for Gemini). Higher is livelier, lower more predictable."`
	System        string       `json:"system,omitempty" format:"markdown" doc:"The system prompt: who the agent is and how it talks, in Markdown."`
	PlainText     bool         `json:"plain_text,omitempty" doc:"Replies are plain text: the agent is not asked for Markdown, and what the model writes is sent as it is, unformatted, the label too. In QQ private chats a streamed reply is still shown as Markdown."`
	Quote         string       `json:"quote,omitempty" enum:"always,never,when_interrupted" default:"always" doc:"Whether a reply quotes the message it answers: always; never; or when_interrupted, only when the chat said something else after that message before the reply started. A sticker after the reply does the same."`
	Sources       *bool        `json:"sources,omitempty" default:"true" doc:"End a reply that cites pages, after a web search, with a line linking them: Sources: title, …. Off, the reply ends with what the model wrote; chat.reply still records the pages."`
	FailureNotes  string       `json:"failure_notes,omitempty" enum:"everywhere,direct_chats" default:"everywhere" doc:"Where a reply says how it failed or that it has no answer (Sorry, I couldn't reply…, Cut short…, I can't answer that…, I have nothing to add.): everywhere; or direct_chats, so that in groups and channels a failed reply sends only what it wrote, or nothing. chat.reply records why either way."`
	OwnFormat     bool         `json:"own_format,omitempty" doc:"The system prompt does not say how to format a reply, leaving it to the agent's own: it asks neither for Markdown nor, with plain text, for plain text. What the model writes is still read as Markdown unless plain text is on."`
	Context       *int         `json:"context,omitempty" minimum:"0" default:"30" doc:"How many recent chat messages the agent sees; 0 for none."`
	MaxTurns      int          `json:"max_turns,omitempty" minimum:"1" default:"40" doc:"How many turns of a thread are sent, newest kept."`
	Tools         []string     `json:"tools,omitempty" enum:"web_search,url_context,code_execution" doc:"Built-in tools the agent uses everywhere: web_search, the provider's search (cited Google Search on Gemini, multi-step on DeepSeek); url_context reads linked pages (Gemini, DeepSeek); code_execution runs code (Gemini). Module tools: Tools, below."`
	MaxToolRounds int          `json:"max_tool_rounds,omitempty" minimum:"1" default:"8" doc:"The most rounds of tool calls in one reply."`
	Judge         *JudgeConfig `json:"judge,omitempty" doc:"Has the decision service judge, in groups, the messages no keyword, reply, or mention triggers. Needs the decision module."`
	Media         *MediaConfig `json:"media,omitempty" doc:"Use the media module's descriptions: in the recent messages, the quoted message, and the wake judgment, and, with as_text, in place of the current message's media. Needs the media module, with the agent's chats on its describe list."`
	Notes         *NotesConfig `json:"notes,omitempty" doc:"A note ending each turn the model answers, by what started the turn, in that request only. A follow-up and a chime have one by default; other turns none."`
	Silent        bool         `json:"silent,omitempty" doc:"The agent may end any turn without replying: it has a tool, stay_silent, to send nothing. When to, or not to, is for its prompt and notes to say: with nothing saying so, the model decides on every turn, when called too."`
}

// NotesConfig is the note ending a turn, by what started it: the triggers
// chat.reply records. Follow and Chime default to followNote and chimeNote:
// unset is the default, and set, empty included, is what is set.
type NotesConfig struct {
	Mention  string  `json:"mention,omitempty" doc:"A message mentioning the account."`
	Reply    string  `json:"reply,omitempty" doc:"A reply to the agent's message."`
	Keyword  string  `json:"keyword,omitempty" doc:"A message starting with the agent's keyword, private chats included."`
	Decision string  `json:"decision,omitempty" doc:"A group message the judgment found addressed to the agent (judge.wake)."`
	Follow   *string `json:"follow,omitempty" default:"(Nobody named you: someone is carrying on the exchange with you. Keep it short.)" doc:"A follow-up (judge.follow)."`
	Chime    *string `json:"chime,omitempty" default:"(Nobody asked you; you chose to join in. Keep it short.)" doc:"Speaking up unasked (judge.chime)."`
}

// MediaConfig has an agent read the media module's descriptions.
type MediaConfig struct {
	AsText   bool     `json:"as_text,omitempty" doc:"Give the model descriptions instead of the current and the quoted message's media, for a model that sees images poorly or not at all. Media left undescribed is named by its kind, not shown."`
	Stickers []int    `json:"stickers,omitempty" format:"sticker" doc:"Stickers, by number, it may send with send_sticker after its reply, one at most. With packs, these come first in its list. A kept sticker named by number can be sent too. Verified on QQ only."`
	Packs    []string `json:"packs,omitempty" format:"sticker-pack" doc:"Sticker packs, by ID, whose stickers it may send, each pack's by number after stickers; a sticker put in a pack is in its list from the next reply on. A pack not there is skipped."`
	// MaxStickers bounds the list an agent is shown before each reply.
	MaxStickers *int           `json:"max_stickers,omitempty" minimum:"1" default:"50" doc:"The most stickers its list shows: past it, those first in it, stickers before packs, and chat.reply records how many were left out (stickers_hidden). The list is about fifty characters a sticker, in the cached part of the request after the system prompt, paid in full once after each change to it."`
	Examine     *ExamineConfig `json:"examine,omitempty" doc:"Give the agent examine_media, to ask a question of its own about a picture, animation, or video in the chat, which the descriptions it is shown then name by ID (#2ce7ad), or about a kept sticker by its number: the media module's models answer from the media itself. The original is the one the media module keeps (its pictures), else fetched again, which fails once the platform's link expired, on QQ in about an hour. Judgments and the memory are shown no IDs."`
	Resend      bool           `json:"resend,omitempty" doc:"Give the agent resend_media, to send a picture or animation of this chat again, by the ID its description shows (#2ce7ad), as a message of its own after its reply, when someone asks for it: one picture or sticker a reply. Only what the reply was shown can be sent, and only in its chat. The original is the media module's when it keeps it (its pictures), else fetched again, which fails once the platform's link expired, on QQ in about an hour. Videos and kept stickers are not resent."`
}

// ExamineConfig gives an agent examine_media.
type ExamineConfig struct {
	Timeout otogi.Duration `json:"timeout,omitempty" default:"1m" doc:"The longest one look may take, fetching the original and every model tried included; past it, the agent is told it took too long, and answers from the description. On Eku, Gemini Flash-Lite took 5.5 seconds at the median to describe a picture, and 33 at most."`
}

// defaultExamineTimeout bounds one look an examine block leaves unbounded.
const defaultExamineTimeout = time.Minute

// defaultMaxStickers is the most stickers an agent's list shows unless set:
// fifty lines, about two thousand characters. Eku's list on dsh was 27.
const defaultMaxStickers = 50

// packID is what a sticker pack's ID matches, as the media module checks.
var packID = regexp.MustCompile(mediasvc.PackIDPattern)

// JudgeConfig has the decision service judge group messages for an agent.
// Each block turns one judgment on.
type JudgeConfig struct {
	Rules  string        `json:"rules,omitempty" format:"text" doc:"What else counts, or does not, as talking to the agent: for example that its name is also a product people discuss."`
	Wake   *WakeConfig   `json:"wake,omitempty" doc:"Answer group messages addressed to the agent that no trigger caught, such as its name in the middle of a sentence."`
	Follow *FollowConfig `json:"follow,omitempty" doc:"Keep up an exchange the agent is in: a group message carrying on what the agent said in the last ten messages is answered, though nobody names it. Reactions that need no reply are not."`
	Wait   *WaitConfig   `json:"wait,omitempty" doc:"In groups, wait until the sender has finished, and answer what was said to the agent meanwhile together. A wait holds one of the process's workers, at most one per agent and group."`
	Brief  *BriefConfig  `json:"brief,omitempty" doc:"Before each reply, in any chat, ask whether the agent should look things up online before answering; when it should, the turn tells it to. Needs web_search or url_context in its tools."`
	Chime  *ChimeConfig  `json:"chime,omitempty" doc:"Speak up unasked: in a group where the agent is not in the conversation, a message it would like to add to is answered once the chat pauses, up to 2 minutes later. Needs wait."`
	Stale  *StaleConfig  `json:"stale,omitempty" doc:"In groups, before a reply's first message goes out, when someone else spoke after the agent started it: ask whether whoever asked took the question back, or someone else already answered it, and if so do not send it. The agent then does not stream in groups, so nothing shows before the check."`
}

// StaleConfig configures the check before a group reply goes out.
type StaleConfig struct {
	Threshold float64 `json:"threshold,omitempty" minimum:"0" maximum:"1" default:"0.8" doc:"How sure the judgment must be, of either question, for the reply not to be sent."`
}

// ChimeConfig configures speaking up unasked.
type ChimeConfig struct {
	Threshold float64 `json:"threshold,omitempty" minimum:"0" maximum:"1" default:"0.55" doc:"How sure the judgment must be that the agent would like to add to a message. It rises with how often the agent has spoken up unasked, as judge.follow's does."`
	Still     float64 `json:"still,omitempty" minimum:"0" maximum:"1" default:"0.5" doc:"While the agent waits for the chat to pause before speaking up, each other message asks whether it still means to: how sure the judgment must be that it does, or it gives up. Lower keeps it waiting through more of the chat."`
}

// BriefConfig configures the judgment asked before each reply.
type BriefConfig struct {
	Lookup float64 `json:"lookup,omitempty" minimum:"0" maximum:"1" default:"0.7" doc:"How sure the judgment must be that the agent should look things up before answering."`
}

// WaitConfig configures waiting for the sender to finish before answering.
type WaitConfig struct {
	Min otogi.Duration `json:"min,omitempty" default:"2s" doc:"How long to wait when the sender has finished. Messages arriving this close together are also judged in one request."`
	Max otogi.Duration `json:"max,omitempty" default:"20s" doc:"The longest wait, from the first message. At most half of timeout, which also bounds the reply."`
}

// The wait of a wait block that sets none.
const (
	defaultWaitMin = 2 * time.Second
	defaultWaitMax = 20 * time.Second
)

// WakeConfig configures answering messages judged to be addressed to the
// agent.
type WakeConfig struct {
	Threshold float64 `json:"threshold,omitempty" minimum:"0" maximum:"1" default:"0.7" doc:"How sure the judgment must be that a message is addressed to the agent. When several agents pass, the surest answers."`
}

// FollowConfig configures answering messages judged to carry on an exchange
// with the agent.
type FollowConfig struct {
	Threshold float64 `json:"threshold,omitempty" minimum:"0" maximum:"1" default:"0.6" doc:"How sure the judgment must be that a message carries on the exchange with the agent. Where the agent has followed up more than 1.3 times as often as the middle member of the conversation has spoken, it rises by 0.1 per time more, up to 0.8; replies the agent was called for do not count."`
}

const (
	// defaultWake is the wake threshold of a wake block that sets none.
	defaultWake = 0.7
	// defaultFollow is the follow threshold of a follow block that sets none.
	defaultFollow = 0.6
	// defaultChime is the chime threshold of a chime block that sets none:
	// Eku's live value (2026-11-25).
	defaultChime = 0.55
	// defaultStill is the still threshold of a chime block that sets none:
	// the default of Eku's code, whose owner runs it at 0.4.
	defaultStill = 0.5
	// defaultLookup is the lookup threshold of a brief block that sets none:
	// Eku's, live since 2026-11-18.
	defaultLookup = 0.7
	// defaultStale is the threshold of a stale block that sets none: Eku's
	// (2026-11-26), high so that a reply is rather sent than lost.
	defaultStale = 0.8
)

// profile is a validated agent.
type profile struct {
	ProfileConfig
	keywords []string
	context  int
	builtins []llm.ToolSpec
	provider llm.Provider
	wake     float64 // how sure a judgment must be to wake it; 0 when it is not judged
	follow   float64 // how sure a judgment must be that a message carries on its exchange; 0 when not judged
	chime    float64 // how sure a judgment must be that it would like to speak up; 0 when not judged
	still    float64 // how sure a judgment must be, while it waits to speak up, that it still means to
	// chimeMax is the longest it waits for a chat to pause before speaking
	// up: chimeHoldMax, or half of timeout, which also bounds the reply.
	chimeMax time.Duration
	rules    string  // what else counts as talking to it, for judgments
	lookup   float64 // how sure a judgment must be that it should look things up first; 0 when not asked
	stale    float64 // how sure the check before a group reply goes out must be to hold it back; 0 when not checked
	// waitMin and waitMax bound how long it waits in groups for the sender
	// to finish; waitMax is 0 when it does not wait.
	waitMin, waitMax time.Duration
	examine          time.Duration // the longest one look with examine_media may take; 0 when it has none
	resend           bool          // whether it may send a picture of the chat again with resend_media
	maxStickers      int           // the most stickers the agent's list shows
	sources          bool          // whether a grounded reply ends with its sources
}

// title is what chats know the agent by: its label, or its name.
func (p *profile) title() string { return cmp.Or(p.Label, p.Name) }

// ids reports whether the descriptions p is shown name their media by ID,
// for examine_media and resend_media.
func (p *profile) ids() bool { return p.examine > 0 || p.resend }

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

// DescribeWait is how long a message waits for its media's descriptions.
type DescribeWait struct {
	Judge otogi.Duration `json:"judge,omitempty" default:"15s" doc:"Before it is judged, at most, from its arrival. Then it is judged with what is described."`
	Reply otogi.Duration `json:"reply,omitempty" default:"30s" doc:"Before an agent answers it, at most, from its arrival; unset, 30s or half of timeout, whichever is less. At most half of timeout, which also bounds the reply."`
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
	if c.DescribedText == 0 {
		c.DescribedText = 1000
	}
	if c.DescribeWait.Judge == 0 {
		c.DescribeWait.Judge = otogi.Duration(15 * time.Second)
	}
	if c.DescribeWait.Reply == 0 {
		// Within the check below, so that settings saved before it, with a
		// short timeout, still hold.
		c.DescribeWait.Reply = min(otogi.Duration(30*time.Second), c.Timeout/2)
	}
	var errs []error
	if c.Retention < 0 || c.Timeout < 0 {
		errs = append(errs, errors.New("retention and timeout must not be negative"))
	}
	switch {
	case c.DescribeWait.Judge < 0:
		errs = append(errs, &otogi.SettingError{Path: "describe_wait.judge", Err: errors.New("must not be negative")})
	case c.DescribeWait.Reply < 0:
		errs = append(errs, &otogi.SettingError{Path: "describe_wait.reply", Err: errors.New("must not be negative")})
	case c.DescribeWait.Reply > c.Timeout/2:
		errs = append(errs, &otogi.SettingError{Path: "describe_wait.reply", Err: fmt.Errorf("%s: at most half of timeout (%s), which also bounds the reply", time.Duration(c.DescribeWait.Reply), time.Duration(c.Timeout))})
	}
	if len(c.Profiles) == 0 {
		errs = append(errs, &otogi.SettingError{Path: "profiles", Err: errors.New("at least one is required")})
	}
	var out []*profile
	names, keywords := map[string]bool{}, map[string]string{}
	for i, pc := range c.Profiles {
		field := fmt.Sprintf("profiles[%d]", i)
		refuse := func(path string, err error) { errs = append(errs, &otogi.SettingError{Path: field + path, Err: err}) }
		p := &profile{ProfileConfig: pc, context: 30, sources: pc.Sources == nil || *pc.Sources}
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
			for j, id := range pc.Media.Packs {
				if !packID.MatchString(id) {
					refuse(fmt.Sprintf(".media.packs[%d]", j), fmt.Errorf("%q: want a sticker pack's ID: lowercase letters, digits, and hyphens, starting with a letter or digit, at most 32", id))
				}
			}
			p.maxStickers = defaultMaxStickers
			if n := pc.Media.MaxStickers; n != nil {
				if p.maxStickers = *n; p.maxStickers < 1 {
					refuse(".media.max_stickers", errors.New("must be at least 1"))
				}
			}
			if pc.Media.Examine != nil {
				if p.examine = cmp.Or(time.Duration(pc.Media.Examine.Timeout), defaultExamineTimeout); p.examine < 0 {
					refuse(".media.examine.timeout", errors.New("must not be negative"))
				}
			}
			p.resend = pc.Media.Resend
		}
		if pc.Judge != nil {
			p.rules = strings.TrimSpace(pc.Judge.Rules)
			if pc.Judge.Wake != nil {
				p.wake = cmp.Or(pc.Judge.Wake.Threshold, defaultWake)
			}
			if pc.Judge.Follow != nil {
				p.follow = cmp.Or(pc.Judge.Follow.Threshold, defaultFollow)
			}
			if pc.Judge.Chime != nil {
				p.chime = cmp.Or(pc.Judge.Chime.Threshold, defaultChime)
				p.still = cmp.Or(pc.Judge.Chime.Still, defaultStill)
			}
			if pc.Judge.Stale != nil {
				p.stale = cmp.Or(pc.Judge.Stale.Threshold, defaultStale)
			}
			if pc.Judge.Brief != nil {
				p.lookup = cmp.Or(pc.Judge.Brief.Lookup, defaultLookup)
				if !slices.ContainsFunc(p.builtins, func(t llm.ToolSpec) bool {
					return t.Builtin == llm.BuiltinWebSearch || t.Builtin == llm.BuiltinURLContext
				}) {
					refuse(".judge.brief", errors.New("needs web_search or url_context in tools, to look things up with"))
				}
			}
			if w := pc.Judge.Wait; w != nil {
				p.waitMin, p.waitMax = cmp.Or(time.Duration(w.Min), defaultWaitMin), cmp.Or(time.Duration(w.Max), defaultWaitMax)
				switch {
				case p.waitMin < 0:
					refuse(".judge.wait.min", errors.New("must not be negative"))
				case p.waitMax < 0:
					refuse(".judge.wait.max", errors.New("must not be negative"))
				case p.waitMin > p.waitMax:
					refuse(".judge.wait.min", fmt.Errorf("%s is longer than max (%s)", p.waitMin, p.waitMax))
				case p.waitMax > time.Duration(c.Timeout)/2:
					refuse(".judge.wait.max", fmt.Errorf("%s: at most half of timeout (%s), which also bounds the reply", p.waitMax, time.Duration(c.Timeout)))
				}
			}
			if p.chime > 0 {
				p.chimeMax = min(chimeHoldMax, time.Duration(c.Timeout)/2)
				if p.waitMax == 0 {
					refuse(".judge.chime", errors.New("needs judge.wait, to wait for the chat to pause"))
				}
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
