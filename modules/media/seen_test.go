package media

import (
	"context"
	"errors"
	"image/color"
	"slices"
	"testing"

	contract "github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// telegramImage is a picture known only by where it is, as Telegram's are
// until fetched.
func (h *harness) telegramImage(url string, data []byte) otogi.Media {
	h.inv.files[url] = data
	return otogi.Media{Role: otogi.MediaImage, MediaType: "image/png", ByteSize: int64(len(data)), Source: otogi.MediaSource{Kind: otogi.MediaSourceExternal, URL: url}}
}

func (h *harness) lookup(md otogi.Media) (contract.Description, bool) {
	h.t.Helper()
	d, ok, err := h.host.services[contract.Service].(contract.Describer).Lookup(context.Background(), from, md)
	if err != nil {
		h.t.Fatal(err)
	}
	return d, ok
}

func (h *harness) count(kind string) int {
	h.host.mu.Lock()
	defer h.host.mu.Unlock()
	n := 0
	for _, r := range h.host.records {
		if r.Kind == kind {
			n++
		}
	}
	return n
}

// A kept sticker is known wherever it appears, on QQ by the MD5 its file is
// named by and on Telegram by the SHA-256 of what was fetched, without asking
// a model: it reads as its number and its text, the note else the summary it
// was kept with, read each time, and its description's kind and text while
// there is one. Handling a message records it as described from the sticker;
// looking it up does not.
func TestKeptStickerIsKnown(t *testing.T) {
	h := newHarness(t, 1)
	h.steps[0].answers = []answer{says(picture), says(picture)}
	png := pngOf(t, color.Black)
	h.sticker(stickerCall{args: "add", media: []otogi.Media{h.image("https://qq/1", png)}})
	calls := h.steps[0].calls()

	qq := h.image("https://qq/2", png)
	if err := h.handle(qq); err != nil {
		t.Fatal(err)
	}
	d, ok := h.lookup(qq)
	if !ok || d.Sticker != 1 || d.Kind != contract.KindMeme || d.Summary != "一只猫 在笑" || d.Text != "哈哈\n第二行" {
		t.Fatalf("Lookup = %+v, %v", d, ok)
	}
	if p := h.host.last("media.described"); p["sticker"] != 1 || p["model"] != nil || p["summary"] != "一只猫 在笑" {
		t.Fatalf("media.described = %v", p)
	}

	h.sticker(stickerCall{args: "note 1 Eku喵喵叫"})
	described := h.count("media.described")
	if d, _ := h.lookup(qq); d.Sticker != 1 || d.Summary != "Eku喵喵叫" {
		t.Fatalf("after the note, Lookup = %+v", d)
	}
	if h.count("media.described") != described {
		t.Fatal("Lookup recorded a description")
	}

	tg := h.telegramImage("https://telegram/1", png)
	if err := h.handle(tg); err != nil {
		t.Fatal(err)
	}
	if d, ok := h.lookup(tg); !ok || d.Sticker != 1 || d.Summary != "Eku喵喵叫" {
		t.Fatalf("a Telegram picture: Lookup = %+v, %v", d, ok)
	}
	if h.steps[0].calls() != calls {
		t.Fatalf("model calls = %d, want %d: a kept sticker is not described again", h.steps[0].calls(), calls)
	}

	// Without its description, it reads as the kind it was kept with.
	if _, err := h.m.store.db.Exec(`DELETE FROM descriptions`); err != nil {
		t.Fatal(err)
	}
	if d, _ := h.lookup(qq); d.Sticker != 1 || d.Kind != contract.KindMeme || d.Text != "" || d.Summary != "Eku喵喵叫" {
		t.Fatalf("without a description, Lookup = %+v", d)
	}

	// Removed, it is a picture like any other.
	h.sticker(stickerCall{args: "remove 1"})
	if err := h.handle(h.image("https://qq/3", png)); err != nil {
		t.Fatal(err)
	}
	if d, ok := h.lookup(qq); !ok || d.Sticker != 0 || h.steps[0].calls() != calls+1 {
		t.Fatalf("a removed sticker: Lookup = %+v, %v; model calls %d", d, ok, h.steps[0].calls())
	}
}

// A sticker with neither a note nor a summary is no sticker yet: it is
// described like any picture, and known once it has a note.
func TestStickerWithoutTextIsDescribed(t *testing.T) {
	h := newHarness(t, 1)
	h.steps[0].answers = []answer{{err: errors.New("down")}, says(picture)}
	png := pngOf(t, color.Black)
	h.sticker(stickerCall{args: "add", media: []otogi.Media{h.image("https://qq/1", png)}})

	md := h.image("https://qq/2", png)
	if err := h.handle(md); err != nil {
		t.Fatal(err)
	}
	if d, ok := h.lookup(md); !ok || d.Sticker != 0 || d.Summary != "一只猫 在笑" || h.steps[0].calls() != 2 {
		t.Fatalf("Lookup = %+v, %v; model calls %d", d, ok, h.steps[0].calls())
	}
	h.sticker(stickerCall{args: "note 1 摸头"})
	if d, _ := h.lookup(md); d.Sticker != 1 || d.Summary != "摸头" || d.Kind != contract.KindMeme {
		t.Fatalf("with a note, Lookup = %+v", d)
	}
}

// A sticker's MD5 is kept with it, so QQ's pictures, never fetched, are
// known; Describe, which waits for Handle, finds it at once.
func TestStickerKeepsItsMD5(t *testing.T) {
	h := newHarness(t, 1)
	h.steps[0].answers = []answer{says(picture)}
	png := pngOf(t, color.White)
	h.sticker(stickerCall{args: "add", media: []otogi.Media{h.image("https://qq/1", png)}})
	var sum []byte
	if err := h.m.store.db.QueryRow(`SELECT md5 FROM stickers WHERE id = 1`).Scan(&sum); err != nil {
		t.Fatal(err)
	}
	if want := h.image("https://qq/1", png).Hashes[0].Value; !slices.Equal(sum, want) {
		t.Fatalf("md5 %x, want %x", sum, want)
	}
	d, err := h.host.services[contract.Service].(contract.Describer).Describe(context.Background(), from, h.image("https://qq/9", png))
	if err != nil || d.Sticker != 1 {
		t.Fatalf("Describe = %+v, %v", d, err)
	}
}
