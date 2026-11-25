package chat

import (
	"errors"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/decision"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// followProfile is an agent that keeps up its exchanges, and is woken by
// address too when wake is not zero.
func followProfile(name string, wake, follow float64) ProfileConfig {
	p := ProfileConfig{Name: name, Keywords: []string{name}, Provider: name, Model: "m", Judge: &JudgeConfig{Follow: &FollowConfig{Threshold: follow}}}
	if wake > 0 {
		p.Judge.Wake = &WakeConfig{Threshold: wake}
	}
	return p
}

// questionsOf names the questions of request i.
func questionsOf(d *decider, i int) []string {
	var out []string
	for id := range d.requests[i].Questions {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// An agent that spoke in the conversation is asked about each message that
// may carry on its exchange, from its point of view, and answers one that
// does, told why it speaks; one whose line is before a gap of over half an
// hour is not asked; an agent that has not spoken is not asked.
func TestFollowUp(t *testing.T) {
	d := &decider{follow: map[string]float64{"a1": 0.9}}
	h := newJudged(t, d, followProfile("luna", 0, 0.5))
	h.mustHandle(h.message("Alice", "anyone up?"))
	if len(d.requests) != 0 {
		t.Fatalf("asked %d times before luna spoke", len(d.requests))
	}
	h.replayAgent(t, "luna", "follow", "I am!", 1)
	h.mustHandle(h.message("Alice", "nice, what are you doing?"))
	if len(d.requests) != 1 || len(h.llm["luna"].requests) != 1 {
		t.Fatalf("decision requests %d, replies %d", len(d.requests), len(h.llm["luna"].requests))
	}
	if q := questionsOf(d, 0); !slices.Equal(q, []string{"follow.a1.m3"}) {
		t.Fatalf("questions %v", q)
	}
	q := d.requests[0].Questions["follow.a1.m3"]
	if !strings.Contains(q.Instructions, "You are agent a1") || !strings.Contains(q.Criteria.False, "A reaction that needs no reply") {
		t.Fatalf("question %+v", q)
	}
	state := d.requests[0].State.(judgeState)
	if state.History[1].From != "a1 luna" {
		t.Fatalf("state %+v", state)
	}
	if got := lastUserText(h.llm["luna"].requests[0]); !strings.HasSuffix(got, followNote) {
		t.Fatalf("the model was not told why it speaks: %q", got)
	}
	rec := lastReply(h)
	j := rec["judged"].(*judgment)
	if rec["trigger"] != "follow" || j.Score != 0.9 || j.Threshold != 0.5 || j.Share == nil || *j.Share != 0.5 {
		t.Fatalf("record %+v, judged %+v", rec, j)
	}

	h.now = h.now.Add(31 * time.Minute)
	h.mustHandle(h.message("Alice", "morning, everyone"))
	if len(d.requests) != 1 {
		t.Fatal("asked about a message after a long gap")
	}
}

// The follow threshold rises once the agent has spoken up unasked more than
// 1.3 times as often as the middle member, counting only members with two
// messages or more, and the agent's turns, not its messages. Replies it was
// called for do not count.
func TestFollowShare(t *testing.T) {
	d := &decider{follow: map[string]float64{"a1": 0.1}} // nothing follows up yet
	h := newJudged(t, d, followProfile("luna", 0, 0.5))
	h.mustHandle(h.message("Alice", "a"))
	h.replayAgent(t, "luna", "follow", "1", 1)
	h.mustHandle(h.message("Bob", "b"))
	h.replayAgent(t, "luna", "follow", "2", 2)
	h.replayAgent(t, "luna", "mention", "called", 5)
	h.replayAgent(t, "luna", "follow", "3", 3)
	h.mustHandle(h.message("Bob", "bb"))
	h.replayAgent(t, "luna", "decision", "called again", 6)
	h.replayAgent(t, "luna", "follow", "4", 4)
	h.mustHandle(h.message("Carol", "[sticker]"))
	// luna 4 unasked turns (the two it was called for do not count);
	// Alice 2, Bob 2 (Carol's one message does not count): share 2,
	// threshold 0.5 + 0.1 × 0.7 = 0.57.
	d.follow["a1"] = 0.56
	held := h.message("Alice", "aa")
	h.mustHandle(held)
	if len(h.llm["luna"].requests) != 0 {
		t.Fatalf("replies %d", len(h.llm["luna"].requests))
	}
	// Held back by the share alone, so it is recorded.
	r := h.host.records[len(h.host.records)-1]
	if p, _ := r.Payload.(map[string]any); r.Kind != "chat.held" || r.Outcome != "skipped" || r.Event != held.ID || p["profile"] != "luna" || p["trigger"] != "follow" ||
		p["score"] != 0.56 || p["base"] != 0.5 || math.Abs(p["threshold"].(float64)-0.57) > 1e-9 || p["share"] != 2.0 {
		t.Fatalf("held record %s %s %+v", r.Kind, r.Outcome, r.Payload)
	}
	// Alice now has 3: middle 2.5, share 1.6, threshold 0.53.
	d.follow["a1"] = 0.54
	h.mustHandle(h.message("Alice", "aaa"))
	j, _ := lastReply(h)["judged"].(*judgment)
	if j == nil || math.Abs(*j.Share-1.6) > 1e-9 || math.Abs(j.Threshold-0.53) > 1e-9 {
		t.Fatalf("judged %+v", j)
	}
	// Its reply is a follow-up too: the next share counts it.
	if cause := turnCause(t, h, "agent-1"); cause != "follow" {
		t.Fatalf("the reply's turn says %q, want follow", cause)
	}
}

// turnCause is what made the agent answer with the message it sent as source.
func turnCause(t *testing.T, h *harness, source string) string {
	t.Helper()
	_, _, cause, found, err := h.m.store.agentTurn(t.Context(), otogi.EventRef{Platform: "telegram", Account: "main", Conversation: "chat:1", SourceEvent: otogi.SourceEventID(source)})
	if err != nil || !found {
		t.Fatalf("turn of %s: found %v, %v", source, found, err)
	}
	return cause
}

func TestFollowThreshold(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ base, share, want float64 }{
		{0.5, 0, 0.5},
		{0.5, 1.3, 0.5},
		{0.5, 2.3, 0.6},
		{0.5, 9, 0.8},
		{0.85, 9, 0.85}, // a base above the cap stays
	} {
		if got := followThreshold(c.base, c.share); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("followThreshold(%v, %v) = %v, want %v", c.base, c.share, got, c.want)
		}
	}
}

// A message addressed to the agent wakes it as addressed, though it also
// carries on the exchange; when the request fails, only a name starting the
// message triggers it, as a keyword.
func TestWakeBeatsFollow(t *testing.T) {
	d := &decider{scores: map[string]float64{"a1": 0.8}, follow: map[string]float64{"a1": 0.95}}
	h := newJudged(t, d, followProfile("luna", 0.7, 0.5))
	h.replayAgent(t, "luna", "follow", "hi", 1)
	h.mustHandle(h.message("Alice", "and you, luna?"))
	if q := questionsOf(d, 0); !slices.Equal(q, []string{"follow.a1.m2", "wake.a1.m2"}) {
		t.Fatalf("questions %v", q)
	}
	if rec := lastReply(h); rec["trigger"] != "decision" || rec["judged"].(*judgment).Score != 0.8 {
		t.Fatalf("record %+v", rec)
	}
	if cause := turnCause(t, h, "agent-1"); cause != "decision" {
		t.Fatalf("the reply's turn says %q, want decision", cause)
	}

	d.err = errors.New("unavailable")
	h.mustHandle(h.message("Alice", "so what now"))
	if len(h.llm["luna"].requests) != 1 {
		t.Fatal("a failed request answered a follow-up")
	}
	h.mustHandle(h.message("Alice", "luna, so what now"))
	if len(h.llm["luna"].requests) != 2 || lastReply(h)["trigger"] != "keyword" {
		t.Fatalf("a failed request: replies %d, %+v", len(h.llm["luna"].requests), lastReply(h))
	}
}

// An agent that only follows up keeps its keyword as a trigger, and needs
// the decision service; an unset threshold is the form's default.
func TestFollowOnly(t *testing.T) {
	d := &decider{}
	h := newJudged(t, d, ProfileConfig{Name: "luna", Keywords: []string{"luna"}, Provider: "luna", Model: "m", Judge: &JudgeConfig{Follow: &FollowConfig{}}})
	h.mustHandle(h.message("Alice", "luna, hello"))
	if len(d.requests) != 0 || len(h.llm["luna"].requests) != 1 || lastReply(h)["trigger"] != "keyword" {
		t.Fatalf("decision requests %d, replies %d", len(d.requests), len(h.llm["luna"].requests))
	}
	if !slices.Contains(h.m.Descriptor().Requires, decision.Service) || h.m.profiles[0].follow != defaultFollow {
		t.Fatalf("requires %v, threshold %v", h.m.Descriptor().Requires, h.m.profiles[0].follow)
	}
	field, _ := reflect.TypeFor[FollowConfig]().FieldByName("Threshold")
	if tag := field.Tag.Get("default"); tag != strconv.FormatFloat(defaultFollow, 'g', -1, 64) {
		t.Fatalf("the form shows %s, an unset threshold is %v", tag, defaultFollow)
	}
}

// A sender's factor multiplies the follow threshold as it does the wake one;
// one that takes it past 1 leaves their messages unjudged, and says so.
func TestFollowThresholdFactor(t *testing.T) {
	d := &decider{follow: map[string]float64{"a1": 0.5}}
	h := newJudged(t, d, followProfile("luna", 0, 0.6))
	h.know(PersonConfig{User: "telegram:user:Owner", ThresholdFactor: 0.8}, PersonConfig{User: "telegram:user:Joker", ThresholdFactor: 2})
	h.replayAgent(t, "luna", "follow", "hi", 1)
	h.mustHandle(h.message("Owner", "and then?"))
	j, _ := lastReply(h)["judged"].(*judgment)
	if j == nil || math.Abs(j.Threshold-0.48) > 1e-9 || j.Factor != 0.8 {
		t.Fatalf("judged %+v", j)
	}
	asked := len(d.requests)
	joker := h.message("Joker", "and then?")
	h.mustHandle(joker)
	r := h.host.records[len(h.host.records)-1]
	if len(d.requests) != asked || r.Kind != "chat.unjudged" || r.Event != joker.ID || math.Abs(r.Payload.(map[string]any)["threshold"].(float64)-1.2) > 1e-9 {
		t.Fatalf("asked %d more; last record %s %+v", len(d.requests)-asked, r.Kind, r.Payload)
	}
}

// With judge.wait, a follow-up waits for its sender as a message to the agent
// does, but someone who sends runs of lines is waited for at least two of
// their rhythms, however finished each line seems; a message to the agent is
// answered once its sender has finished.
func TestFollowUpWaitsForSomeoneWhoSendsRuns(t *testing.T) {
	d := &decider{scores: map[string]float64{"a1": 0.1}, follow: map[string]float64{"a1": 0.9}, answer: finished()}
	p := followProfile("luna", 0.7, 0.5)
	p.Judge.Wait = &WaitConfig{Min: otogi.Duration(50 * time.Millisecond), Max: otogi.Duration(time.Second)}
	h := newJudged(t, d, p)
	first := h.message("Alice", "so I went to the station")
	h.mustHandle(first)
	h.mustHandle(h.message("Alice", "and the train was gone", func(e *otogi.Event) { e.OccurredAt = first.OccurredAt.Add(2 * time.Second) }))
	h.replayAgent(t, "luna", "follow", "oh no", 1)

	// Two of Alice's 3s rhythms is 6s: the wait ends at max.
	start := time.Now()
	h.mustHandle(h.message("Alice", "I had to walk home"))
	if took := time.Since(start); took < 900*time.Millisecond || lastReply(h)["trigger"] != "follow" {
		t.Fatalf("a follow-up answered after %v, %+v", took, lastReply(h))
	}
	// Two minutes on, after luna's answer: no run with the line before.
	d.scores["a1"] = 0.9
	h.now = h.now.Add(time.Minute)
	start = time.Now()
	h.mustHandle(h.message("Alice", "luna, do you walk much?"))
	if took := time.Since(start); took > 700*time.Millisecond || lastReply(h)["trigger"] != "decision" {
		t.Fatalf("a message to the agent answered after %v, %+v", took, lastReply(h))
	}
}
