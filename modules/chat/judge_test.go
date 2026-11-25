package chat

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/TundraWork/Ex-Otogi/pkg/decision"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// decider answers every wake question with its score and every follow
// question with its follow score, by the agent it asks about (a1, a2, …),
// and keeps the requests. answer, when set, answers a question first (ok
// false leaves it to the scores); before, when set, runs before a request is
// answered, and may block it.
type decider struct {
	mu       sync.Mutex
	scores   map[string]float64
	follow   map[string]float64
	answer   func(id string, req decision.Request) (float64, bool)
	before   func(req decision.Request)
	err      error
	requests []decision.Request
}

func (d *decider) Decide(_ context.Context, req decision.Request) (decision.Answers, error) {
	if d.before != nil {
		d.before(req)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.requests = append(d.requests, req)
	if d.err != nil {
		return nil, d.err
	}
	out := decision.Answers{}
	for id := range req.Questions {
		if d.answer != nil {
			if s, ok := d.answer(id, req); ok {
				out[id] = s
				continue
			}
		}
		for agent, s := range d.scores {
			if strings.HasPrefix(id, "wake."+agent+".") {
				out[id] = s
			}
		}
		for agent, s := range d.follow {
			if strings.HasPrefix(id, "follow."+agent+".") {
				out[id] = s
			}
		}
	}
	return out, nil
}

// asked is the text of the message a question is about: the last part of its
// ID names it in the request's history.
func asked(id string, req decision.Request) string {
	msg := id[strings.LastIndex(id, ".")+1:]
	var history []judgeLine
	switch s := req.State.(type) {
	case judgeState:
		history = s.History
	case briefState:
		history = s.History
	}
	for _, l := range history {
		if l.ID == msg {
			return l.Text
		}
	}
	return ""
}

func judgedProfile(name string, threshold float64) ProfileConfig {
	return ProfileConfig{Name: name, Keywords: []string{name}, Provider: name, Model: "m", Judge: &JudgeConfig{Rules: name + " is also a game.", Wake: &WakeConfig{Threshold: threshold}}}
}

func newJudged(t *testing.T, d *decider, profiles ...ProfileConfig) *harness {
	t.Helper()
	return newHarnessWith(t, map[string]any{decision.Service: d}, profiles...)
}

func lastReply(h *harness) map[string]any {
	h.t.Helper()
	for i := len(h.host.records) - 1; i >= 0; i-- {
		if h.host.records[i].Kind == "chat.reply" {
			return h.host.records[i].Payload.(map[string]any)
		}
	}
	return nil
}

// A group message no trigger caught is answered when the judgment says it is
// addressed to the agent, and the reply records the judgment.
func TestWakeByJudgment(t *testing.T) {
	d := &decider{scores: map[string]float64{"a1": 0.9}}
	h := newJudged(t, d, judgedProfile("luna", 0.7))
	h.mustHandle(h.message("Alice", "what do you think, luna?"))
	if len(d.requests) != 1 || len(h.llm["luna"].requests) != 1 {
		t.Fatalf("decision requests %d, replies %d", len(d.requests), len(h.llm["luna"].requests))
	}
	req := d.requests[0]
	state := req.State.(judgeState)
	if !slices.Equal(state.Candidates, []string{"m1"}) || state.History[0].From != "u1 Alice" || state.Agents[0].Rules != "luna is also a game." {
		t.Fatalf("state %+v", state)
	}
	if _, ok := req.Questions["wake.a1.m1"]; !ok || len(req.Questions) != 1 {
		t.Fatalf("questions %v", req.Questions)
	}
	rec := lastReply(h)
	if rec["trigger"] != "decision" || *rec["judged"].(*judgment) != (judgment{Score: 0.9, Threshold: 0.7}) {
		t.Fatalf("record %+v", rec)
	}

	d.scores["a1"] = 0.6
	h.mustHandle(h.message("Alice", "the game luna is fun"))
	if len(d.requests) != 2 || len(h.llm["luna"].requests) != 1 {
		t.Fatal("a judgment under the threshold woke the agent")
	}
}

// Messages a trigger catches, replies to another module's messages, private
// chats, and agents not judged or not on the chat's allowlist ask nothing.
func TestWhatIsNotJudged(t *testing.T) {
	d := &decider{scores: map[string]float64{"a1": 0.9, "a2": 0.9}}
	plain := ProfileConfig{Name: "otogi", Keywords: []string{"otogi"}, Provider: "otogi", Model: "m"}
	away := judgedProfile("mizuki", 0.5)
	h := newJudged(t, d, judgedProfile("luna", 0.7), plain, away)
	delete(h.host.lists, [2]string{"mizuki", group})

	h.mustHandle(h.message("Alice", "otogi, hello"))
	h.mustHandle(h.message("Alice", "hi there", func(e *otogi.Event) { e.Conversation.Kind = otogi.ConversationDirect }))
	if len(d.requests) != 0 {
		t.Fatalf("asked %d times", len(d.requests))
	}
	// A reply to a message the account sent for another module, such as a
	// card, addresses no agent.
	card := h.message("bot", "a card", func(e *otogi.Event) { e.Direction, e.SourceID = otogi.DirectionOutgoing, "card-1" })
	h.mustHandle(card)
	h.mustHandle(h.message("Alice", "what is this video?", replyingTo("card-1")))
	if len(d.requests) != 0 {
		t.Fatalf("asked %d times", len(d.requests))
	}
	h.mustHandle(h.message("Alice", "anyone?"))
	if len(d.requests) != 1 {
		t.Fatalf("asked %d times", len(d.requests))
	}
	state := d.requests[0].State.(judgeState)
	if len(state.Agents) != 2 || state.Agents[1].Name != "otogi" || len(d.requests[0].Questions) != 1 {
		t.Fatalf("agents %+v, questions %v", state.Agents, d.requests[0].Questions)
	}
}

// In a group, a message starting with a judged agent's name is judged, not
// triggered; a mention still triggers with the name picking the agent; when
// the judgment fails, the name triggers the agent; private chats keep the
// keyword; a reply to a card that starts with the name is judged too.
func TestNameIsJudged(t *testing.T) {
	d := &decider{scores: map[string]float64{"a1": 0.2}}
	h := newJudged(t, d, judgedProfile("luna", 0.7))
	luna := h.llm["luna"]

	h.mustHandle(h.message("Alice", "luna wrote that script, not me"))
	if len(d.requests) != 1 || len(luna.requests) != 0 {
		t.Fatalf("decision requests %d, replies %d", len(d.requests), len(luna.requests))
	}
	d.scores["a1"] = 0.9
	h.mustHandle(h.message("Alice", "luna, what do you think?"))
	if len(d.requests) != 2 || len(luna.requests) != 1 || lastReply(h)["trigger"] != "decision" {
		t.Fatalf("decision requests %d, replies %d, reply %v", len(d.requests), len(luna.requests), lastReply(h))
	}

	d.err = errors.New("unavailable")
	h.mustHandle(h.message("Alice", "luna, are you there?"))
	if len(luna.requests) != 2 || lastReply(h)["trigger"] != "keyword" {
		t.Fatalf("a failed judgment: replies %d, reply %v", len(luna.requests), lastReply(h))
	}
	h.mustHandle(h.message("Alice", "is anyone there?"))
	if len(luna.requests) != 2 {
		t.Fatal("a failed judgment woke an agent the message does not name")
	}
	d.err = nil

	asked := len(d.requests)
	h.mustHandle(h.message("Alice", "luna hi", func(e *otogi.Event) { e.Mentioned = true }))
	h.mustHandle(h.message("Alice", "luna hi", func(e *otogi.Event) { e.Conversation.Kind = otogi.ConversationDirect }))
	if len(d.requests) != asked || len(luna.requests) != 4 {
		t.Fatalf("a mention or a private chat was judged: asked %d more, replies %d", len(d.requests)-asked, len(luna.requests))
	}

	card := h.message("bot", "a card", func(e *otogi.Event) { e.Direction, e.SourceID = otogi.DirectionOutgoing, "card-1" })
	h.mustHandle(card)
	h.mustHandle(h.message("Alice", "luna, what is this video?", replyingTo("card-1")))
	if len(d.requests) != asked+1 {
		t.Fatalf("a reply to a card starting with the name: asked %d more", len(d.requests)-asked)
	}
}

// When several agents pass their thresholds, the surest answers; a failed
// request is no decision.
func TestSurestAgentAndFailedJudgment(t *testing.T) {
	d := &decider{scores: map[string]float64{"a1": 0.8, "a2": 0.95}}
	h := newJudged(t, d, judgedProfile("luna", 0.7), judgedProfile("mizuki", 0.9))
	h.mustHandle(h.message("Alice", "you two, which is better?"))
	if len(h.llm["luna"].requests) != 0 || len(h.llm["mizuki"].requests) != 1 {
		t.Fatalf("luna %d, mizuki %d", len(h.llm["luna"].requests), len(h.llm["mizuki"].requests))
	}

	d.err = errors.New("unavailable")
	if err := h.handle(h.message("Alice", "and now?")); err != nil {
		t.Fatal(err)
	}
	if len(h.llm["luna"].requests) != 0 || len(h.llm["mizuki"].requests) != 1 {
		t.Fatal("a failed judgment woke an agent")
	}
}

// The judgment is told it is a group chat and sees the last messages
// numbered, the agent's own named, a reply to a message in view by number and
// to an older one quoted, and each message but the judged one cut short.
func TestJudgeState(t *testing.T) {
	d := &decider{scores: map[string]float64{"a1": 0.1}} // nothing wakes
	h := newJudged(t, d, judgedProfile("luna", 0.7))
	long := strings.Repeat("长", 200)
	old := h.message("Bob", long)
	h.mustHandle(old)
	for range judgeHistory {
		h.mustHandle(h.message("Bob", "filler"))
	}
	d.scores["a1"] = 0.9
	h.mustHandle(h.message("Alice", "luna, look")) // judged addressed: luna answers, as agent-1
	d.scores["a1"] = 0.1
	h.mustHandle(h.message("bot", "a cat", func(e *otogi.Event) { e.Direction, e.SourceID = otogi.DirectionOutgoing, "agent-1" }))
	shown := h.message("Carol", long)
	h.mustHandle(shown)
	h.mustHandle(h.message("Alice", long, replyingTo(string(old.SourceID))))

	state := d.requests[len(d.requests)-1].State.(judgeState)
	from := func(l judgeLine) string { return l.From }
	if state.Chat != groupChat {
		t.Fatalf("chat %q", state.Chat)
	}
	if len(state.History) != judgeHistory || state.History[0].From != "u1 Bob" || !slices.Contains(sliceOf(state.History, from), "a1 luna") {
		t.Fatalf("history %+v", state.History)
	}
	n := len(state.History)
	judged, carol := state.History[n-1], state.History[n-2]
	if judged.Text != long || judged.From != "u2 Alice" || judged.ReplyTo != "" || !strings.HasPrefix(judged.ReplyingTo, "Bob: 长") || !strings.HasSuffix(judged.ReplyingTo, "…") {
		t.Fatalf("judged message %+v", judged)
	}
	if carol.From != "u3 Carol" || !strings.HasSuffix(carol.Text, "…") {
		t.Fatalf("earlier message %+v", carol)
	}

	// Numbers are per request: Carol's message has a new one now.
	h.mustHandle(h.message("Alice", "and this", replyingTo(string(shown.SourceID))))
	state = d.requests[len(d.requests)-1].State.(judgeState)
	got, carolNow := state.History[len(state.History)-1], state.History[len(state.History)-3]
	if carolNow.From != "u3 Carol" || got.ReplyTo != carolNow.ID || got.ReplyingTo != "" {
		t.Fatalf("reply %+v, to %+v", got, carolNow)
	}
}

func sliceOf[T, U any](in []T, f func(T) U) []U {
	out := make([]U, 0, len(in))
	for _, v := range in {
		out = append(out, f(v))
	}
	return out
}

// Only a judged agent makes the module require the decision service, and
// a wake block without a threshold gets the default the form shows.
func TestJudgedRequiresDecision(t *testing.T) {
	t.Parallel()
	providers := func(string) (llm.Provider, error) { return &provider{}, nil }
	plain, err := New(Config{Profiles: []ProfileConfig{{Name: "luna", Keywords: []string{"luna"}, Provider: "p", Model: "m"}}}, providers)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(plain.Descriptor().Requires, decision.Service) {
		t.Fatal("an unjudged chat requires the decision service")
	}
	judged, err := New(Config{Profiles: []ProfileConfig{{Name: "luna", Keywords: []string{"luna"}, Provider: "p", Model: "m", Judge: &JudgeConfig{Wake: &WakeConfig{}}}}}, providers)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(judged.Descriptor().Requires, decision.Service) || judged.profiles[0].wake != defaultWake {
		t.Fatalf("requires %v, threshold %v", judged.Descriptor().Requires, judged.profiles[0].wake)
	}
	field, _ := reflect.TypeFor[WakeConfig]().FieldByName("Threshold")
	if tag := field.Tag.Get("default"); tag != strconv.FormatFloat(defaultWake, 'g', -1, 64) {
		t.Fatalf("the form shows %s, an unset threshold is %v", tag, defaultWake)
	}
}
