package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// The live tests call the real Gemini API. They run only when
// OTOGI_GEMINI_API_KEY is set, so they are not part of make quality:
//
//	OTOGI_GEMINI_API_KEY=... go test -run Live -v ./internal/adapter/gemini
//
// OTOGI_GEMINI_BASE_URL, OTOGI_GEMINI_MODEL, and OTOGI_GEMINI_IMAGE_MODEL
// override the defaults.
func liveProvider(t *testing.T) (*Provider, *recorder) {
	t.Helper()
	key := os.Getenv("OTOGI_GEMINI_API_KEY")
	if key == "" {
		t.Skip("OTOGI_GEMINI_API_KEY is not set")
	}
	rec := &recorder{}
	p, err := New(Config{APIKey: key, BaseURL: os.Getenv("OTOGI_GEMINI_BASE_URL"), Recorder: rec, Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	return p, rec
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// logRecords shows each call's record, for what the real API sent.
func logRecords(t *testing.T, rec *recorder) {
	t.Helper()
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, r := range rec.records {
		payload, _ := json.MarshalIndent(r.Payload, "", "  ")
		t.Logf("%s %s %v\n%s", r.Kind, r.Outcome, r.Duration, payload)
	}
}

// redSquare is a small PNG of a red square.
func redSquare(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for x := range 64 {
		for y := range 64 {
			img.Set(x, y, color.RGBA{R: 220, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestLiveConversationWithThinkingAndAnImage(t *testing.T) {
	p, rec := liveProvider(t)
	defer logRecords(t, rec)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	model := env("OTOGI_GEMINI_MODEL", "gemini-3.8-flash")
	turns := []llm.Turn{userTurn(
		otogi.Part{Kind: otogi.PartText, Text: "What colour is this image? Answer in one short sentence with the colour in bold."},
		otogi.Part{Kind: otogi.PartMedia, Media: &otogi.Media{Role: otogi.MediaImage, Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: redSquare(t)}}},
	)}
	var partials []time.Duration
	started := time.Now()
	first, err := p.Generate(ctx, llm.Request{Model: model, Thinking: "low", Turns: turns}, func(otogi.Message) error {
		partials = append(partials, time.Since(started))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("turn 1: %q finish=%s usage=%+v partials at %v", first.Markdown, first.Finish, first.Usage, partials)
	if first.Finish != llm.FinishStop || first.Markdown == "" || len(partials) == 0 {
		t.Fatalf("turn 1: %+v", first)
	}

	turns = append(turns, first.Turn, userTurn(otogi.Part{Kind: otogi.PartText, Text: "Now write a four-line poem about that colour, as a numbered list."}))
	partials = nil
	started = time.Now()
	second, err := p.Generate(ctx, llm.Request{Model: model, Thinking: "low", Turns: turns}, func(otogi.Message) error {
		partials = append(partials, time.Since(started))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("turn 2: %q finish=%s partials at %v", second.Markdown, second.Finish, partials)
	if second.Finish != llm.FinishStop || len(partials) < 2 {
		t.Fatalf("turn 2 did not stream: %d partials", len(partials))
	}
}

func TestLiveImageGenerationAndEdit(t *testing.T) {
	p, rec := liveProvider(t)
	defer logRecords(t, rec)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	model := env("OTOGI_GEMINI_IMAGE_MODEL", "gemini-nano-banana-2.1")
	turns := []llm.Turn{userTurn(otogi.Part{Kind: otogi.PartText, Text: "Draw a simple flat icon of a blue circle on a white background."})}
	first, err := p.Generate(ctx, llm.Request{Model: model, Turns: turns}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if images(first) == 0 {
		t.Fatalf("no image in %+v", first.Turn.Content)
	}
	turns = append(turns, first.Turn, userTurn(otogi.Part{Kind: otogi.PartText, Text: "Make the circle green, keep everything else."}))
	second, err := p.Generate(ctx, llm.Request{Model: model, Turns: turns}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if images(second) == 0 {
		t.Fatalf("no image in the edit: %+v", second.Turn.Content)
	}
}

func images(resp llm.Response) int {
	n := 0
	for _, p := range resp.Turn.Content.Parts {
		if p.Media != nil && len(p.Media.Source.Inline) > 0 {
			n++
		}
	}
	return n
}
