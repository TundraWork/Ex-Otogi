package chat

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/decision"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	mediasvc "github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// describer is a fake media service: it keeps descriptions by the media's
// locator, and Describe fails with err, or waits for its context to end
// when block is set, for media it has none of.
type describer struct {
	mu    sync.Mutex
	kept  map[string]mediasvc.Description
	err   error
	block bool
	asked []string
	// late, when set, is described at lateAt: Describe waits for it until
	// then, or until its context ends.
	late   map[string]mediasvc.Description
	lateAt time.Time
	// originals names the media whose originals the module keeps, which
	// Examine takes without bytes; examineErr fails Examine, and
	// examineBlock has it wait for its context to end. Each call is kept in
	// examined.
	originals    map[string]bool
	examineErr   error
	examineBlock bool
	examined     []examineCall
}

// examineCall is a call to the fake Examine: the media's name, whether it
// came with its bytes, and the question.
type examineCall struct {
	name     string
	inline   bool
	question string
}

// Examine answers "<name>: <question>" about media with its bytes (named by
// them, as the harness fetches "bytes of <name>"), or about media whose
// original is kept.
func (d *describer) Examine(ctx context.Context, _ otogi.Destination, md otogi.Media, question string) (string, error) {
	d.mu.Lock()
	inline := md.Source.Kind == otogi.MediaSourceInline
	name := string(md.Source.Locator.Data)
	if inline {
		name = strings.TrimPrefix(string(md.Source.Inline), "bytes of ")
	}
	d.examined = append(d.examined, examineCall{name: name, inline: inline, question: question})
	kept, err, block := d.originals[name], d.examineErr, d.examineBlock
	d.mu.Unlock()
	switch {
	case !inline && !kept:
		return "", mediasvc.ErrNotKept
	case block:
		<-ctx.Done()
		return "", ctx.Err()
	case err != nil:
		return "", err
	}
	return name + ": " + question, nil
}

// Original gives back media whose original is kept, as "kept bytes of
// <name>"; the rest is not kept.
func (d *describer) Original(_ context.Context, _ otogi.Destination, md otogi.Media) (otogi.Media, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	name := string(md.Source.Locator.Data)
	if !d.originals[name] {
		return otogi.Media{}, mediasvc.ErrNotKept
	}
	md.Source = otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: []byte("kept bytes of " + name)}
	return md, nil
}

func (d *describer) Describe(ctx context.Context, _ otogi.Destination, md otogi.Media) (mediasvc.Description, error) {
	d.mu.Lock()
	name := string(md.Source.Locator.Data)
	d.asked = append(d.asked, name)
	desc, ok := d.kept[name]
	block, err := d.block, d.err
	late, isLate := d.late[name]
	d.mu.Unlock()
	switch {
	case ok:
		return desc, nil
	case isLate:
		timer := time.NewTimer(time.Until(d.lateAt))
		defer timer.Stop()
		select {
		case <-timer.C:
			return late, nil
		case <-ctx.Done():
			return mediasvc.Description{}, ctx.Err()
		}
	case block:
		<-ctx.Done()
		return mediasvc.Description{}, ctx.Err()
	}
	return mediasvc.Description{}, err
}

func (d *describer) Lookup(_ context.Context, _ otogi.Destination, md otogi.Media) (mediasvc.Description, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	desc, ok := d.kept[string(md.Source.Locator.Data)]
	return desc, ok, nil
}

var cat = mediasvc.Description{Kind: mediasvc.KindSticker, Summary: "a cat eating watermelon, content"}

func newDescriber() *describer {
	return &describer{kept: map[string]mediasvc.Description{"cat": cat}, err: mediasvc.ErrNotAllowed}
}

// photo is an image part named name, which the fake describer keys on.
func photo(name string) otogi.Part {
	return otogi.Part{ID: otogi.PartID(name), Kind: otogi.PartMedia, Media: &otogi.Media{Role: otogi.MediaImage,
		Source: otogi.MediaSource{Kind: otogi.MediaSourceOpaque, Locator: otogi.ProviderLocator{Data: []byte(name)}}}}
}

// withParts makes a message of text, if any, and parts.
func withParts(text string, parts ...otogi.Part) func(*otogi.Event) {
	return func(e *otogi.Event) {
		var msg otogi.Message
		if text != "" {
			msg = otogi.TextMessage(text)
		}
		for _, p := range parts {
			msg = llm.Join(msg, otogi.Message{Roots: []otogi.PartID{p.ID}, Parts: []otogi.Part{p}})
		}
		e.Message = msg
	}
}

