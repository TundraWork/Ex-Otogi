package chat

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// What an agent said reads, in the recent chat and in a quote, as said by
// the agent through the account, without the label its reply showed first;
// another module's message reads as the account's. Each agent's line comes
// back from the platform as it was sent.
func TestAgentLinesReadAsTheirAgent(t *testing.T) {
	t.Parallel()
	h := newHarness(t,
		ProfileConfig{Name: "otogi", Label: "Otogi", Keywords: []string{"otogi"}, Provider: "otogi", Model: "m"},
		ProfileConfig{Name: "sol", Label: "Sol", Keywords: []string{"sol"}, Provider: "sol", Model: "m"})
	h.llm["sol"].responses = []llm.Response{answer("sun's out")}
	h.mustHandle(h.message("Alice", "otogi hi"))
	otogiSaid := h.streams[0]
	if got := otogiSaid.final.PlainText(); got != "Otogi\nhello" {
		t.Fatalf("sent %q", got)
	}
	h.echo(otogiSaid)
	h.mustHandle(h.message("Bob", "sol hi"))
	solSaid := h.streams[1]
	h.echo(solSaid)
	h.mustHandle(h.message("Me", "a card", func(e *otogi.Event) { e.Direction, e.SourceID = otogi.DirectionOutgoing, "card-1" }))

	h.mustHandle(h.message("Carol", "otogi is that so?", replyingTo(string(solSaid.ref.SourceEvent))))
	req := h.llm["otogi"].requests[1]
	got := lastUserText(req)
	for _, want := range []string{"Otogi (this account): hello", "Sol (this account): sun's out", "Me (this account): a card", "Replying to Sol (this account):\nsun's out"} {
		if !strings.Contains(got, want) {
			t.Fatalf("no %q in %q", want, got)
		}
	}
	if strings.Contains(got, "**") {
		t.Fatalf("a label in %q", got)
	}
	if !strings.Contains(req.System, "those from Otogi (this account) are yours") {
		t.Fatalf("system %q", req.System)
	}
}

// A judgment reads an agent's line by the agent's number and title, without
// the label its reply showed first, and so does a quote of one that has left
// the history.
func TestAgentLinesInJudgments(t *testing.T) {
	t.Parallel()
	d := &decider{scores: map[string]float64{"a1": 0.9}}
	luna := judgedProfile("luna", 0.7)
	luna.Label = "Luna"
	h := newJudged(t, d, luna)
	h.mustHandle(h.message("Alice", "luna, look"))
	said := h.streams[0]
	h.echo(said)
	d.scores["a1"] = 0.1
	h.mustHandle(h.message("Bob", "nice"))
	state := d.requests[len(d.requests)-1].State.(judgeState)
	i := slices.IndexFunc(state.History, func(l judgeLine) bool { return l.From == "a1 Luna" })
	if i < 0 || state.History[i].Text != "hello" {
		t.Fatalf("history %+v", state.History)
	}

	// Bob's reply to it stays in the history after the agent's line has left.
	h.mustHandle(h.message("Bob", "ha", replyingTo(string(said.ref.SourceEvent))))
	for range judgeHistory - 2 {
		h.mustHandle(h.message("Carol", "filler"))
	}
	state = d.requests[len(d.requests)-1].State.(judgeState)
	i = slices.IndexFunc(state.History, func(l judgeLine) bool { return l.Text == "ha" })
	if i < 0 || state.History[i].ReplyTo != "" || state.History[i].ReplyingTo != "Luna (this account): hello" {
		t.Fatalf("history %+v", state.History)
	}
}

// Another agent's line between the messages of a batch reads as that agent's
// too.
func TestAgentLinesInABatch(t *testing.T) {
	t.Parallel()
	d := &decider{answer: finished("luna tell me about")}
	h := newJudged(t, d, waitingProfile("luna", 20*time.Millisecond, 2*time.Second),
		ProfileConfig{Name: "sol", Label: "Sol", Keywords: []string{"sol"}, Provider: "sol", Model: "m"})
	h.llm["sol"].responses = []llm.Response{answer("sun's out")}
	first := h.message("Alice", "luna tell me about")
	errc := make(chan error, 1)
	go func() { errc <- h.handle(first) }()
	time.Sleep(150 * time.Millisecond)
	h.mustHandle(h.message("Bob", "sol hi"))
	h.mu.Lock()
	solSaid := h.streams[0]
	h.mu.Unlock()
	h.echo(solSaid)
	h.mustHandle(h.message("Alice", "luna the weather"))
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if n := h.llm["luna"].count(); n != 1 {
		t.Fatalf("%d replies, want 1", n)
	}
	got := lastUserText(h.llm["luna"].requests[0])
	if !strings.Contains(got, "Answer: Alice: luna tell me about / Alice: luna the weather") || !strings.Contains(got, "Sol (this account): sun's out") || strings.Contains(got, "**") {
		t.Fatalf("user turn %q", got)
	}
}
