package chat

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"os"
	"testing"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/gemini"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// TestLiveConversation runs the module against the real Gemini API; it runs
// only when OTOGI_GEMINI_API_KEY is set:
//
//	OTOGI_GEMINI_API_KEY=... go test -run Live -v ./modules/chat
func TestLiveConversation(t *testing.T) {
	key := os.Getenv("OTOGI_GEMINI_API_KEY")
	if key == "" {
		t.Skip("OTOGI_GEMINI_API_KEY is not set")
	}
	g, err := gemini.New(gemini.Config{APIKey: key, BaseURL: os.Getenv("OTOGI_GEMINI_BASE_URL"), Recorder: otogi.NopRecorder{}, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, ProfileConfig{Name: "otogi", Label: "🤖 Otogi", Keywords: []string{"otogi"}, Chats: []string{group}, Provider: "gemini", Model: "gemini-3.8-flash", Thinking: "low",
		System: "You are Otogi, a cheerful member of this chat. Answer in one or two short sentences."})
	h.m.profiles[0].provider = g

	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for x := range 32 {
		for y := range 32 {
			img.Set(x, y, color.RGBA{G: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	h.mustHandle(h.message("Bob", "the meeting moved to 3pm, bring the green folder"))
	photo := otogi.Part{ID: "p", Kind: otogi.PartMedia, Media: &otogi.Media{Role: otogi.MediaImage, MediaType: "image/png", Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: buf.Bytes()}}}
	ask := h.message("Alice", "otogi, when is the meeting, and what colour is this?", func(e *otogi.Event) {
		e.Message.Parts = append(e.Message.Parts, photo)
		e.Message.Roots = append(e.Message.Roots, "p")
	})
	h.mustHandle(ask)
	t.Logf("reply 1 (%d updates): %q", len(h.streams[0].updates), h.streams[0].final.PlainText())
	h.mustHandle(h.message("Alice", "and who told us?", replyingTo("agent-1")))
	t.Logf("reply 2 (%d updates): %q", len(h.streams[1].updates), h.streams[1].final.PlainText())
	for _, r := range h.host.records {
		if p, ok := r.Payload.(map[string]any); ok && r.Kind == "chat.reply" {
			t.Logf("%s %s trigger=%v finish=%v usage=%+v", r.Kind, r.Outcome, p["trigger"], p["finish"], p["usage"].(llm.Usage))
		}
	}
}