func mediaProfile(asText bool) ProfileConfig {
	return ProfileConfig{Name: "otogi", Keywords: []string{"otogi"}, Provider: "otogi", Model: "m", Media: &MediaConfig{AsText: asText}}
}

// mediaParts counts the media parts the model got in the request's last turn.
func mediaParts(req llm.Request) int {
	n := 0
	for _, p := range req.Turns[len(req.Turns)-1].Content.Parts {
		if p.Kind == otogi.PartMedia {
			n++
		}
	}
	return n
}

func TestDescriptionsInTheTranscript(t *testing.T) {
	h := newHarnessWith(t, map[string]any{mediasvc.Service: newDescriber()}, mediaProfile(false))
	h.mustHandle(h.message("Bob", "", withParts("", photo("cat"))))
	h.mustHandle(h.message("Bob", "", withParts("and this", photo("dog"))))
	h.mustHandle(h.message("Alice", "otogi, what are these?", replyingTo("m1")))

	req := h.llm["otogi"].requests[0]
	text := lastUserText(req)
	for _, want := range []string{"Bob: [sticker: a cat eating watermelon, content]", "Bob: and this [photo]"} {
		if !strings.Contains(text, want) {
			t.Errorf("request lacks %q:\n%s", want, text)
		}
	}
	// Without as_text the quoted picture is still given as itself.
	if n := mediaParts(req); n != 1 {
		t.Fatalf("%d media parts, want the quoted picture", n)
	}
}

// A kept sticker reads as its number in the recent messages, so the agent
// can send it back.
func TestKeptStickerInTheChat(t *testing.T) {
	d := newDescriber()
	d.kept["eku"] = mediasvc.Description{Kind: mediasvc.KindMeme, Summary: "Eku喵喵叫", Sticker: 3}
	h := newHarnessWith(t, map[string]any{mediasvc.Service: d}, mediaProfile(false))
	h.mustHandle(h.message("Bob", "", withParts("", photo("eku"))))
	h.mustHandle(h.message("Alice", "otogi, what is that?"))
	if text := lastUserText(h.llm["otogi"].requests[0]); !strings.Contains(text, "Bob: [sticker 3: Eku喵喵叫]") {
		t.Fatalf("request:\n%s", text)
	}
}

func TestWithoutMediaNoDescriptions(t *testing.T) {
	d := newDescriber()
	h := newHarnessWith(t, map[string]any{mediasvc.Service: d}, ProfileConfig{Name: "otogi", Keywords: []string{"otogi"}, Provider: "otogi", Model: "m"})
	h.mustHandle(h.message("Bob", "", withParts("", photo("cat"))))
	h.mustHandle(h.message("Alice", "otogi, what is that?"))
	if text := lastUserText(h.llm["otogi"].requests[0]); !strings.Contains(text, "Bob: [photo]") {
		t.Fatalf("an agent without media read a description:\n%s", text)
	}
	if len(d.asked) != 0 {
		t.Fatalf("the media service was asked about %v", d.asked)
	}
	if req := h.m.Descriptor().Requires; len(req) != 0 {
		t.Fatalf("requires %v", req)
	}
}

func TestAsText(t *testing.T) {
	h := newHarnessWith(t, map[string]any{mediasvc.Service: newDescriber()}, mediaProfile(true))
	if req := h.m.Descriptor().Requires; len(req) != 1 || req[0] != mediasvc.Service {
		t.Fatalf("requires %v, want the media service", req)
	}
	h.mustHandle(h.message("Alice", "", withParts("otogi, what is this?", photo("cat"))))
	req := h.llm["otogi"].requests[0]
	if mediaParts(req) != 0 || !strings.Contains(lastUserText(req), "[sticker: a cat eating watermelon, content]") {
		t.Fatalf("as_text sent %d media parts:\n%s", mediaParts(req), lastUserText(req))
	}

	h.mustHandle(h.message("Alice", "", withParts("otogi, and this?", photo("dog"))))
	req = h.llm["otogi"].requests[1]
	if mediaParts(req) != 0 || !strings.Contains(lastUserText(req), "and this?\n\n[photo]") {
		t.Fatalf("undescribed media under as_text: %d media parts:\n%s", mediaParts(req), lastUserText(req))
	}
	var skipped map[string]any
	for _, r := range h.host.records {
		if r.Kind == "chat.media_skipped" {
			skipped = r.Payload.(map[string]any)
		}
	}
	if skipped["reason"] != "not described" || !strings.Contains(skipped["error"].(string), "describe list") {
		t.Fatalf("chat.media_skipped = %v", skipped)
	}
}

