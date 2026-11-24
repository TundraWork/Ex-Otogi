package media

import (
	"context"
	"image"
	"image/color"
	"log/slog"
	"os"
	"testing"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/gemini"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// TestLiveDescribe has Gemini describe a picture and an animation. It runs
// only when OTOGI_GEMINI_API_KEY is set, so it is not part of make quality:
//
//	OTOGI_GEMINI_API_KEY=... go test -run Live -v ./modules/media
//
// OTOGI_GEMINI_MODEL overrides the first step's model.
func TestLiveDescribe(t *testing.T) {
	key := os.Getenv("OTOGI_GEMINI_API_KEY")
	if key == "" {
		t.Skip("OTOGI_GEMINI_API_KEY is not set")
	}
	storage, err := modulestore.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	h := &harness{t: t, host: &host{storage: storage, allowed: map[string]bool{chat: true}, services: map[string]any{}}, inv: &invocation{files: map[string][]byte{}}}
	provider, err := gemini.New(gemini.Config{APIKey: key, BaseURL: os.Getenv("OTOGI_GEMINI_BASE_URL"), Recorder: h.host, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	first := "gemini-3.5-flash-lite"
	if m := os.Getenv("OTOGI_GEMINI_MODEL"); m != "" {
		first = m
	}
	h.m, err = New(Config{Language: "Chinese", Steps: []Step{
		{Provider: "gemini", Model: first, Thinking: "minimal"},
		{Provider: "gemini", Model: "gemini-3.1-flash-lite", Thinking: "minimal"},
	}}, func(string) (llm.Provider, error) { return provider, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.m.Start(context.Background(), h.host); err != nil {
		t.Fatal(err)
	}

	still := h.image("https://live/still", pngOf(t, color.RGBA{255, 0, 0, 255}))
	var frames []gifFrame
	for x := range 4 {
		frames = append(frames, gifFrame{at: image.Pt(x, x), c: color.RGBA{0, 0, 255, 255}, delay: 20})
	}
	frames[0] = gifFrame{full: true, c: color.White, delay: 20}
	animated := h.image("https://live/anim", gifOf(t, frames))
	animated.Role, animated.MediaType = otogi.MediaAnimation, "image/gif"

	for _, md := range []otogi.Media{still, animated} {
		if err := h.handle(md); err != nil {
			t.Fatal(err)
		}
		d, ok, err := h.m.Lookup(context.Background(), from, md)
		if err != nil || !ok || d.Summary == "" {
			t.Fatalf("%s: Lookup = %+v, %v, %v; records %v", md.Source.URL, d, ok, err, h.host.kinds())
		}
		t.Logf("%s: %s %q, text %q", md.Source.URL, d.Kind, d.Summary, d.Text)
	}
}
