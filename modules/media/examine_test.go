package media

import (
	"context"
	"crypto/md5"
	"errors"
	"image"
	"image/color"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/settingstest"
	contract "github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// keeping is a harness whose module keeps pictures, up to limit MiB.
func keeping(t *testing.T, steps, limit int) *harness {
	t.Helper()
	return newHarnessWith(t, steps, func(c *Config) { c.Pictures = &PicturesConfig{MaxMB: limit} })
}

// originals counts the originals kept, and the chats they were seen in.
func (h *harness) originals() (pictures, chats int) {
	h.t.Helper()
	db := h.m.store.db
	if err := db.QueryRow(`SELECT count(*) FROM originals`).Scan(&pictures); err != nil {
		h.t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM original_chats`).Scan(&chats); err != nil {
		h.t.Fatal(err)
	}
	return pictures, chats
}

// inline is md with its bytes, as an agent fetched it again.
func inline(md otogi.Media, data []byte) otogi.Media {
	md.Source = otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: data}
	return md
}

// Without pictures, nothing is kept: a picture without its bytes cannot be
// examined, and its description has an ID all the same, for agents to fetch
// it again with.
func TestPicturesAreNotKeptByDefault(t *testing.T) {
	h := newHarness(t, 1)
	h.steps[0].answers = []answer{says(picture)}
	img := h.image("https://qq/a", pngOf(t, color.White))
	if err := h.handle(img); err != nil {
		t.Fatal(err)
	}
	if n, _ := h.originals(); n != 0 {
		t.Fatalf("%d originals kept", n)
	}
	if d, ok := h.lookup(img); !ok || len(d.ID) != 6 {
		t.Fatalf("Lookup = %+v, %v", d, ok)
	}
	if _, err := h.m.Examine(context.Background(), from, img, "what is it?"); !errors.Is(err, contract.ErrNotKept) {
		t.Fatalf("Examine = %v, want ErrNotKept", err)
	}
}

// With pictures, the pictures and animations described are kept, and
// examined from what is kept: the models get the picture and the question,
// in the language set, and the answer is recorded with where the picture
// came from. Videos are not kept.
func TestPicturesAreKeptAndExamined(t *testing.T) {
	h := keeping(t, 1, 512)
	img := h.image("https://qq/a", pngOf(t, color.White))
	anim := h.image("https://qq/b", gifOf(t, []gifFrame{{full: true, c: red}, {at: image.Pt(0, 0), c: blue}, {at: image.Pt(1, 1), c: green}, {at: image.Pt(2, 2), c: yellow}}))
	anim.Role, anim.MediaType = otogi.MediaAnimation, "image/gif"
	mp4 := append([]byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isommp41"), make([]byte, 64)...)
	h.inv.files["https://qq/v"] = mp4
	video := otogi.Media{Role: otogi.MediaVideo, MediaType: "video/mp4", Source: otogi.MediaSource{Kind: otogi.MediaSourceExternal, URL: "https://qq/v"}}
	h.steps[0].answers = []answer{says(picture), says(picture), says("description: 有人在弹琴\ntext:")}
	for _, md := range []otogi.Media{img, anim, video} {
		if err := h.handle(md); err != nil {
			t.Fatal(err)
		}
	}
	if n, chats := h.originals(); n != 2 || chats != 2 {
		t.Fatalf("%d originals in %d chats, want the picture and the animation", n, chats)
	}
	fetched := h.inv.fetches()
	h.steps[0].answers = []answer{says("一只白色的猫"), says("它在眨眼")}
	answer, err := h.m.Examine(context.Background(), from, img, "猫是什么颜色？")
	if err != nil || answer != "一只白色的猫" {
		t.Fatalf("Examine = %q, %v", answer, err)
	}
	req := h.steps[0].requests[len(h.steps[0].requests)-1]
	parts := req.Turns[0].Content.Parts
	if !strings.Contains(req.System, "Chinese") || !strings.Contains(req.System, "question") || len(parts) != 2 ||
		parts[0].Media == nil || parts[1].Text != "Question: 猫是什么颜色？" {
		t.Fatalf("request = %+v", req)
	}
	rec := h.host.last("media.examined")
	if rec["origin"] != "kept" || rec["question"] != "猫是什么颜色？" || rec["answer"] != "一只白色的猫" || rec["step"] != 1 {
		t.Fatalf("media.examined = %v", rec)
	}
	// An animation is asked about as it is described: its frames after the
	// note on them, then the question.
	if _, err := h.m.Examine(context.Background(), from, anim, "它在做什么？"); err != nil {
		t.Fatal(err)
	}
	parts = h.steps[0].requests[len(h.steps[0].requests)-1].Turns[0].Content.Parts
	if len(parts) < 3 || !strings.Contains(parts[0].Text, "frames") || parts[len(parts)-1].Text != "Question: 它在做什么？" {
		t.Fatalf("parts = %+v", parts)
	}
	if h.inv.fetches() != fetched {
		t.Fatal("examining what is kept fetched it again")
	}
	if _, err := h.m.Examine(context.Background(), from, video, "弹的什么？"); !errors.Is(err, contract.ErrNotKept) {
		t.Fatalf("Examine of a video not fetched = %v, want ErrNotKept", err)
	}
}

// A picture described before, as a sticker sent again, is fetched once to
// keep it, not described again; once kept, it is only marked seen.
func TestPicturesDescribedBeforeAreKept(t *testing.T) {
	h := keeping(t, 1, 512)
	data := pngOf(t, color.White)
	sum := md5.Sum(data)
	if err := h.m.store.put(context.Background(), key{Algorithm: "md5", Digest: sum[:]}, contract.Description{Kind: contract.KindMeme, Summary: "一只猫"}, "a/model-a", time.Now()); err != nil {
		t.Fatal(err)
	}
	img := h.image("https://qq/a", data)
	for range 2 {
		if err := h.handle(img); err != nil {
			t.Fatal(err)
		}
	}
	if h.steps[0].calls() != 0 || h.inv.fetches() != 1 {
		t.Fatalf("calls = %d, fetches = %d, want none and one", h.steps[0].calls(), h.inv.fetches())
	}
	if n, _ := h.originals(); n != 1 {
		t.Fatalf("%d originals", n)
	}
}

// A picture is examined only from the chats it was seen in.
func TestOriginalsBelongToTheirChats(t *testing.T) {
	h := keeping(t, 1, 512)
	h.host.allowed["qq:group-2"] = true
	h.steps[0].answers = []answer{says(picture)}
	img := h.image("https://qq/a", pngOf(t, color.White))
	if err := h.handle(img); err != nil {
		t.Fatal(err)
	}
	other := otogi.Destination{Platform: "qq", Account: "bot", Conversation: "group-2"}
	if _, err := h.m.Examine(context.Background(), other, img, "what is it?"); !errors.Is(err, contract.ErrNotKept) {
		t.Fatalf("Examine from another chat = %v, want ErrNotKept", err)
	}
}

// Original gives back what is kept of a picture, with its bytes and type,
// only to the chats it was seen in; without pictures, or for a chat off the
// describe list, there is none.
func TestOriginal(t *testing.T) {
	h := keeping(t, 1, 512)
	h.host.allowed["qq:group-2"] = true
	h.steps[0].answers = []answer{says(picture)}
	data := pngOf(t, color.White)
	img := h.image("https://qq/a", data)
	if err := h.handle(img); err != nil {
		t.Fatal(err)
	}
	got, err := h.m.Original(context.Background(), from, img)
	if err != nil || got.Source.Kind != otogi.MediaSourceInline || string(got.Source.Inline) != string(data) ||
		got.MediaType != "image/png" || got.ByteSize != int64(len(data)) || got.Role != img.Role {
		t.Fatalf("Original = %+v, %v", got, err)
	}
	other := otogi.Destination{Platform: "qq", Account: "bot", Conversation: "group-2"}
	if _, err := h.m.Original(context.Background(), other, img); !errors.Is(err, contract.ErrNotKept) {
		t.Fatalf("Original from another chat = %v, want ErrNotKept", err)
	}
	off := otogi.Destination{Platform: "qq", Account: "bot", Conversation: "group-9"}
	if _, err := h.m.Original(context.Background(), off, img); !errors.Is(err, contract.ErrNotAllowed) {
		t.Fatalf("Original off the describe list = %v, want ErrNotAllowed", err)
	}
	plain := newHarness(t, 1)
	plain.steps[0].answers = []answer{says(picture)}
	img = plain.image("https://qq/a", data)
	if err := plain.handle(img); err != nil {
		t.Fatal(err)
	}
	if _, err := plain.m.Original(context.Background(), from, img); !errors.Is(err, contract.ErrNotKept) {
		t.Fatalf("Original without pictures = %v, want ErrNotKept", err)
	}
}

// Media given with its bytes is examined as given: fetched again, or a kept
// sticker's picture, which the record tells apart. A chat off the describe
// list, or media that is not a picture or video, is refused.
func TestExamineGivenBytes(t *testing.T) {
	h := newHarness(t, 1)
	ctx := context.Background()
	data := pngOf(t, color.White)
	h.steps[0].answers = []answer{says("白的"), says("一张表情")}
	if _, err := h.m.Examine(ctx, from, inline(h.image("https://qq/a", data), data), "什么颜色？"); err != nil {
		t.Fatal(err)
	}
	if rec := h.host.last("media.examined"); rec["origin"] != "fetched" {
		t.Fatalf("media.examined = %v", rec)
	}
	st := pngOf(t, color.Black)
	sum := md5.Sum(st)
	if _, _, _, err := h.m.store.addSticker(ctx, sticker{SHA256: []byte("not this"), MediaType: "image/png", Bytes: st}, "", "qq:"+operator, time.Now()); err != nil {
		t.Fatal(err)
	}
	stMedia := otogi.Media{Role: otogi.MediaImage, MediaType: "image/png", Hashes: []otogi.MediaHash{{Algorithm: "md5", Value: sum[:]}}, Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: st}}
	if _, err := h.m.Examine(ctx, from, stMedia, "这是什么？"); err != nil {
		t.Fatal(err)
	}
	if rec := h.host.last("media.examined"); rec["origin"] != "sticker" {
		t.Fatalf("media.examined = %v", rec)
	}
	elsewhere := otogi.Destination{Platform: "qq", Account: "bot", Conversation: "group-9"}
	if _, err := h.m.Examine(ctx, elsewhere, inline(h.image("https://qq/a", data), data), "?"); !errors.Is(err, contract.ErrNotAllowed) {
		t.Fatalf("Examine off the list = %v", err)
	}
	doc := otogi.Media{Role: otogi.MediaDocument, Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: []byte("%PDF")}}
	if _, err := h.m.Examine(ctx, from, doc, "?"); !errors.Is(err, contract.ErrNotDescribed) {
		t.Fatalf("Examine of a file = %v", err)
	}
}

// A model that fails is recorded with the question, and the next one asked;
// when every one fails, Examine says the service is unavailable.
func TestExamineTriesTheNextModel(t *testing.T) {
	h := newHarness(t, 2)
	data := pngOf(t, color.White)
	img := inline(h.image("https://qq/a", data), data)
	h.steps[0].answers = []answer{{err: errors.New("503")}, says("   ")}
	h.steps[1].answers = []answer{says("白的"), {err: errors.New("503")}}
	answer, err := h.m.Examine(context.Background(), from, img, "什么颜色？")
	if err != nil || answer != "白的" {
		t.Fatalf("Examine = %q, %v", answer, err)
	}
	if rec := h.host.last("media.failed"); rec["question"] != "什么颜色？" || rec["step"] != 1 {
		t.Fatalf("media.failed = %v", rec)
	}
	if rec := h.host.last("media.examined"); rec["step"] != 2 {
		t.Fatalf("media.examined = %v", rec)
	}
	if _, err := h.m.Examine(context.Background(), from, img, "什么颜色？"); !errors.Is(err, otogi.ErrUnavailable) {
		t.Fatalf("Examine = %v, want ErrUnavailable", err)
	}
}

// The defaults the panel shows are those the module takes for unset fields.
func TestSchemaDefaultsAreTheDefaults(t *testing.T) {
	unset := Config{Steps: []Step{{Provider: "a", Model: "m"}}, Pictures: &PicturesConfig{}}
	typed := settingstest.WithDefaults(t, unset)
	if unset.keepBytes() != typed.keepBytes() || typed.keepBytes() != defaultPicturesMB<<20 || typed.Language != defaultLanguage ||
		unset.largestBytes() != typed.largestBytes() || typed.largestBytes() != 25<<20 {
		t.Fatalf("schema defaults give %+v, %d and %d bytes; unset fields %d and %d bytes", typed, typed.keepBytes(), typed.largestBytes(), unset.keepBytes(), unset.largestBytes())
	}
}

// Pruning deletes the originals whose descriptions are gone, then, past the
// limit, the longest unseen until the rest fit; with no pictures kept, all.
func TestPruneOriginals(t *testing.T) {
	h := keeping(t, 1, 1)
	ctx := context.Background()
	s := h.m.store
	day := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	keys := make([]key, 4)
	for i := range keys {
		keys[i] = key{Algorithm: "md5", Digest: []byte{byte(i), 1, 2}}
		if err := s.put(ctx, keys[i], contract.Description{Kind: contract.KindPhoto, Summary: "x"}, "m", day); err != nil {
			t.Fatal(err)
		}
		// 400 KiB each, seen an hour apart: the first is the longest unseen.
		if err := s.keepOriginal(ctx, keys[i], chat, "image/png", make([]byte, 400<<10), day.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`DELETE FROM descriptions WHERE digest = ?`, keys[3].Digest); err != nil {
		t.Fatal(err)
	}
	n, err := s.pruneOriginals(ctx, 1<<20)
	if err != nil || n != 2 {
		t.Fatalf("pruned %d, %v; want the one without a description and the longest unseen", n, err)
	}
	for i, want := range []bool{false, true, true, false} {
		if _, _, ok, err := s.original(ctx, keys[i], chat); err != nil || ok != want {
			t.Fatalf("original %d kept = %v, %v; want %v", i, ok, err, want)
		}
	}
	if n, err := s.pruneOriginals(ctx, 0); err != nil || n != 2 {
		t.Fatalf("pruned %d, %v; want the rest", n, err)
	}
	if pictures, chats := h.originals(); pictures != 0 || chats != 0 {
		t.Fatalf("%d originals in %d chats left", pictures, chats)
	}
}