// The quoted message's media is only looked up: the media module never comes
// back to an older message's media, so waiting for it would only run out.
func TestAsTextQuoteDoesNotWait(t *testing.T) {
	d := newDescriber()
	d.block = true
	h := newHarnessWith(t, map[string]any{mediasvc.Service: d}, mediaProfile(true))
	h.mustHandle(h.message("Bob", "", withParts("", photo("dog"))))
	h.mustHandle(h.message("Bob", "", withParts("", photo("cat"))))
	d.asked = nil

	start := time.Now()
	h.mustHandle(h.message("Alice", "otogi, what is this?", replyingTo("m1")))
	h.mustHandle(h.message("Alice", "otogi, and this?", replyingTo("m2")))
	if took := time.Since(start); took > time.Duration(h.m.cfg.DescribeWait.Judge)/2 {
		t.Fatalf("replies quoting older media took %v", took)
	}
	if len(d.asked) != 0 {
		t.Fatalf("waited on quoted media %v", d.asked)
	}
	first, second := h.llm["otogi"].requests[0], h.llm["otogi"].requests[1]
	if mediaParts(first) != 0 || !strings.Contains(lastUserText(first), "Replying to Bob:\n\n\n[photo]") {
		t.Fatalf("an undescribed quoted picture: %d media parts:\n%q", mediaParts(first), lastUserText(first))
	}
	if !strings.Contains(lastUserText(second), "[sticker: a cat eating watermelon, content]") {
		t.Fatalf("a described quoted picture:\n%s", lastUserText(second))
	}
	var skipped map[string]any
	for _, r := range h.host.records {
		if r.Kind == "chat.media_skipped" {
			skipped = r.Payload.(map[string]any)
		}
	}
	if skipped["reason"] != "not described" || skipped["error"] != errNotKept.Error() {
		t.Fatalf("chat.media_skipped = %v", skipped)
	}
}

// The media of one message wait for their descriptions until one deadline
// together, not each in turn.
func TestOneWaitPerMessage(t *testing.T) {
	d := newDescriber()
	d.block = true
	h := newHarnessWith(t, map[string]any{mediasvc.Service: d}, mediaProfile(true))
	e := h.message("Alice", "", withParts("otogi, look", photo("a"), photo("b"), photo("c")))
	const wait = 100 * time.Millisecond
	start := time.Now()
	msg, _ := h.m.userContent(context.Background(), e, invocation{h: h}, h.m.byName["otogi"], true, start.Add(wait))
	took := time.Since(start)
	if took < wait || took > 2*wait {
		t.Fatalf("three pieces of media waited %v, want about %v in all", took, wait)
	}
	if text := msg.PlainText(); strings.Count(text, "[photo]") != 3 {
		t.Fatalf("content %q", text)
	}
}

func TestJudgmentSeesDescriptions(t *testing.T) {
	media := newDescriber()
	dec := &decider{scores: map[string]float64{"a1": 0.1}}
	p := judgedProfile("luna", 0.7)
	p.Media = &MediaConfig{}
	h := newHarnessWith(t, map[string]any{decision.Service: dec, mediasvc.Service: media}, p)
	h.mustHandle(h.message("Bob", "", withParts("", photo("cat"))))

	state := dec.requests[0].State.(judgeState)
	if got := state.History[len(state.History)-1].Text; got != "[sticker: a cat eating watermelon, content]" {
		t.Fatalf("the judgment saw %q", got)
	}
	if len(media.asked) != 1 || media.asked[0] != "cat" {
		t.Fatalf("the judgment waited on %v, want the message's picture", media.asked)
	}
}

func TestJudgmentWithoutMedia(t *testing.T) {
	media := newDescriber()
	dec := &decider{scores: map[string]float64{"a1": 0.1}}
	h := newHarnessWith(t, map[string]any{decision.Service: dec, mediasvc.Service: media}, judgedProfile("luna", 0.7))
	h.mustHandle(h.message("Bob", "", withParts("", photo("cat"))))
	if got := dec.requests[0].State.(judgeState).History[0].Text; got != "[photo]" || len(media.asked) != 0 {
		t.Fatalf("the judgment saw %q and asked %v", got, media.asked)
	}
}

