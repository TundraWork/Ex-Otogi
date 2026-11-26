package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	mediasvc "github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// stickerBox is a media.stickers service keeping list; Picture fails for
// the numbers in gone.
type stickerBox struct {
	list []mediasvc.Sticker
	gone map[int]bool
}

func (s *stickerBox) List(context.Context) ([]mediasvc.Sticker, error) { return s.list, nil }

func (s *stickerBox) Picture(_ context.Context, id int) (otogi.Media, mediasvc.Sticker, error) {
	for _, st := range s.list {
		if st.ID == id && !s.gone[id] {
			data := []byte(fmt.Sprintf("GIF89a %d", id))
			return otogi.Media{Role: otogi.MediaAnimation, MediaType: "image/gif", ByteSize: int64(len(data)), Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: data}}, st, nil
		}
	}
	return otogi.Media{}, mediasvc.Sticker{}, mediasvc.ErrNoSticker
}

func newStickerBox() *stickerBox {
	return &stickerBox{list: []mediasvc.Sticker{{ID: 1, Kind: mediasvc.KindSticker, Text: "a cat\nwaving"}, {ID: 3, Kind: mediasvc.KindSticker, Text: "a dog"}}}
}

// stickerProfile is an agent that sends stickers.
func stickerProfile() ProfileConfig {
	return ProfileConfig{Name: "otogi", Label: "Otogi", Keywords: []string{"otogi"}, Provider: "otogi", Model: "m", Media: &MediaConfig{Stickers: []int{1, 3}}}
}

func newStickerHarness(t *testing.T, box *stickerBox) *harness {
	t.Helper()
	return newHarnessWith(t, map[string]any{mediasvc.Service: newDescriber(), mediasvc.StickersService: box}, stickerProfile())
}

// picks is a model round saying text, then calling send_sticker for each
// number.
func picks(text string, numbers ...int) llm.Response {
	r := llm.Response{Turn: llm.Turn{Role: llm.RoleModel}, Finish: llm.FinishToolCalls}
	if text != "" {
		r.Turn.Content = otogi.TextMessage(text)
	}
	for i, n := range numbers {
		r.Turn.Calls = append(r.Turn.Calls, llm.Call{ID: fmt.Sprintf("s%d", i), Name: stickerTool, Args: json.RawMessage(fmt.Sprintf(`{"number":%d}`, n))})
	}
	return r
}

func lastReplyRecord(t *testing.T, h *harness) (otogi.Record, map[string]any) {
	t.Helper()
	for i := len(h.host.records) - 1; i >= 0; i-- {
		if r := h.host.records[i]; r.Kind == "chat.reply" {
			return r, r.Payload.(map[string]any)
		}
	}
	t.Fatal("no chat.reply")
	return otogi.Record{}, nil
}

func hasTool(req llm.Request, name string) bool {
	for _, t := range req.Tools {
		if t.Function != nil && t.Function.Name == name {
			return true
		}
	}
	return false
}

