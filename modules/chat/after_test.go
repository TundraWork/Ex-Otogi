package chat

import (
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/decision"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Messages that called the agent while it replied are answered after that
// reply, which the agent sees: under a heading of its own, as it came after
// them within their minute, before the turn, which ends with them. The
// platform hands the reply back before they are answered, as QQ does within
// a second.
func TestAReplySentWhileMessagesWaitedIsSeen(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		asked   []string
		heading string
		lead    string
	}{
		{[]string{"luna and how far does 7kg reach"}, "Sent after the message to answer:", "The message to answer:"},
		{[]string{"luna and how far does 7kg reach", "luna and buried?"}, "Sent after the messages to answer:", "The messages to answer:"},
	} {
		echoed := make(chan struct{})
		d := &decider{answer: func(id string, req decision.Request) (float64, bool) {
			if id == coveredID {
				return 0.1, true
			}
			return finished()(id, req)
		}}
		d.before = func(req decision.Request) {
			if _, ok := req.Questions[coveredID]; ok {
				<-echoed
			}
		}
		p := waitingProfile("luna", 20*time.Millisecond, 2*time.Second)
		p.Label = "Luna"
		h := newJudged(t, d, p)
		h.llm["luna"].responses = []llm.Response{answer("TM-62M holds 7.5 kg"), answer("about ten metres")}
		release := make(chan struct{})
		h.llm["luna"].before = func(n int) {
			if n == 1 {
				<-release
			}
		}
		first := h.message("Alice", "luna how much explosive is in a TM-62")
		var later []otogi.Event
		for _, text := range c.asked {
			later = append(later, h.message("Alice", text))
		}
		errc := make(chan error, 1)
		go func() { errc <- h.handle(first) }()
		eventually(t, "the first reply to start", func() bool { return h.llm["luna"].count() == 1 })
		errs := make(chan error, len(later))
		for _, e := range later { // one after another, so they join in this order
			go func() { errs <- h.handle(e) }()
			time.Sleep(100 * time.Millisecond)
		}
		close(release)
		if err := <-errc; err != nil {
			t.Fatal(err)
		}
		h.mu.Lock()
		s := h.streams[0]
		h.mu.Unlock()
		last := later[len(later)-1]
		h.echo(s, func(e *otogi.Event) { e.OccurredAt = last.OccurredAt.Add(20 * time.Second) }) // in the same minute
		close(echoed)
		for range later {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
		}
		if n := h.llm["luna"].count(); n != 2 {
			t.Fatalf("%v: %d requests", c.asked, n)
		}
		got := lastUserText(h.llm["luna"].requests[1])
		minute := last.OccurredAt.In(time.Local).Format("15:04")
		want := c.heading + "\n[" + minute + "] Luna (this account): TM-62M holds 7.5 kg\n\n"
		if !strings.Contains(got, want) || !strings.Contains(got, "["+minute+"] ") || !strings.HasSuffix(got, "Alice: "+c.asked[len(c.asked)-1]) {
			t.Fatalf("%v: user turn %q", c.asked, got)
		}
		if i, j := strings.Index(got, "Recent messages"), strings.Index(got, c.heading); i < 0 || i > j || !strings.Contains(got[j:], c.lead) {
			t.Fatalf("%v: user turn %q", c.asked, got)
		}
	}
}

// What the chat says while the agent waits to chime in, and the agent goes on
// waiting, is seen when it speaks, after the message it speaks up on.
func TestChimeSeesWhatCameDuringTheWait(t *testing.T) {
	t.Parallel()
	d := &decider{answer: chiming(0.9, 0.8)}
	h := newChiming(t, d, chimeProfile("luna", 0))
	first := h.message("Alice", "anyone tried the new Unity?")
	errc := make(chan error, 1)
	go func() { errc <- h.handle(first) }()
	eventually(t, "the wait to start", func() bool { return waitingToChime(h) })
	h.mustHandle(h.message("Bob", "the shaders broke for me"))
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if n := h.llm["luna"].count(); n != 1 {
		t.Fatalf("%d replies", n)
	}
	got := lastUserText(h.llm["luna"].requests[0])
	if !strings.Contains(got, "Sent after the message to answer:\n[") || !strings.Contains(got, "] Bob: the shaders broke for me\n\nThe message to answer:") || !strings.Contains(got, "Alice: anyone tried the new Unity?") {
		t.Fatalf("user turn %q", got)
	}
}