func TestDescriptionText(t *testing.T) {
	long := strings.Repeat("字", 301)
	for _, c := range []struct {
		md    otogi.Media
		d     mediasvc.Description
		limit int
		ids   bool
		want  string
	}{
		{d: cat, limit: 300, want: "[sticker: a cat eating watermelon, content]"},
		{d: mediasvc.Description{Kind: mediasvc.KindScreenshot, Summary: "a post", Text: "line one\nline  two"}, limit: 300, want: `[screenshot: a post Text: "line one line two"]`},
		{d: mediasvc.Description{Kind: mediasvc.KindScreenshot, Summary: "a post", Text: "line one\nline  two"}, want: "[screenshot: a post Text: \"line one\nline  two\"]"},
		{d: mediasvc.Description{Kind: mediasvc.KindMeme, Summary: "s", Text: long}, limit: 300, want: `[meme: s Text: "` + strings.Repeat("字", 300) + `…"]`},
		{md: otogi.Media{Duration: 72 * time.Second}, d: mediasvc.Description{Kind: mediasvc.KindVideo, Summary: "a song", Text: "la la"}, limit: 300, want: `[video 1:12: a song Said: "la la"]`},
		{d: mediasvc.Description{Kind: mediasvc.KindVideo, Summary: "a song"}, want: "[video: a song]"},
		// A kept sticker reads as its number, whatever its kind.
		{d: mediasvc.Description{Kind: mediasvc.KindMeme, Summary: "Eku喵喵叫", Sticker: 3}, limit: 300, want: "[sticker 3: Eku喵喵叫]"},
		{d: mediasvc.Description{Kind: mediasvc.KindScreenshot, Summary: "s", Text: "哈哈", Sticker: 3}, limit: 300, want: `[sticker 3: s Text: "哈哈"]`},
		// With ids, other media shows its ID after its kind; a kept sticker
		// still its number, and an ID only where there is one.
		{d: mediasvc.Description{Kind: mediasvc.KindPhoto, Summary: "a tank", ID: "00a0a6"}, want: "[photo: a tank]"},
		{d: mediasvc.Description{Kind: mediasvc.KindPhoto, Summary: "a tank", ID: "00a0a6"}, ids: true, want: "[photo #00a0a6: a tank]"},
		{md: otogi.Media{Duration: 12 * time.Second}, d: mediasvc.Description{Kind: mediasvc.KindVideo, Summary: "a song", ID: "9f03c1"}, ids: true, want: "[video 0:12 #9f03c1: a song]"},
		{d: mediasvc.Description{Kind: mediasvc.KindMeme, Summary: "Eku喵喵叫", Sticker: 3}, ids: true, want: "[sticker 3: Eku喵喵叫]"},
		{d: mediasvc.Description{Kind: mediasvc.KindPhoto, Summary: "a tank"}, ids: true, want: "[photo: a tank]"},
	} {
		if got := descriptionText(c.md, c.d, c.limit, c.ids); got != c.want {
			t.Errorf("descriptionText = %q, want %q", got, c.want)
		}
	}
}

// A message waits for its media's descriptions until describe_wait.judge
// before it is judged, and until describe_wait.reply, both from its
// arrival, before the agent answers it, alone or in a batch: a picture
// described in between is judged unseen and answered seen.
func TestJudgmentAndReplyWaitUntilTheirOwnDeadlines(t *testing.T) {
	for _, batched := range []bool{false, true} {
		t.Run(map[bool]string{false: "alone", true: "batch"}[batched], func(t *testing.T) { judgedThenAnswered(t, batched) })
	}
}

func judgedThenAnswered(t *testing.T, batched bool) {
	synctest.Test(t, func(t *testing.T) {
		d := newDescriber()
		var judgedAt time.Time
		judge := &decider{scores: map[string]float64{"a1": 0.9}, answer: finished(), before: func(decision.Request) {
			if judgedAt.IsZero() {
				judgedAt = time.Now()
			}
		}}
		p := mediaProfile(true)
		p.Judge = &JudgeConfig{Wake: &WakeConfig{}}
		if batched {
			p.Judge.Wait = &WaitConfig{}
		}
		h := newHarnessWith(t, map[string]any{mediasvc.Service: d, decision.Service: judge}, p)
		start := time.Now()
		d.late = map[string]mediasvc.Description{"chart": {Kind: mediasvc.KindScreenshot, Summary: "a chart of sales by month"}}
		d.lateAt = start.Add(20 * time.Second)
		h.mustHandle(h.message("Alice", "", withParts("what do you make of this", photo("chart"))))
		if took := time.Since(start); took != 20*time.Second {
			t.Fatalf("answered after %v, want the 20s the description took", took)
		}
		if waited := judgedAt.Sub(start); waited != 15*time.Second {
			t.Fatalf("judged after %v, want describe_wait.judge's 15s", waited)
		}
		history := judge.requests[0].State.(judgeState).History
		if judged := history[len(history)-1].Text; strings.Contains(judged, "sales") {
			t.Fatalf("judged with the description %q, which came after 15s", judged)
		}
		if got := lastUserText(h.llm["otogi"].requests[0]); !strings.Contains(got, "a chart of sales by month") {
			t.Fatalf("answered without the description:\n%s", got)
		}
	})
}