// An agent with media.stickers gets the list after its system prompt, and
// send_sticker; a call ends the reply, and the sticker follows the words as
// a message of its own, in the sticker slot, replying to the message,
// without the agent's label.
func TestStickerFollowsTheReply(t *testing.T) {
	h := newStickerHarness(t, newStickerBox())
	if req := h.m.Descriptor().Requires; strings.Join(req, ",") != mediasvc.Service+","+mediasvc.StickersService {
		t.Fatalf("requires %v", req)
	}
	p := h.llm["otogi"]
	p.responses = []llm.Response{picks("Here.", 3)}
	ask := h.message("Alice", "otogi cheer me up")
	h.mustHandle(ask)

	if len(p.requests) != 1 || !hasTool(p.requests[0], stickerTool) {
		t.Fatalf("requests %d, tools %+v", len(p.requests), p.requests[0].Tools)
	}
	if got := p.requests[0].System; !strings.HasPrefix(got, "Stickers you can send with send_sticker:\n1. a cat waving\n3. a dog\n\nYou are otogi") {
		t.Fatalf("system %q", got)
	}
	if got := lastUserText(p.requests[0]); strings.Contains(got, "Stickers you can send") {
		t.Fatalf("the list is in the turn too: %q", got)
	}
	if len(h.streams) != 1 || h.streams[0].final.PlainText() != "Otogi\nHere." {
		t.Fatalf("streams %d", len(h.streams))
	}
	if len(h.sends) != 1 {
		t.Fatalf("sends %+v", h.sends)
	}
	s := h.sends[0]
	if s.slot != "sticker" || len(s.msg.Parts) != 1 || s.msg.Parts[0].Media == nil || string(s.msg.Parts[0].Media.Source.Inline) != "GIF89a 3" ||
		s.msg.PlainText() != "" || len(s.msg.Relations) != 1 || s.msg.Relations[0].Target.SourceEvent != ask.SourceID {
		t.Fatalf("sticker message %+v", s)
	}
	r, payload := lastReplyRecord(t, h)
	if r.Outcome != "ok" || *payload["sticker"].(*sentSticker) != (sentSticker{ID: 3, Text: "a dog", Listed: true, Ref: "agent-2"}) || len(payload["messages"].([]otogi.EventRef)) != 2 {
		t.Fatalf("record %v %+v", r.Outcome, payload)
	}

	// The sticker comes back from the platform; the recent messages read it
	// as what it shows.
	h.mustHandle(h.message("Otogi", "", withParts("", photo("sticker")), func(e *otogi.Event) {
		e.SourceID, e.Direction = "agent-2", otogi.DirectionOutgoing
	}))
	p.responses = []llm.Response{answer("A dog.")}
	h.mustHandle(h.message("Bob", "otogi what was that?"))
	if got := lastUserText(p.requests[1]); !strings.Contains(got, "Otogi (this account): [sticker: a dog]") {
		t.Fatalf("recent messages %q", got)
	}

	// Replying to the sticker continues the thread, which ends with the call
	// and its result; the list did not go into it.
	p.responses = []llm.Response{answer("Yes.")}
	h.mustHandle(h.message("Alice", "cute!", replyingTo("agent-2")))
	turns := p.requests[2].Turns
	if len(turns) != 4 || turns[1].Calls[0].Name != stickerTool || turns[2].Role != llm.RoleTool || string(turns[2].Results[0].Output) != `{"sent":true}` {
		t.Fatalf("thread %+v", turns)
	}
	if strings.Contains(turns[0].Content.PlainText(), "Stickers you can send") {
		t.Fatalf("the list went into the thread: %q", turns[0].Content.PlainText())
	}
}

// A reply that is only a sticker shows nothing else: no empty label, no "I
// have nothing to add."
func TestStickerAlone(t *testing.T) {
	h := newStickerHarness(t, newStickerBox())
	h.llm["otogi"].responses = []llm.Response{picks("", 1)}
	h.mustHandle(h.message("Alice", "otogi"))
	if len(h.streams) != 1 || len(h.streams[0].final.Parts) != 0 || len(h.sends) != 1 {
		t.Fatalf("streams %d, final %q, sends %d", len(h.streams), h.streams[0].final.PlainText(), len(h.sends))
	}
}

// One sticker per reply: a second pick, in the same round or later, is
// refused without an error, so the reply still ends. A number no sticker
// has is an error the model sees, and it answers again.
func TestOneStickerPerReply(t *testing.T) {
	h := newStickerHarness(t, newStickerBox())
	p := h.llm["otogi"]
	p.responses = []llm.Response{picks("Two!", 1, 3)}
	h.mustHandle(h.message("Alice", "otogi two please"))
	if len(p.requests) != 1 || len(h.sends) != 1 || string(h.sends[0].msg.Parts[0].Media.Source.Inline) != "GIF89a 1" {
		t.Fatalf("requests %d, sends %+v", len(p.requests), h.sends)
	}

	p.responses, p.requests, h.streams, h.sends = []llm.Response{picks("", 9), answer("Never mind.")}, nil, nil, nil
	h.mustHandle(h.message("Alice", "otogi number nine"))
	if len(p.requests) != 2 || len(h.sends) != 0 || h.streams[0].final.PlainText() != "Otogi\nNever mind." {
		t.Fatalf("requests %d, sends %d, reply %q", len(p.requests), len(h.sends), h.streams[0].final.PlainText())
	}
	if res := p.requests[1].Turns[len(p.requests[1].Turns)-1].Results[0]; res.Error != "there is no sticker 9" {
		t.Fatalf("result %+v", res)
	}
	if _, payload := lastReplyRecord(t, h); payload["sticker"] != nil {
		t.Fatalf("a reply without a sticker recorded one: %+v", payload)
	}
}

