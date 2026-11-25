package chat

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	decisionmodule "github.com/TundraWork/Ex-Otogi/modules/decision"
	"github.com/TundraWork/Ex-Otogi/pkg/decision"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// replayLine is one message of a replayed chat.
type replayLine struct {
	From      string `json:"from"`
	Text      string `json:"text"`
	At        int64  `json:"at,omitempty"`        // when it was sent, in unix milliseconds; without it, a minute after the last
	Agent     bool   `json:"agent,omitempty"`     // the agent's own message
	Kind      string `json:"kind,omitempty"`      // for the agent's: "chime" when it spoke up unasked, else it was called
	Addressed *bool  `json:"addressed,omitempty"` // known to be to the agent, or known not to be
}

// replayAt sets the harness clock so that the next message is sent at l's
// time, keeping messages in order.
func (h *harness) replayAt(l replayLine) {
	if l.At == 0 {
		return
	}
	at := time.UnixMilli(l.At)
	if last := h.now; !at.After(last) {
		at = last.Add(time.Millisecond)
	}
	h.now = at.Add(-time.Minute) // message adds the minute back
}

// decisionHost is enough of a host for the decision module.
type decisionHost struct {
	otogi.Host
	services map[string]any
	records  []otogi.Record
}

func (h *decisionHost) Provide(name string, v any) error { h.services[name] = v; return nil }
func (h *decisionHost) Logger() *slog.Logger             { return slog.Default() }
func (h *decisionHost) Record(_ context.Context, r otogi.Record) {
	h.records = append(h.records, r)
}

