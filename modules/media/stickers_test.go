package media

import (
	"context"
	"crypto/sha256"
	"errors"
	"image/color"
	"strings"
	"sync"
	"testing"
	"time"

	contract "github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// replies is an invocation that also keeps what the module sends.
type replies struct {
	*invocation
	mu   sync.Mutex
	sent []otogi.Message
}

func (r *replies) Send(_ context.Context, _ string, msg otogi.Message) (otogi.EventRef, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, msg)
	return otogi.EventRef{SourceEvent: "sent"}, nil
}

// post handles a message with media, from source, in the test chat.
func (h *harness) post(source string, media ...otogi.Media) error {
	e := otogi.Event{Platform: from.Platform, Account: from.Account, Conversation: otogi.Conversation{ID: from.Conversation, Kind: otogi.ConversationGroup},
		Action: otogi.ActionMessageCreated, Direction: otogi.DirectionIncoming, SourceID: otogi.SourceEventID(source)}
	for i, md := range media {
		e.Message.Parts = append(e.Message.Parts, otogi.Part{ID: otogi.PartID(string(rune('p' + i))), Kind: otogi.PartMedia, Media: &md})
	}
	return h.m.Handle(context.Background(), e, h.inv)
}

// stickerCall is one /sticker command: its arguments, its sender, where it
// was sent, and the rest of its message.
type stickerCall struct {
	args    string
	actor   otogi.ActorID
	direct  bool
	replyTo string
	media   []otogi.Media
}

// sticker runs /sticker as c and returns the module's reply.
func (h *harness) sticker(c stickerCall) string {
	h.t.Helper()
	if c.actor == "" {
		c.actor = operator
	}
	conv := otogi.Conversation{ID: from.Conversation, Kind: otogi.ConversationGroup}
	if c.direct {
		conv = otogi.Conversation{ID: "user:op", Kind: otogi.ConversationDirect}
	}
	e := otogi.Event{ID: "cmd", Platform: from.Platform, Account: from.Account, Conversation: conv, Actor: otogi.Actor{ID: c.actor},
		Action: otogi.ActionMessageCreated, Direction: otogi.DirectionIncoming, SourceID: "cmd",
		Command: &otogi.Command{Name: "sticker", Args: c.args, Argv: strings.Fields(c.args)}}
	for i, md := range c.media {
		e.Message.Parts = append(e.Message.Parts, otogi.Part{ID: otogi.PartID(string(rune('p' + i))), Kind: otogi.PartMedia, Media: &md})
	}
	if c.replyTo != "" {
		e.Message.Relations = []otogi.Relation{{Kind: otogi.RelationReply, Target: otogi.EventRef{Platform: from.Platform, Account: from.Account, Conversation: conv.ID, SourceEvent: otogi.SourceEventID(c.replyTo)}}}
	}
	r := &replies{invocation: h.inv}
	if err := h.m.Handle(context.Background(), e, r); err != nil {
		h.t.Fatalf("/sticker %s: %v", c.args, err)
	}
	if len(r.sent) != 1 {
		h.t.Fatalf("/sticker %s sent %d replies, want 1", c.args, len(r.sent))
	}
	return r.sent[0].Parts[0].Text
}