// A sticker that cannot be sent makes the reply an error, recorded with the
// sticker and no ref; the words went out already.
func TestStickerFailures(t *testing.T) {
	box := newStickerBox()
	h := newStickerHarness(t, box)
	p := h.llm["otogi"]

	box.gone = map[int]bool{3: true} // removed since the list was read
	p.responses = []llm.Response{picks("Here.", 3)}
	if err := h.handle(h.message("Alice", "otogi")); !errors.Is(err, mediasvc.ErrNoSticker) {
		t.Fatalf("err %v", err)
	}
	if r, payload := lastReplyRecord(t, h); r.Outcome != "error" || *payload["sticker"].(*sentSticker) != (sentSticker{ID: 3, Text: "a dog", Listed: true}) || h.streams[0].final.PlainText() != "Otogi\nHere." {
		t.Fatalf("record %v %+v", r.Outcome, payload)
	}

	h.sendErr, h.streams = errors.New("upload refused"), nil
	p.responses = []llm.Response{picks("", 1)}
	if err := h.handle(h.message("Alice", "otogi")); err == nil || !strings.Contains(err.Error(), "upload refused") {
		t.Fatalf("err %v", err)
	}
	if r, payload := lastReplyRecord(t, h); r.Outcome != "error" || payload["sticker"].(*sentSticker).Ref != "" {
		t.Fatalf("record %v %+v", r.Outcome, payload)
	}
}

// Without media.stickers, or with no stickers kept, the agent gets neither
// the list nor send_sticker.
func TestNoStickers(t *testing.T) {
	h := newHarnessWith(t, map[string]any{mediasvc.Service: newDescriber()}, mediaProfile(false))
	h.mustHandle(h.message("Alice", "otogi hi"))
	if req := h.llm["otogi"].requests[0]; hasTool(req, stickerTool) || strings.Contains(req.System+lastUserText(req), "Stickers you can send") {
		t.Fatalf("an agent without stickers got them: %+v", req.Tools)
	}

	h = newStickerHarness(t, &stickerBox{})
	h.mustHandle(h.message("Alice", "otogi hi"))
	if req := h.llm["otogi"].requests[0]; hasTool(req, stickerTool) || strings.Contains(req.System+lastUserText(req), "Stickers you can send") {
		t.Fatalf("an empty list was offered: %+v", req.Tools)
	}

	// None of its numbers kept any more: no list and no send_sticker either.
	p := stickerProfile()
	p.Media.Stickers = []int{4, 5}
	h = newHarnessWith(t, map[string]any{mediasvc.Service: newDescriber(), mediasvc.StickersService: newStickerBox()}, p)
	h.mustHandle(h.message("Alice", "otogi hi"))
	if req := h.llm["otogi"].requests[0]; hasTool(req, stickerTool) || strings.Contains(req.System+lastUserText(req), "Stickers you can send") {
		t.Fatalf("an agent whose stickers are all gone got them: %+v", req.Tools)
	}
}