// The recent chat keeps described_text characters of a picture's text:
// 1000 unless set.
func TestDescribedTextInTheRecentChat(t *testing.T) {
	text := strings.Repeat("字", 1200)
	for _, c := range []struct{ set, kept int }{{0, 1000}, {50, 50}} {
		d := newDescriber()
		d.kept["shot"] = mediasvc.Description{Kind: mediasvc.KindScreenshot, Summary: "a long post", Text: text}
		h := newHarnessConfig(t, map[string]any{mediasvc.Service: d}, Config{DescribedText: c.set, Profiles: []ProfileConfig{mediaProfile(false)}})
		h.mustHandle(h.message("Bob", "", withParts("", photo("shot"))))
		h.mustHandle(h.message("Alice", "otogi what did it say"))
		want := `[screenshot: a long post Text: "` + strings.Repeat("字", c.kept) + `…"]`
		if got := lastUserText(h.llm["otogi"].requests[0]); !strings.Contains(got, want) || strings.Contains(got, strings.Repeat("字", c.kept+1)) {
			t.Errorf("described_text %d: the recent chat reads %d characters of it, want %d", c.set, strings.Count(got, "字"), c.kept)
		}
	}
}

// voice is a voice message part named name, which the fake describer keys
// on, with the platform's transcript, if any.
func voice(name, transcript string) otogi.Part {
	return otogi.Part{ID: otogi.PartID(name), Kind: otogi.PartMedia, Media: &otogi.Media{Role: otogi.MediaVoice, MediaType: "audio/silk", Transcript: transcript,
		Source: otogi.MediaSource{Kind: otogi.MediaSourceOpaque, Locator: otogi.ProviderLocator{Data: []byte(name)}}}}
}

// A voice message the platform transcribed reads as its transcript, in the
// recent chat (cut to described_text) and in the message answered, with
// as_text or without, and is neither waited on nor fetched; one without a
// transcript is [voice], as before.
func TestVoiceTranscript(t *testing.T) {
	for _, asText := range []bool{true, false} {
		d := newDescriber()
		h := newHarnessConfig(t, map[string]any{mediasvc.Service: d}, Config{DescribedText: 6, Profiles: []ProfileConfig{mediaProfile(asText)}})
		h.mustHandle(h.message("Bob", "", withParts("", voice("v1", "今天去打 VRChat 吗"))))
		h.mustHandle(h.message("Bob", "", withParts("", voice("v2", ""))))
		h.mustHandle(h.message("Alice", "", withParts("otogi", voice("v3", "这是什么意思"))))
		req := h.llm["otogi"].requests[0]
		text := lastUserText(req)
		for _, want := range []string{`Bob: [voice: Said: "今天去打 V…"]`, "Bob: [voice]", `otogi` + "\n\n" + `[voice: Said: "这是什么意思"]`} {
			if !strings.Contains(text, want) {
				t.Errorf("as_text %v: request lacks %q:\n%s", asText, want, text)
			}
		}
		if n := mediaParts(req); n != 0 {
			t.Errorf("as_text %v: %d media parts, want the transcript alone", asText, n)
		}
		if slices.Contains(d.asked, "v3") || slices.Contains(d.asked, "v1") {
			t.Errorf("as_text %v: waited on a transcribed voice message: %v", asText, d.asked)
		}
		for _, r := range h.host.records {
			if r.Kind == "chat.media_skipped" {
				t.Errorf("as_text %v: chat.media_skipped %v", asText, r.Payload)
			}
		}
	}
}

// The judgment reads a transcribed voice message as its transcript, whether
// or not the agent reads descriptions, and does not wait for a description
// of it.
func TestJudgmentSeesTranscripts(t *testing.T) {
	for _, described := range []bool{false, true} {
		dec := &decider{scores: map[string]float64{"a1": 0.1}}
		media := newDescriber()
		p := judgedProfile("luna", 0.7)
		if described {
			p.Media = &MediaConfig{}
		}
		h := newHarnessWith(t, map[string]any{decision.Service: dec, mediasvc.Service: media}, p)
		h.mustHandle(h.message("Bob", "", withParts("", voice("v1", "有人在吗"))))
		state := dec.requests[0].State.(judgeState)
		if got := state.History[len(state.History)-1].Text; got != `[voice: Said: "有人在吗"]` {
			t.Fatalf("described %v: the judgment saw %q", described, got)
		}
		if len(media.asked) != 0 {
			t.Fatalf("described %v: the judgment waited on %v", described, media.asked)
		}
	}
}