// TestReplayWake replays a real group chat through judge.wake with the real
// decision service, message by message, and prints the score each message
// got, then how many messages each threshold would wake. Real chats stay out
// of the repository: it runs only when OTOGI_CHAT_REPLAY names a local JSON
// Lines file of replayLine, and OTOGI_TYPESAFE_API_KEY is set.
//
//	OTOGI_CHAT_REPLAY=chat.jsonl OTOGI_CHAT_REPLAY_AGENT=Mizuki OTOGI_TYPESAFE_API_KEY=... go test -run Replay -v ./modules/chat
//
// OTOGI_CHAT_REPLAY_AGENT is the agent's name and keyword (default luna);
// OTOGI_CHAT_REPLAY_RULES, its judge.rules. Messages starting with the name
// are judged too, as in a group with judge.wake on.
func TestReplayWake(t *testing.T) {
	file := os.Getenv("OTOGI_CHAT_REPLAY")
	if file == "" || os.Getenv("OTOGI_TYPESAFE_API_KEY") == "" {
		t.Skip("OTOGI_CHAT_REPLAY or OTOGI_TYPESAFE_API_KEY is not set")
	}
	lines := readReplay(t, file)
	dm, err := decisionmodule.New(decisionmodule.Config{Jev: &decisionmodule.JevConfig{APIKey: "${OTOGI_TYPESAFE_API_KEY}", Timeout: otogi.Duration(30 * time.Second)}}, os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	dh := &decisionHost{services: map[string]any{}}
	if err := dm.Start(context.Background(), dh); err != nil {
		t.Fatal(err)
	}
	name := cmp.Or(os.Getenv("OTOGI_CHAT_REPLAY_AGENT"), "luna")
	// A threshold no score reaches: every message is judged, none answered,
	// so the chat goes on as it did.
	agent := ProfileConfig{Name: name, Keywords: []string{name}, Provider: "replay", Model: "m", Judge: &JudgeConfig{Rules: os.Getenv("OTOGI_CHAT_REPLAY_RULES"), Wake: &WakeConfig{Threshold: 1.01}}}
	h := newHarnessWith(t, dh.services, agent)
	h.llm["replay"].responses = []llm.Response{answer("…")}

	type scored struct {
		score     float64
		addressed *bool
		text      string
	}
	var got []scored
	for i, l := range lines {
		if l.Agent {
			h.replayAgent(t, name, replayCause(l), l.Text, i)
			continue
		}
		before := len(dh.records)
		h.mustHandle(h.message(l.From, l.Text))
		if len(dh.records) == before {
			continue // triggered, not judged
		}
		r := dh.records[len(dh.records)-1]
		answers, _ := r.Payload.(map[string]any)["answers"].(decision.Answers)
		s := -1.0
		for _, v := range answers {
			s = v
		}
		got = append(got, scored{score: s, addressed: l.Addressed, text: l.Text})
		mark := " "
		if l.Addressed != nil {
			mark = map[bool]string{true: "+", false: "-"}[*l.Addressed]
		}
		t.Logf("%s %.2f %s: %s", mark, s, l.From, oneLine(l.Text))
	}
	for _, threshold := range []float64{0.5, 0.6, 0.7, 0.8, 0.9} {
		count := func(keep func(scored) bool) (woke, all int) {
			for _, g := range got {
				if keep(g) {
					all++
					if g.score >= threshold {
						woke++
					}
				}
			}
			return woke, all
		}
		yes, yesAll := count(func(g scored) bool { return g.addressed != nil && *g.addressed })
		no, noAll := count(func(g scored) bool { return g.addressed != nil && !*g.addressed })
		rest, restAll := count(func(g scored) bool { return g.addressed == nil })
		t.Logf("threshold %.1f: addressed %d/%d, not addressed %d/%d, unlabeled %d/%d", threshold, yes, yesAll, no, noAll, rest, restAll)
	}
}

// TestReplayFollow replays a real group chat through judge.follow with the
// real decision service. For each message the agent's recent line makes a
// follow-up candidate, it prints the follow score, the agent's share of the
// conversation, and the threshold the share sets; then, per base threshold,
// how many follow-ups would be answered and how many the share held back,
// and how many of those the agent did answer in the real chat. The agent's
// own lines are the chat's (agent: true; kind "chime" when it spoke up
// unasked), so its share is the one it had. Lines need "at" for the
// conversation gaps, and a line that @-mentioned the agent (addressed: true)
// is a mention, not a candidate. Like TestReplayWake, it runs only with
// OTOGI_CHAT_REPLAY and OTOGI_TYPESAFE_API_KEY.
func TestReplayFollow(t *testing.T) {
	file := os.Getenv("OTOGI_CHAT_REPLAY")
	if file == "" || os.Getenv("OTOGI_TYPESAFE_API_KEY") == "" {
		t.Skip("OTOGI_CHAT_REPLAY or OTOGI_TYPESAFE_API_KEY is not set")
	}
	lines := readReplay(t, file)
	dm, err := decisionmodule.New(decisionmodule.Config{Jev: &decisionmodule.JevConfig{APIKey: "${OTOGI_TYPESAFE_API_KEY}", Timeout: otogi.Duration(30 * time.Second)}}, os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	dh := &decisionHost{services: map[string]any{}}
	if err := dm.Start(context.Background(), dh); err != nil {
		t.Fatal(err)
	}
	name := cmp.Or(os.Getenv("OTOGI_CHAT_REPLAY_AGENT"), "luna")
	// A threshold no score reaches: every candidate is judged, none
	// answered, so the chat and the agent's share go on as they did.
	agent := ProfileConfig{Name: name, Keywords: []string{name}, Provider: "replay", Model: "m",
		Judge: &JudgeConfig{Rules: os.Getenv("OTOGI_CHAT_REPLAY_RULES"), Follow: &FollowConfig{Threshold: 1.01}}}
	h := newHarnessWith(t, dh.services, agent)
	h.llm["replay"].responses = []llm.Response{answer("…")}
	p := h.m.profiles[0]

	type scored struct {
		score, share float64
		answered     bool // the agent spoke next in the real chat
	}
	var got []scored
	for i, l := range lines {
		h.replayAt(l)
		if l.Agent {
			h.replayAgent(t, name, replayCause(l), l.Text, i)
			continue
		}
		before := len(dh.records)
		e := h.message(l.From, l.Text)
		h.mustHandle(e)
		if len(dh.records) == before {
			continue // triggered, or not a follow-up candidate
		}
		if l.Addressed != nil && *l.Addressed {
			continue // the real chat @-mentioned the agent: a mention, not a follow-up
		}
		answers, _ := dh.records[len(dh.records)-1].Payload.(map[string]any)["answers"].(decision.Answers)
		s := -1.0
		for id, v := range answers {
			if strings.HasPrefix(id, "follow.") {
				s = v
			}
		}
		share, err := h.m.share(context.Background(), e, p)
		if err != nil {
			t.Fatal(err)
		}
		answered := i+1 < len(lines) && lines[i+1].Agent
		got = append(got, scored{score: s, share: share, answered: answered})
		mark := map[bool]string{true: "*", false: " "}[answered]
		t.Logf("%s %.2f share %.2f: %s: %s", mark, s, share, l.From, oneLine(l.Text))
	}
	for _, base := range []float64{0.5, 0.6} {
		var pass, held, passAnswered, heldAnswered int
		for _, g := range got {
			switch {
			case g.score >= followThreshold(base, g.share):
				pass++
				if g.answered {
					passAnswered++
				}
			case g.score >= base:
				held++
				if g.answered {
					heldAnswered++
				}
			}
		}
		t.Logf("base %.1f: %d candidates, %d answered (%d of them answered in the chat), %d held back by the share (%d of them answered in the chat)",
			base, len(got), pass, passAnswered, held, heldAnswered)
	}
}

func readReplay(t *testing.T, file string) []replayLine {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out []replayLine
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var l replayLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		out = append(out, l)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// replayAgent puts the agent's own message in the transcript, as sent by it.
func (h *harness) replayAgent(t *testing.T, name, cause, text string, i int) {
	t.Helper()
	source := fmt.Sprintf("replay-agent-%d", i)
	h.mustHandle(h.message("bot", text, func(e *otogi.Event) { e.Direction, e.SourceID = otogi.DirectionOutgoing, otogi.SourceEventID(source) }))
	ref := otogi.EventRef{Platform: "telegram", Account: "main", Conversation: "chat:1", SourceEvent: otogi.SourceEventID(source)}
	turn := []storedTurn{{Turn: llm.Turn{Role: llm.RoleUser, Content: otogi.TextMessage("(replayed)")}}}
	if _, err := h.m.store.thread(context.Background(), name, cause, otogi.Destination{Platform: "telegram", Account: "main", Conversation: "chat:1"}, 0, turn, []sentMessage{{Ref: ref}}, h.now); err != nil {
		t.Fatal(err)
	}
}

// replayCause is the cause a replayed agent line is kept with: eku logged
// what it said unasked as "chime", and those were its follow-ups.
func replayCause(l replayLine) string {
	if l.Kind == "chime" {
		return "follow"
	}
	return "mention"
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 80 {
		return string(r[:80]) + "…"
	}
	return s
}