// The list is the agent's own: in the order it gives, each number once,
// skipping those no longer kept, and not the stickers it was not given. It
// may still send a kept sticker that is not on its list, by number; the
// record says it was not listed.
func TestStickersAreTheAgents(t *testing.T) {
	box := newStickerBox()
	box.list = append(box.list, mediasvc.Sticker{ID: 5, Kind: mediasvc.KindMeme, Text: "a frog"})
	p := stickerProfile()
	p.Media.Stickers = []int{5, 2, 1, 5}
	h := newHarnessWith(t, map[string]any{mediasvc.Service: newDescriber(), mediasvc.StickersService: box}, p)
	l := h.llm["otogi"]
	l.responses = []llm.Response{picks("This one.", 3)}
	h.mustHandle(h.message("Alice", "otogi send number 3"))

	if got := l.requests[0].System; !strings.HasPrefix(got, "Stickers you can send with send_sticker:\n5. a frog\n1. a cat waving\n\nYou are") {
		t.Fatalf("system %q", got)
	}
	if len(h.sends) != 1 || string(h.sends[0].msg.Parts[0].Media.Source.Inline) != "GIF89a 3" {
		t.Fatalf("sends %+v", h.sends)
	}
	if _, payload := lastReplyRecord(t, h); *payload["sticker"].(*sentSticker) != (sentSticker{ID: 3, Text: "a dog", Listed: false, Ref: "agent-2"}) {
		t.Fatalf("record %+v", payload["sticker"])
	}
}

// With other tools, a reply is at most three messages: the words before the
// first call, the words after, and the sticker.
func TestStickerAfterOtherTools(t *testing.T) {
	storage, err := modulestore.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	shout := llm.NewTool(llm.Function{Name: "shout", Description: "shouts"}, func(context.Context, otogi.Event, json.RawMessage) (any, error) { return "HI", nil })
	h := &harness{t: t, host: &host{storage: storage, lists: map[[2]string]string{{"otogi", group}: "test", {"tools:fun", group}: "test"}, services: map[string]any{
		"llm.tools/fun": []llm.Tool{shout}, mediasvc.Service: newDescriber(), mediasvc.StickersService: newStickerBox(),
	}}, llm: map[string]*provider{}, now: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)}
	call := llm.Response{Turn: llm.Turn{Role: llm.RoleModel, Content: otogi.TextMessage("Let me shout."), Calls: []llm.Call{{ID: "c1", Name: "shout", Args: json.RawMessage(`"hi"`)}}}, Finish: llm.FinishToolCalls}
	p := &provider{responses: []llm.Response{call, picks("Shouted.", 1)}}
	if h.m, err = New(Config{Tools: []string{"fun"}, Profiles: []ProfileConfig{stickerProfile()}}, func(string) (llm.Provider, error) { return p, nil }, nil); err != nil {
		t.Fatal(err)
	}
	h.m.now = h.clock
	if err := h.m.Start(context.Background(), h.host); err != nil {
		t.Fatal(err)
	}
	h.mustHandle(h.message("Alice", "otogi shout"))
	if len(p.requests) != 2 || len(h.streams) != 2 || h.streams[0].final.PlainText() != "Otogi\nLet me shout." || h.streams[1].final.PlainText() != "Otogi\nShouted." || len(h.sends) != 1 {
		t.Fatalf("requests %d, streams %d, sends %d", len(p.requests), len(h.streams), len(h.sends))
	}
	if _, payload := lastReplyRecord(t, h); len(payload["messages"].([]otogi.EventRef)) != 3 {
		t.Fatalf("messages %v", payload["messages"])
	}
}

// packBox keeps stickers in packs: 1 and 3 in cats, 3 and 4 in dogs, 5 in
// none.
func packBox() *stickerBox {
	return &stickerBox{list: []mediasvc.Sticker{
		{ID: 1, Kind: mediasvc.KindSticker, Text: "a cat", Packs: []string{"cats"}},
		{ID: 3, Kind: mediasvc.KindSticker, Text: "a cat and a dog", Packs: []string{"cats", "dogs"}},
		{ID: 4, Kind: mediasvc.KindSticker, Text: "a dog", Packs: []string{"dogs"}},
		{ID: 5, Kind: mediasvc.KindMeme, Text: "a frog"},
	}}
}