func (h *harness) list() []contract.Sticker {
	h.t.Helper()
	all, err := h.host.services[contract.StickersService].(contract.Stickers).List(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	return all
}

func (h *harness) picture(id int) (otogi.Media, contract.Sticker, error) {
	return h.host.services[contract.StickersService].(contract.Stickers).Picture(context.Background(), id)
}

func wantPrefix(t *testing.T, got, prefix string) {
	t.Helper()
	if !strings.HasPrefix(got, prefix) {
		t.Fatalf("reply = %q, want it to start with %q", got, prefix)
	}
}

func TestStickerFromAReply(t *testing.T) {
	h := newHarness(t, 1)
	h.steps[0].answers = []answer{says(picture)}
	png := pngOf(t, color.RGBA{R: 255, A: 255})
	if err := h.post("m1", h.image("https://qq/1", png)); err != nil {
		t.Fatal(err)
	}
	wantPrefix(t, h.sticker(stickerCall{args: "add", replyTo: "m1"}), "Kept as sticker 1: 一只猫")
	if h.steps[0].calls() != 1 {
		t.Fatalf("model calls = %d, want 1: the kept description is reused", h.steps[0].calls())
	}
	all := h.list()
	if len(all) != 1 || all[0].ID != 1 || all[0].Kind != contract.KindMeme || !strings.HasPrefix(all[0].Text, "一只猫") {
		t.Fatalf("List = %+v", all)
	}
	md, st, err := h.picture(1)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(png)
	if md.Role != otogi.MediaImage || md.MediaType != "image/png" || string(md.Source.Inline) != string(png) || md.Source.Kind != otogi.MediaSourceInline ||
		len(md.Hashes) != 1 || md.Hashes[0].Algorithm != "sha256" || string(md.Hashes[0].Value) != string(sum[:]) || st.ID != 1 {
		t.Fatalf("Picture = %+v, %+v", md, st)
	}
	if p := h.host.last("media.sticker"); p["action"] != "add" || p["id"] != 1 || p["by"] != "qq:"+operator {
		t.Fatalf("media.sticker = %v", p)
	}
}

func TestStickerSentWithTheCommand(t *testing.T) {
	h := newHarness(t, 1)
	h.steps[0].answers = []answer{says(picture)}
	gif := gifOf(t, []gifFrame{{full: true, c: color.RGBA{G: 255, A: 255}}})
	// A private chat is not on the describe list: the picture is described
	// now, since nothing described it on the way in.
	inline := otogi.Media{Role: otogi.MediaImage, Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: gif}}
	wantPrefix(t, h.sticker(stickerCall{args: "add 生气", direct: true, media: []otogi.Media{inline}}), "Kept as sticker 1: 生气")
	if h.steps[0].calls() != 1 {
		t.Fatalf("model calls = %d, want 1", h.steps[0].calls())
	}
	sum := sha256.Sum256(gif)
	if d, ok, _ := h.m.store.get(context.Background(), key{"sha256", sum[:]}); !ok || d.Kind != contract.KindMeme {
		t.Fatalf("the description was not kept: %+v, %v", d, ok)
	}
	md, st, err := h.picture(1)
	if err != nil || md.Role != otogi.MediaAnimation || md.MediaType != "image/gif" || st.Text != "生气" || st.Kind != contract.KindMeme {
		t.Fatalf("Picture = %+v, %+v, %v", md, st, err)
	}
}

func TestStickerSeveralInOneMessage(t *testing.T) {
	h := newHarness(t, 1)
	h.steps[0].answers = []answer{says(picture), says(picture)}
	a, b := h.image("https://qq/a", pngOf(t, color.Black)), h.image("https://qq/b", pngOf(t, color.White))
	got := h.sticker(stickerCall{args: "add", media: []otogi.Media{a, b}})
	if lines := strings.Split(got, "\n"); len(lines) != 2 || !strings.HasPrefix(lines[0], "Kept as sticker 1") || !strings.HasPrefix(lines[1], "Kept as sticker 2") {
		t.Fatalf("reply = %q", got)
	}
}

func TestStickerRefused(t *testing.T) {
	h := newHarness(t, 1)
	png := h.image("https://qq/1", pngOf(t, color.Black))

	wantPrefix(t, h.sticker(stickerCall{args: "add", actor: "user:someone", media: []otogi.Media{png}}), "Only operators can manage stickers. Your ID is qq:user:someone")

	h.host.allowed[chat] = false
	wantPrefix(t, h.sticker(stickerCall{args: "add", media: []otogi.Media{png}}), "This chat is not on media's describe list.")
	h.host.allowed[chat] = true

	wantPrefix(t, h.sticker(stickerCall{args: "add"}), "Reply to a picture with /sticker add")
	wantPrefix(t, h.sticker(stickerCall{args: "nope"}), "Usage: /sticker")
	wantPrefix(t, h.sticker(stickerCall{args: "remove x"}), "Usage: /sticker")
	if len(h.list()) != 0 {
		t.Fatal("a refused /sticker kept something")
	}
}

func TestStickerOperatorsAreReadEachTime(t *testing.T) {
	h := newHarness(t, 1)
	h.steps[0].answers = []answer{says(picture)}
	png := otogi.Media{Role: otogi.MediaImage, Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: pngOf(t, color.Black)}}

	// The operators are edited in the control panel while the module
	// runs: the next command goes by the list as it is then.
	h.operators = []string{"qq:user:someone"}
	wantPrefix(t, h.sticker(stickerCall{args: "add", actor: "user:someone", direct: true, media: []otogi.Media{png}}), "Kept as sticker 1")
	wantPrefix(t, h.sticker(stickerCall{args: "list"}), "Only operators can manage stickers. Your ID is qq:"+operator)
}

