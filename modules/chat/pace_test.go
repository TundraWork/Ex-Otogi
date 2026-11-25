package chat

import (
	"testing"
	"time"
)

// A rhythm is how fast someone sends a run of messages: the gaps between
// their own messages in a row, with nothing in between and under a minute
// apart. A back-and-forth, anyone's line in between, or an agent starting a
// reply in between is no rhythm.
func TestRhythm(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	at := func(sec float64) time.Time { return base.Add(time.Duration(sec * float64(time.Second))) }
	by := func(who string, sec float64) line { return line{ActorID: who, At: at(sec)} }
	agent := func(sec float64) line { l := by("bot", sec); l.Outgoing = true; return l }
	for name, c := range map[string]struct {
		lines   []line
		replied []time.Time
		want    time.Duration
		own     bool
	}{
		"their own run":              {[]line{by("a", 0), by("a", 4), by("a", 10), by("a", 12)}, nil, 4 * time.Second, true},
		"no faster than the floor":   {[]line{by("a", 0), by("a", 1), by("a", 2)}, nil, rhythmFloor, true},
		"someone else's run":         {[]line{by("b", 0), by("b", 8), by("b", 14), by("a", 20)}, nil, 7 * time.Second, false},
		"no runs at all":             {[]line{by("a", 0), by("b", 5), by("a", 10)}, nil, rhythmDefault, false},
		"a minute apart is no run":   {[]line{by("a", 0), by("a", 70)}, nil, rhythmDefault, false},
		"the agent in between":       {[]line{by("a", 0), agent(2), by("a", 4)}, nil, rhythmDefault, false},
		"a back-and-forth is no run": {[]line{by("a", 0), by("b", 30), by("a", 61)}, nil, rhythmDefault, false},
		"a reply started in between": {[]line{by("a", 0), by("a", 4), by("a", 30)}, []time.Time{at(6)}, 4 * time.Second, true},
		"a reply started before":     {[]line{by("a", 10), by("a", 14)}, []time.Time{at(10)}, 4 * time.Second, true},
	} {
		if got, own := rhythm(c.lines, c.replied, "a"); got != c.want || own != c.own {
			t.Errorf("%s: rhythm %v, own %v; want %v, %v", name, got, own, c.want, c.own)
		}
	}
}

// The test group on 2026-11-25, as sent: "ds", "Go 1.27有什么新东西" 7s
// later, "ds 今天星期几" while the answer to it was written, then "ds 谢了"
// and "ds 还在吗？" 2s apart. The agent started its replies 20s after the
// second and the third. The 27s waiting for the first answer is no rhythm:
// counted, it made the last message's rhythm 7s; without it, 4.5s.
func TestRhythmOfTheTestGroup(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 11, 25, 2, 6, 0, 0, time.FixedZone("JST", 9*60*60))
	at := func(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }
	var lines []line
	for _, sec := range []int{14, 21, 48, 118, 120} {
		lines = append(lines, line{ActorID: "user:mihari", At: at(sec)})
	}
	replied := []time.Time{at(41), at(68)}
	if got, _ := rhythm(lines[:3], replied[:1], "user:mihari"); got != 7*time.Second {
		t.Errorf("rhythm at the third message %v, want 7s", got)
	}
	got, _ := rhythm(lines, replied, "user:mihari")
	if got != 4500*time.Millisecond {
		t.Fatalf("rhythm at the last message %v, want 4.5s", got)
	}
	if w := waitFor(2*time.Second, 0.97, true, got); w > 2500*time.Millisecond {
		t.Errorf("waits %v after a finished message, want about min (2s)", w)
	}
}

// The wait after a message is the least when its sender has finished, up to
// one and a half rhythms more the less sure that is.
func TestWaitFor(t *testing.T) {
	t.Parallel()
	least, r := 2*time.Second, 4*time.Second
	for name, c := range map[string]struct {
		done  float64
		known bool
		want  time.Duration
	}{
		"finished":                  {1, true, least},
		"half sure":                 {0.5, true, least + 3*time.Second},
		"not finished":              {0, true, least + 6*time.Second},
		"not known":                 {0, false, least + 3*time.Second},
		"a score out of range is 1": {1.5, true, least},
	} {
		if got := waitFor(least, c.done, c.known, r); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

// After a follow-up, someone with a run of their own is waited for at least
// two of their rhythms; anyone else as after any message.
func TestFollowUpWait(t *testing.T) {
	t.Parallel()
	r := 4 * time.Second
	for name, c := range map[string]struct {
		wait time.Duration
		own  bool
		want time.Duration
	}{
		"a run of their own":    {2 * time.Second, true, 8 * time.Second},
		"a wait already longer": {10 * time.Second, true, 10 * time.Second},
		"no run of their own":   {2 * time.Second, false, 2 * time.Second},
	} {
		if got := followUpWait(c.wait, r, c.own); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}