// Packs give an agent their stickers after its own, in its order of packs
// and each pack's by number, each sticker once; a pack not there gives
// none. A sticker put in a pack is in the list of the next reply.
func TestStickerPacksGiveStickers(t *testing.T) {
	box := packBox()
	p := stickerProfile()
	p.Media.Stickers = []int{4}
	p.Media.Packs = []string{"gone", "dogs", "cats"}
	h := newHarnessWith(t, map[string]any{mediasvc.Service: newDescriber(), mediasvc.StickersService: box}, p)
	l := h.llm["otogi"]
	h.mustHandle(h.message("Alice", "otogi hi"))
	if got := l.requests[0].System; !strings.HasPrefix(got, "Stickers you can send with send_sticker:\n4. a dog\n3. a cat and a dog\n1. a cat\n\nYou are") {
		t.Fatalf("system %q", got)
	}
	box.list = append(box.list, mediasvc.Sticker{ID: 6, Text: "a puppy", Packs: []string{"dogs"}})
	h.mustHandle(h.message("Alice", "otogi again"))
	if got := l.requests[1].System; !strings.HasPrefix(got, "Stickers you can send with send_sticker:\n4. a dog\n3. a cat and a dog\n6. a puppy\n1. a cat\n\nYou are") {
		t.Fatalf("system after a sticker went in dogs %q", got)
	}

	// Packs alone give stickers, and send_sticker with them.
	p = stickerProfile()
	p.Media.Stickers, p.Media.Packs = nil, []string{"cats"}
	h = newHarnessWith(t, map[string]any{mediasvc.Service: newDescriber(), mediasvc.StickersService: packBox()}, p)
	h.mustHandle(h.message("Alice", "otogi hi"))
	if req := h.llm["otogi"].requests[0]; !hasTool(req, stickerTool) || !strings.HasPrefix(req.System, "Stickers you can send with send_sticker:\n1. a cat\n3. a cat and a dog\n\n") {
		t.Fatalf("packs alone: tools %+v, system %q", req.Tools, req.System)
	}
}

// max_stickers bounds the list, those first in it kept, and chat.reply
// says how many were left out; a list within it records nothing of it.
func TestMaxStickers(t *testing.T) {
	p := stickerProfile()
	p.Media.Stickers, p.Media.Packs, p.Media.MaxStickers = []int{5}, []string{"cats", "dogs"}, ptr(2)
	h := newHarnessWith(t, map[string]any{mediasvc.Service: newDescriber(), mediasvc.StickersService: packBox()}, p)
	l := h.llm["otogi"]
	h.mustHandle(h.message("Alice", "otogi hi"))
	if got := l.requests[0].System; !strings.HasPrefix(got, "Stickers you can send with send_sticker:\n5. a frog\n1. a cat\n\nYou are") {
		t.Fatalf("system %q", got)
	}
	if _, payload := lastReplyRecord(t, h); payload["stickers_hidden"] != 2 {
		t.Fatalf("stickers_hidden = %v, want 2 (3 and 4)", payload["stickers_hidden"])
	}

	p.Media.MaxStickers = ptr(4)
	h = newHarnessWith(t, map[string]any{mediasvc.Service: newDescriber(), mediasvc.StickersService: packBox()}, p)
	h.mustHandle(h.message("Alice", "otogi hi"))
	if _, payload := lastReplyRecord(t, h); payload["stickers_hidden"] != nil {
		t.Fatalf("a list within max_stickers recorded stickers_hidden %v", payload["stickers_hidden"])
	}
}

// The list sits between the persona and what changes with the chat and the
// minute, so what comes before it is the same in every chat and request.
func TestStickerListIsCachedWithThePersona(t *testing.T) {
	p := stickerProfile()
	p.System = "You are a cheerful cat."
	h := newHarnessWith(t, map[string]any{mediasvc.Service: newDescriber(), mediasvc.StickersService: newStickerBox()}, p)
	l := h.llm["otogi"]
	h.mustHandle(h.message("Alice", "otogi hi"))
	h.mustHandle(h.message("Bob", "otogi hello", func(e *otogi.Event) { e.Conversation.Title, e.Conversation.Members = "Another", 99 }))
	const prefix = "You are a cheerful cat.\n\nStickers you can send with send_sticker:\n1. a cat waving\n3. a dog\n\nYou are otogi, talking in "
	for i, req := range l.requests {
		if !strings.HasPrefix(req.System, prefix) {
			t.Fatalf("request %d system %q", i, req.System)
		}
	}
	if l.requests[0].System == l.requests[1].System {
		t.Fatal("both chats got the same system prompt; the test is not telling chats apart")
	}
}