func TestStickerOnlyFromRecentMessages(t *testing.T) {
	h := newHarness(t, 1)
	ctx := context.Background()
	now := time.Now()
	h.m.now = func() time.Time { return now }
	pic := []otogi.Media{h.image("https://qq/old", pngOf(t, color.Black))}
	if err := h.m.store.putRecent(ctx, from, "old", pic, now.Add(-recentMedia-time.Minute)); err != nil {
		t.Fatal(err)
	}
	wantPrefix(t, h.sticker(stickerCall{args: "add", replyTo: "old"}), "I can't find a picture in that message any more")
	wantPrefix(t, h.sticker(stickerCall{args: "add", replyTo: "never-seen"}), "I can't find a picture in that message any more")

	// The chat is on the list, but its media was not kept: describing is
	// what keeps it, and a message without media keeps nothing.
	if err := h.post("plain"); err != nil {
		t.Fatal(err)
	}
	wantPrefix(t, h.sticker(stickerCall{args: "add", replyTo: "plain"}), "I can't find a picture in that message any more")
}

func TestStickerPictureGone(t *testing.T) {
	h := newHarness(t, 1)
	md := h.image("https://qq/1", pngOf(t, color.Black))
	h.inv.fail = errors.New("expired")
	wantPrefix(t, h.sticker(stickerCall{args: "add", media: []otogi.Media{md}}), "Couldn't fetch that picture any more")
	if p := h.host.last("media.sticker"); p["reason"] != "gone" {
		t.Fatalf("media.sticker = %v", p)
	}
}

func TestStickerTooLarge(t *testing.T) {
	h := newHarness(t, 1)
	declared := h.image("https://qq/big", pngOf(t, color.Black))
	declared.ByteSize = maxStickerBytes + 1
	wantPrefix(t, h.sticker(stickerCall{args: "add", media: []otogi.Media{declared}}), "That picture is 9.0 MiB; a sticker can be at most 9 MiB.")
	if h.inv.fetches() != 0 {
		t.Fatal("a picture declared too large was fetched")
	}
	// A picture that turns out larger than it said.
	big := make([]byte, maxStickerBytes+1)
	copy(big, pngOf(t, color.Black))
	h.inv.files["https://qq/huge"] = big
	under := otogi.Media{Role: otogi.MediaImage, ByteSize: 10, Source: otogi.MediaSource{Kind: otogi.MediaSourceExternal, URL: "https://qq/huge"}}
	wantPrefix(t, h.sticker(stickerCall{args: "add", media: []otogi.Media{under}}), "That picture is 9.0 MiB")
	if p := h.host.last("media.sticker"); p["reason"] != "too large" {
		t.Fatalf("media.sticker = %v", p)
	}
}

func TestStickerTypes(t *testing.T) {
	h := newHarness(t, 1)
	mp4 := append([]byte{0, 0, 0, 0x20}, []byte("ftypisom\x00\x00\x02\x00isomiso2mp41")...)
	webp := append([]byte("RIFF\x24\x00\x00\x00WEBPVP8 "), make([]byte, 24)...)
	for name, data := range map[string][]byte{"mp4": mp4, "webp": webp} {
		md := otogi.Media{Role: otogi.MediaAnimation, Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: data}}
		wantPrefix(t, h.sticker(stickerCall{args: "add", media: []otogi.Media{md}}), "Only PNG, JPEG, and GIF pictures can be kept.")
		if p := h.host.last("media.sticker"); p["reason"] != "not png, jpeg, or gif" {
			t.Fatalf("%s: media.sticker = %v", name, p)
		}
	}
	if len(h.list()) != 0 {
		t.Fatal("a picture of the wrong type was kept")
	}
}

func TestStickerKeptOnce(t *testing.T) {
	h := newHarness(t, 1)
	h.steps[0].answers = []answer{says(picture)}
	png := pngOf(t, color.Black)
	wantPrefix(t, h.sticker(stickerCall{args: "add", media: []otogi.Media{h.image("https://qq/1", png)}}), "Kept as sticker 1")
	// The same picture by another way in, with no driver digest.
	inline := otogi.Media{Role: otogi.MediaImage, Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: png}}
	wantPrefix(t, h.sticker(stickerCall{args: "add", direct: true, media: []otogi.Media{inline}}), "Already kept as sticker 1.")
	if h.steps[0].calls() != 1 {
		t.Fatalf("model calls = %d, want 1", h.steps[0].calls())
	}
}

