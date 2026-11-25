package chat

import (
	"context"
	"strings"
	"sync"
	"testing"
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
}

func (d *describer) Describe(ctx context.Context, _ otogi.Destination, md otogi.Media) (mediasvc.Description, error) {
	d.mu.Lock()
	name := string(md.Source.Locator.Data)
	d.asked = append(d.asked, name)
	desc, ok := d.kept[name]
	block, err := d.block, d.err
	d.mu.Unlock()
	switch {
	case ok:
		return desc, nil
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
	if took := time.Since(start); took > describeWait/2 {
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
		want  string
	}{
		{d: cat, limit: describedText, want: "[sticker: a cat eating watermelon, content]"},
		{d: mediasvc.Description{Kind: mediasvc.KindScreenshot, Summary: "a post", Text: "line one\nline  two"}, limit: describedText, want: `[screenshot: a post Text: "line one line two"]`},
		{d: mediasvc.Description{Kind: mediasvc.KindScreenshot, Summary: "a post", Text: "line one\nline  two"}, want: "[screenshot: a post Text: \"line one\nline  two\"]"},
		{d: mediasvc.Description{Kind: mediasvc.KindMeme, Summary: "s", Text: long}, limit: describedText, want: `[meme: s Text: "` + strings.Repeat("字", 300) + `…"]`},
		{md: otogi.Media{Duration: 72 * time.Second}, d: mediasvc.Description{Kind: mediasvc.KindVideo, Summary: "a song", Text: "la la"}, limit: describedText, want: `[video 1:12: a song Said: "la la"]`},
		{d: mediasvc.Description{Kind: mediasvc.KindVideo, Summary: "a song"}, want: "[video: a song]"},
		// A kept sticker reads as its number, whatever its kind.
		{d: mediasvc.Description{Kind: mediasvc.KindMeme, Summary: "Eku喵喵叫", Sticker: 3}, limit: describedText, want: "[sticker 3: Eku喵喵叫]"},
		{d: mediasvc.Description{Kind: mediasvc.KindScreenshot, Summary: "s", Text: "哈哈", Sticker: 3}, limit: describedText, want: `[sticker 3: s Text: "哈哈"]`},
	} {
		if got := descriptionText(c.md, c.d, c.limit); got != c.want {
			t.Errorf("descriptionText = %q, want %q", got, c.want)
		}
	}
}