func TestStickerLimits(t *testing.T) {
	h := newHarness(t, 1)
	h.m.limits = stickerLimits{count: 2, total: 1 << 20}
	h.steps[0].answers = []answer{says(picture), says(picture), says(picture)}
	for i, c := range []color.Color{color.Black, color.White} {
		wantPrefix(t, h.sticker(stickerCall{args: "add", media: []otogi.Media{h.image("https://qq/"+string(rune('a'+i)), pngOf(t, c))}}), "Kept as sticker")
	}
	third := h.image("https://qq/c", pngOf(t, color.RGBA{R: 255, A: 255}))
	wantPrefix(t, h.sticker(stickerCall{args: "add", media: []otogi.Media{third}}), "There are 2 stickers already; remove one first.")

	h.m.limits = stickerLimits{count: 10, total: 100}
	wantPrefix(t, h.sticker(stickerCall{args: "add", media: []otogi.Media{third}}), "The stickers would take more than 0 MiB; remove some first.")
	if p := h.host.last("media.sticker"); p["reason"] != "too much in all" {
		t.Fatalf("media.sticker = %v", p)
	}
	if h.steps[0].calls() != 2 {
		t.Fatalf("model calls = %d, want 2: a picture that can't be kept is not described", h.steps[0].calls())
	}
}

func TestStickerNoteAndRemove(t *testing.T) {
	h := newHarness(t, 1)
	h.steps[0].answers = []answer{says(picture), says(picture)}
	png := pngOf(t, color.Black)
	h.sticker(stickerCall{args: "add", media: []otogi.Media{h.image("https://qq/1", png)}})

	wantPrefix(t, h.sticker(stickerCall{args: "note 1 摸头 安慰"}), "Sticker 1 now shows: 摸头 安慰")
	if all := h.list(); len(all) != 1 || all[0].Text != "摸头 安慰" {
		t.Fatalf("List = %+v", all)
	}
	wantPrefix(t, h.sticker(stickerCall{args: "note 1"}), "Sticker 1 now shows: 一只猫")
	wantPrefix(t, h.sticker(stickerCall{args: "list"}), "1 of 50 stickers")
	wantPrefix(t, h.sticker(stickerCall{args: "note 7 x"}), "There is no sticker 7.")

	wantPrefix(t, h.sticker(stickerCall{args: "remove 1"}), "Removed sticker 1.")
	if _, _, err := h.picture(1); !errors.Is(err, contract.ErrNoSticker) {
		t.Fatalf("Picture of a removed sticker = %v", err)
	}
	wantPrefix(t, h.sticker(stickerCall{args: "remove 1"}), "There is no sticker 1.")
	wantPrefix(t, h.sticker(stickerCall{args: "list"}), "No stickers yet.")
	// Numbers are not reused: a thread's old call must not point at another
	// picture.
	wantPrefix(t, h.sticker(stickerCall{args: "add", media: []otogi.Media{h.image("https://qq/1", png)}}), "Kept as sticker 2")
}

func TestStickerWithoutDescription(t *testing.T) {
	h := newHarness(t, 1)
	h.steps[0].answers = []answer{{err: errors.New("down")}}
	md := h.image("https://qq/1", pngOf(t, color.Black))
	wantPrefix(t, h.sticker(stickerCall{args: "add", media: []otogi.Media{md}}), "Kept as sticker 1, but it has no description")
	if all := h.list(); len(all) != 0 {
		t.Fatalf("List = %+v; a sticker with neither a note nor a description is left out", all)
	}
	if got := h.sticker(stickerCall{args: "list"}); !strings.Contains(got, "1. [-] (no description") {
		t.Fatalf("list = %q", got)
	}
	h.sticker(stickerCall{args: "note 1 摸头"})
	if all := h.list(); len(all) != 1 || all[0].Text != "摸头" || all[0].Kind != "" {
		t.Fatalf("List = %+v", all)
	}
}

// A kept sticker stands on its own: its description may be pruned.
func TestStickerOutlivesItsDescription(t *testing.T) {
	h := newHarness(t, 1)
	h.steps[0].answers = []answer{says(picture)}
	h.sticker(stickerCall{args: "add", media: []otogi.Media{h.image("https://qq/1", pngOf(t, color.Black))}})
	if _, err := h.m.store.db.Exec(`DELETE FROM descriptions`); err != nil {
		t.Fatal(err)
	}
	if all := h.list(); len(all) != 1 || all[0].Kind != contract.KindMeme || !strings.HasPrefix(all[0].Text, "一只猫") {
		t.Fatalf("List = %+v", all)
	}
}

func TestAfter(t *testing.T) {
	for _, c := range []struct {
		args string
		n    int
		want string
	}{
		{"add  摸头  安慰 ", 1, "摸头  安慰"},
		{"note 3 a b", 2, "a b"},
		{"note 3", 2, ""},
		{"add", 1, ""},
	} {
		if got := after(c.args, c.n); got != c.want {
			t.Errorf("after(%q, %d) = %q, want %q", c.args, c.n, got, c.want)
		}
	}
}
