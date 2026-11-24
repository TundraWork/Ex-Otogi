package media

import (
	"bytes"
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	contract "github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// answer is what a fake model replies to one call: an error, or a response.
type answer struct {
	resp llm.Response
	err  error
	// hold, when set, is waited on before answering.
	hold chan struct{}
}

func says(text string) answer {
	return answer{resp: llm.Response{Markdown: text, Finish: llm.FinishStop, Usage: llm.Usage{Input: 300, Output: 40}}}
}

// provider is a fake model that answers in turn and keeps what it was asked.
type provider struct {
	mu       sync.Mutex
	answers  []answer
	requests []llm.Request
}

func (p *provider) Generate(ctx context.Context, req llm.Request, _ func(otogi.Message) error) (llm.Response, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	if len(p.answers) == 0 {
		p.mu.Unlock()
		return llm.Response{}, errors.New("provider: no answer left")
	}
	a := p.answers[0]
	p.answers = p.answers[1:]
	p.mu.Unlock()
	if a.hold != nil {
		select {
		case <-a.hold:
		case <-ctx.Done():
			return llm.Response{}, ctx.Err()
		}
	}
	return a.resp, a.err
}

func (p *provider) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}

type host struct {
	otogi.Host
	storage  *modulestore.Storage
	allowed  map[string]bool
	mu       sync.Mutex
	records  []otogi.Record
	services map[string]any
}

func (h *host) Module() otogi.ModuleID                   { return "media" }
func (h *host) Logger() *slog.Logger                     { return slog.New(slog.DiscardHandler) }
func (h *host) Allowed(_, chat string) bool              { return h.allowed[chat] }
func (h *host) Schedule(string, string, otogi.Job) error { return nil }
func (h *host) Database(ctx context.Context) (*sql.DB, error) {
	return h.storage.Database(ctx, "media")
}
func (h *host) Provide(name string, s any) error {
	h.services[name] = s
	return nil
}
func (h *host) Record(_ context.Context, r otogi.Record) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
}

// kinds lists the kinds of the records kept so far.
func (h *host) kinds() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, r := range h.records {
		out = append(out, r.Kind)
	}
	return out
}

func (h *host) last(kind string) map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := len(h.records) - 1; i >= 0; i-- {
		if h.records[i].Kind == kind {
			p, _ := h.records[i].Payload.(map[string]any)
			return p
		}
	}
	return nil
}

// invocation fetches media from a map of URL to bytes.
type invocation struct {
	otogi.Invocation
	mu      sync.Mutex
	files   map[string][]byte
	fetched int
	fail    error
}

func (i *invocation) FetchMedia(_ context.Context, _ otogi.Destination, src otogi.MediaSource) ([]byte, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.fetched++
	if i.fail != nil {
		return nil, i.fail
	}
	b, ok := i.files[src.URL]
	if !ok {
		return nil, errors.New("no such file")
	}
	return b, nil
}

func (i *invocation) fetches() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.fetched
}

const (
	chat = "qq:group-1"
	// operator is the actor the tests' module takes as an operator.
	operator = "user:op"
)

type harness struct {
	t         *testing.T
	m         *Module
	host      *host
	inv       *invocation
	steps     []*provider
	operators []string // what the module's operators function returns
}

// newHarness starts the module with one fake model per step, the test
// chat on its list.
func newHarness(t *testing.T, steps int) *harness {
	t.Helper()
	storage, err := modulestore.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	h := &harness{t: t, host: &host{storage: storage, allowed: map[string]bool{chat: true}, services: map[string]any{}}, inv: &invocation{files: map[string][]byte{}}, operators: []string{"qq:" + operator}}
	cfg := Config{Language: "Chinese"}
	byName := map[string]*provider{}
	for i := range steps {
		p := &provider{}
		name := string(rune('a' + i))
		byName[name] = p
		h.steps = append(h.steps, p)
		cfg.Steps = append(cfg.Steps, Step{Provider: name, Model: "model-" + name, Thinking: "minimal"})
	}
	h.m, err = New(cfg, func(name string) (llm.Provider, error) {
		if p, ok := byName[name]; ok {
			return p, nil
		}
		return nil, otogi.ErrInvalid
	}, func() []string { return h.operators })
	if err != nil {
		t.Fatal(err)
	}
	if err := h.m.Start(context.Background(), h.host); err != nil {
		t.Fatal(err)
	}
	return h
}

var from = otogi.Destination{Platform: "qq", Account: "bot", Conversation: "group-1"}

// image is a QQ picture at url, its MD5 given as QQ names it.
func (h *harness) image(url string, data []byte) otogi.Media {
	h.inv.files[url] = data
	sum := md5.Sum(data)
	return otogi.Media{Role: otogi.MediaImage, MediaType: "image/png", ByteSize: int64(len(data)),
		Source: otogi.MediaSource{Kind: otogi.MediaSourceExternal, URL: url}, Hashes: []otogi.MediaHash{{Algorithm: "md5", Value: sum[:]}}}
}

func (h *harness) handle(media ...otogi.Media) error {
	e := otogi.Event{Platform: from.Platform, Account: from.Account, Conversation: otogi.Conversation{ID: from.Conversation},
		Action: otogi.ActionMessageCreated, Direction: otogi.DirectionIncoming}
	for i, md := range media {
		e.Message.Parts = append(e.Message.Parts, otogi.Part{ID: otogi.PartID(string(rune('p' + i))), Kind: otogi.PartMedia, Media: &md})
	}
	return h.m.Handle(context.Background(), e, h.inv)
}

func pngOf(t *testing.T, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := range 4 {
		for x := range 4 {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

const picture = "kind: meme\ndescription: 一只猫  在笑\ntext: 哈哈\n第二行"

func TestDescribesOnceByContent(t *testing.T) {
	h := newHarness(t, 1)
	h.steps[0].answers = []answer{says(picture)}
	img := h.image("https://qq/a", pngOf(t, color.White))
	if err := h.handle(img); err != nil {
		t.Fatal(err)
	}
	d, ok, err := h.m.Lookup(context.Background(), from, img)
	if err != nil || !ok {
		t.Fatalf("Lookup = %v, %v", ok, err)
	}
	want := contract.Description{Kind: contract.KindMeme, Summary: "一只猫 在笑", Text: "哈哈\n第二行"}
	if d != want {
		t.Fatalf("description = %+v, want %+v", d, want)
	}
	rec := h.host.last("media.described")
	if rec["summary"] != want.Summary || rec["text"] != want.Text || rec["model"] != "model-a" || rec["input_tokens"] != 300 {
		t.Fatalf("media.described = %v", rec)
	}
	req := h.steps[0].requests[0]
	if req.Model != "model-a" || req.Thinking != "minimal" || !strings.Contains(req.System, "written in Chinese") {
		t.Fatalf("request = %+v", req)
	}

	// The same picture again, at another URL: known by its MD5, so it is
	// neither fetched nor described again.
	again := h.image("https://qq/b", pngOf(t, color.White))
	if err := h.handle(again); err != nil {
		t.Fatal(err)
	}
	if h.inv.fetches() != 1 || h.steps[0].calls() != 1 {
		t.Fatalf("fetches = %d, calls = %d; want 1, 1", h.inv.fetches(), h.steps[0].calls())
	}
}

func TestStepsAreTriedInOrder(t *testing.T) {
	h := newHarness(t, 5)
	h.steps[0].answers = []answer{{err: otogi.ErrUnavailable}}
	h.steps[1].answers = []answer{{resp: llm.Response{Finish: llm.FinishBlocked, Reason: "PROHIBITED_CONTENT"}}}
	h.steps[2].answers = []answer{says("这是一只猫")}
	withheld := says(picture)
	withheld.resp.Withheld = []llm.WithheldPart{{Part: "m0", MediaType: "image/png", Bytes: 70}}
	h.steps[3].answers = []answer{withheld}
	h.steps[4].answers = []answer{says(picture)}
	img := h.image("https://qq/a", pngOf(t, color.White))
	if err := h.handle(img); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(h.host.kinds(), ",")
	if got != "media.failed,media.failed,media.failed,media.failed,media.described" {
		t.Fatalf("records = %s", got)
	}
	if rec := h.host.last("media.described"); rec["step"] != 5 {
		t.Fatalf("described by step %v, want 5", rec["step"])
	}
	if _, ok, _ := h.m.Lookup(context.Background(), from, img); !ok {
		t.Fatal("the description was not kept")
	}
}

func TestEveryStepFailing(t *testing.T) {
	h := newHarness(t, 2)
	h.steps[0].answers = []answer{{err: otogi.ErrUnavailable}}
	h.steps[1].answers = []answer{says("no sections here")}
	img := h.image("https://qq/a", pngOf(t, color.White))
	if err := h.handle(img); err != nil {
		t.Fatalf("Handle = %v; a model failing is recorded, not returned", err)
	}
	if _, ok, _ := h.m.Lookup(context.Background(), from, img); ok {
		t.Fatal("a failed description was kept")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := h.m.Describe(ctx, from, img); !errors.Is(err, otogi.ErrUnavailable) {
		t.Fatalf("Describe = %v, want ErrUnavailable at once", err)
	}
}

func TestOnlyChatsOnTheList(t *testing.T) {
	h := newHarness(t, 1)
	other := otogi.Destination{Platform: "qq", Account: "bot", Conversation: "group-2"}
	img := h.image("https://qq/a", pngOf(t, color.White))
	e := otogi.Event{Platform: "qq", Account: "bot", Conversation: otogi.Conversation{ID: "group-2"}, Action: otogi.ActionMessageCreated,
		Message: otogi.Message{Parts: []otogi.Part{{ID: "p", Kind: otogi.PartMedia, Media: &img}}}}
	if err := h.m.Handle(context.Background(), e, h.inv); err != nil {
		t.Fatal(err)
	}
	if h.inv.fetches() != 0 || len(h.host.kinds()) != 0 {
		t.Fatalf("fetches = %d, records = %v; want none", h.inv.fetches(), h.host.kinds())
	}
	if _, err := h.m.Describe(context.Background(), other, img); !errors.Is(err, contract.ErrNotAllowed) {
		t.Fatalf("Describe = %v, want ErrNotAllowed", err)
	}
}

func TestNotDescribed(t *testing.T) {
	h := newHarness(t, 1)
	big := h.image("https://qq/big", nil)
	big.ByteSize = maxBytes + 1
	file := h.image("https://qq/doc", []byte("%PDF-1.7"))
	file.Role, file.MediaType = otogi.MediaDocument, "application/pdf"
	if err := h.handle(big, file); err != nil {
		t.Fatal(err)
	}
	if h.inv.fetches() != 0 || h.steps[0].calls() != 0 {
		t.Fatalf("fetches = %d, calls = %d; want none", h.inv.fetches(), h.steps[0].calls())
	}
	if got := strings.Join(h.host.kinds(), ","); got != "media.skipped,media.skipped" {
		t.Fatalf("records = %s", got)
	}
	if rec := h.host.last("media.skipped"); rec["reason"] != "not described" {
		t.Fatalf("media.skipped = %v", rec)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, md := range []otogi.Media{big, file} {
		if _, err := h.m.Describe(ctx, from, md); !errors.Is(err, contract.ErrNotDescribed) {
			t.Fatalf("Describe = %v, want ErrNotDescribed", err)
		}
	}
}

// waiting reports whether a caller is waiting on md.
func (h *harness) waiting(md otogi.Media) bool {
	k, src := mediaKey(md)
	h.m.waits.mu.Lock()
	defer h.m.waits.mu.Unlock()
	return len(h.m.waits.waiting[waitKey(k, src)]) > 0
}

func TestDescribeWaitsForHandle(t *testing.T) {
	h := newHarness(t, 1)
	release := make(chan struct{})
	a := says(picture)
	a.hold = release
	h.steps[0].answers = []answer{a}
	img := h.image("https://qq/a", pngOf(t, color.White))

	type got struct {
		d   contract.Description
		err error
	}
	done := make(chan got, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		d, err := h.m.Describe(ctx, from, img)
		done <- got{d, err}
	}()
	handled := make(chan error, 1)
	go func() { handled <- h.handle(img) }()
	for !h.waiting(img) {
		time.Sleep(time.Millisecond)
	}
	close(release)
	r := <-done
	if r.err != nil || r.d.Summary != "一只猫 在笑" {
		t.Fatalf("Describe = %+v, %v", r.d, r.err)
	}
	if err := <-handled; err != nil {
		t.Fatal(err)
	}
}

func TestDescribeRunsOut(t *testing.T) {
	h := newHarness(t, 1)
	release := make(chan struct{})
	a := says(picture)
	a.hold = release
	h.steps[0].answers = []answer{a}
	img := h.image("https://qq/a", pngOf(t, color.White))
	handled := make(chan error, 1)
	go func() { handled <- h.handle(img) }()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := h.m.Describe(ctx, from, img); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Describe = %v, want the deadline", err)
	}
	close(release)
	if err := <-handled; err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := h.m.Lookup(context.Background(), from, img); !ok {
		t.Fatal("the description finished later was not kept")
	}
}

func TestHandleFailingTellsWhoWaits(t *testing.T) {
	h := newHarness(t, 1)
	img := h.image("https://qq/a", pngOf(t, color.White))
	h.inv.fail = errors.New("download expired")

	result := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := h.m.Describe(ctx, from, img)
		result <- err
	}()
	for !h.waiting(img) {
		time.Sleep(time.Millisecond)
	}
	if err := h.handle(img); err == nil || !strings.Contains(err.Error(), "download expired") {
		t.Fatalf("Handle = %v, want the fetch error", err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, otogi.ErrUnavailable) {
			t.Fatalf("Describe = %v, want ErrUnavailable", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Describe kept waiting after Handle failed")
	}
}

func TestKnownBySourceWithoutHash(t *testing.T) {
	h := newHarness(t, 1)
	h.steps[0].answers = []answer{says(picture)}
	img := h.image("https://tg/a", pngOf(t, color.Black))
	img.Hashes = nil
	if err := h.handle(img); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := h.m.Lookup(context.Background(), from, img); !ok {
		t.Fatal("Lookup by source found nothing")
	}
	// The same content at another source is fetched to learn its digest,
	// but not described again.
	again := h.image("https://tg/b", pngOf(t, color.Black))
	again.Hashes = nil
	if err := h.handle(again); err != nil {
		t.Fatal(err)
	}
	if h.steps[0].calls() != 1 {
		t.Fatalf("calls = %d, want 1", h.steps[0].calls())
	}
}

// gifOf builds a 4x4 GIF from frames, each one pixel of a color at a
// point, drawn over a red first frame.
func gifOf(t testing.TB, frames []gifFrame) []byte {
	t.Helper()
	g := &gif.GIF{Config: image.Config{Width: 4, Height: 4}}
	for _, f := range frames {
		var img *image.Paletted
		if f.full {
			img = image.NewPaletted(image.Rect(0, 0, 4, 4), color.Palette{f.c})
		} else {
			img = image.NewPaletted(image.Rect(f.at.X, f.at.Y, f.at.X+1, f.at.Y+1), color.Palette{f.c})
		}
		g.Image = append(g.Image, img)
		g.Delay = append(g.Delay, f.delay)
		g.Disposal = append(g.Disposal, f.disposal)
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

type gifFrame struct {
	full     bool
	at       image.Point
	c        color.Color
	delay    int
	disposal byte
}

var (
	red    = color.RGBA{255, 0, 0, 255}
	blue   = color.RGBA{0, 0, 255, 255}
	green  = color.RGBA{0, 255, 0, 255}
	yellow = color.RGBA{255, 255, 0, 255}
)

// bigGIF is a GIF of frames frames of size x size on a canvas as large,
// written by hand: only the blocks are real, each frame's pixel data is a
// placeholder, so it is small whatever size says.
func bigGIF(size, frames int) []byte {
	le := func(n int) []byte { return []byte{byte(n), byte(n >> 8)} }
	b := append([]byte("GIF89a"), le(size)...)
	b = append(b, le(size)...)
	b = append(b, 0x80, 0, 0, 0, 0, 0, 255, 255, 255) // two colors
	for range frames {
		b = append(b, 0x21, 0xF9, 4, 0, 10, 0, 0, 0) // a graphic control extension
		b = append(b, 0x2C, 0, 0, 0, 0)
		b = append(b, le(size)...)
		b = append(b, le(size)...)
		b = append(b, 0, 2, 2, 0x4C, 0x01, 0)
	}
	return append(b, 0x3B)
}

func TestGIFDecodedSize(t *testing.T) {
	// Four frames on a 4x4 canvas: one whole, three of one pixel each.
	small := gifOf(t, []gifFrame{{full: true, c: red}, {at: image.Pt(0, 0), c: blue}, {at: image.Pt(1, 1), c: green}, {at: image.Pt(2, 2), c: yellow}})
	if got, err := gifDecoded(small); err != nil || got != 16+3+8*16 {
		t.Fatalf("gifDecoded = %d, %v; want %d", got, err, 16+3+8*16)
	}
	// As in the review: fifty 2000x2000 frames.
	if got, err := gifDecoded(bigGIF(2000, 50)); err != nil || got != 50*2000*2000+8*2000*2000 {
		t.Fatalf("gifDecoded = %d, %v", got, err)
	}
	whole := bigGIF(20, 2)
	for _, bad := range [][]byte{[]byte("GIF89a"), whole[:40], whole[:len(whole)-1], []byte("\x89PNG")} {
		if _, err := gifDecoded(bad); err == nil {
			t.Errorf("gifDecoded(%q) succeeded", bad)
		}
	}

	encoded := encodedGIF(t, 64, 16)
	if got, err := gifDecoded(encoded); err != nil || got != 16*64*64+8*64*64 {
		t.Fatalf("gifDecoded = %d, %v; want %d", got, err, 16*64*64+8*64*64)
	}
	// From the review: extensions that image/gif reads past and an earlier
	// gifDecoded took for the end of the file, so it counted none of the
	// frames after them. image/gif decodes all sixteen; gifDecoded refuses.
	for _, ext := range [][]byte{plainTextHidingTrailer, applicationHidingTrailer} {
		hidden := withExtension(encoded, ext)
		g, err := gif.DecodeAll(bytes.NewReader(hidden))
		if err != nil {
			t.Fatalf("image/gif: %v; the test GIF should decode", err)
		}
		if len(g.Image) != 16 {
			t.Fatalf("image/gif decoded %d frames, want 16", len(g.Image))
		}
		if got, err := gifDecoded(hidden); err == nil {
			t.Errorf("gifDecoded(% x ...) = %d, want an error", ext[:4], got)
		}
	}
}

// Extensions that image/gif and an earlier gifDecoded read differently.
var (
	// A plain text extension whose first byte is 0, not 12: image/gif
	// skips thirteen bytes (past the 0x3B), then sub-blocks to the last 0.
	plainTextHidingTrailer = append([]byte{0x21, 0x01, 0x00, 0x3B}, make([]byte, 12)...)
	// An application extension of size 0: image/gif reads 0x3B as a
	// sub-block of 59 bytes, then the terminator after them.
	applicationHidingTrailer = append([]byte{0x21, 0xFF, 0x00, 0x3B}, make([]byte, 60)...)
)

// encodedGIF is a GIF of frames frames of size x size, each of one color,
// as image/gif writes it: it decodes, unlike bigGIF.
func encodedGIF(t testing.TB, size, frames int) []byte {
	t.Helper()
	g := &gif.GIF{Config: image.Config{Width: size, Height: size}}
	for range frames {
		g.Image = append(g.Image, image.NewPaletted(image.Rect(0, 0, size, size), color.Palette{red, blue}))
		g.Delay = append(g.Delay, 10)
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// withExtension puts ext right after data's header and global color table,
// before its first block.
func withExtension(data, ext []byte) []byte {
	at := 13
	if data[10]&0x80 != 0 {
		at += 3 << ((data[10] & 7) + 1)
	}
	return append(append(append([]byte(nil), data[:at]...), ext...), data[at:]...)
}

// FuzzGIFDecoded holds gifDecoded to image/gif: a GIF it passes, at the
// size it counts, must not decode to more frame pixels than that. A GIF
// counted at more than 4 MiB is not decoded, nor one that could decode to
// more than 64 MiB if the count were wrong (each frame lies within the
// header's canvas, which image/gif checks, and starts with a 0x2C byte),
// so the fuzzer itself stays small; a way to read a block differently
// works as well on a small GIF.
func FuzzGIFDecoded(f *testing.F) {
	small := encodedGIF(f, 64, 16)
	f.Add(small)
	f.Add(gifOf(f, []gifFrame{{full: true, c: red}, {at: image.Pt(1, 1), c: blue, disposal: gif.DisposalPrevious}}))
	f.Add(bigGIF(8, 3))
	f.Add(withExtension(small, plainTextHidingTrailer))
	f.Add(withExtension(small, applicationHidingTrailer))
	f.Fuzz(func(t *testing.T, data []byte) {
		counted, err := gifDecoded(data)
		if err != nil || counted > 4<<20 {
			return
		}
		canvas := int64(binary.LittleEndian.Uint16(data[6:])) * int64(binary.LittleEndian.Uint16(data[8:]))
		if canvas*int64(bytes.Count(data, []byte{0x2C})) > 64<<20 {
			return
		}
		g, err := gif.DecodeAll(bytes.NewReader(data))
		if err != nil {
			return
		}
		var pixels int64
		for _, m := range g.Image {
			pixels += int64(m.Bounds().Dx()) * int64(m.Bounds().Dy())
		}
		if pixels > counted {
			t.Fatalf("gifDecoded = %d, but image/gif decoded %d frames of %d pixels", counted, len(g.Image), pixels)
		}
	})
}

// A small file of large frames is not decoded: decoding it would take far
// more memory than its size says.
func TestLargeDecodedGIFIsSkipped(t *testing.T) {
	h := newHarness(t, 1)
	bomb := bigGIF(2000, 50)
	if len(bomb) > 2000 {
		t.Fatalf("the test GIF is %d bytes; it should be small", len(bomb))
	}
	a := h.image("https://qq/bomb", bomb)
	a.Role, a.MediaType = otogi.MediaAnimation, "image/gif"
	if err := h.handle(a); err != nil {
		t.Fatal(err)
	}
	if rec := h.host.last("media.skipped"); rec["reason"] != "too large" || h.steps[0].calls() != 0 {
		t.Fatalf("media.skipped = %v, calls = %d", rec, h.steps[0].calls())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := h.m.Describe(ctx, from, a); !errors.Is(err, contract.ErrNotDescribed) {
		t.Fatalf("Describe = %v, want ErrNotDescribed", err)
	}
}

// Results nobody waits for still expire: an agent that only looks
// descriptions up never calls wait.
func TestFinishedResultsExpire(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	w := newWaits(func() time.Time { return now })
	w.finish("a", result{})
	now = now.Add(keepResults + time.Second)
	w.finish("b", result{})
	if _, kept := w.done["a"]; kept || len(w.done) != 1 {
		t.Fatalf("kept %d results; the expired one is still there: %v", len(w.done), kept)
	}
}

func TestGIFFramesHonorDisposal(t *testing.T) {
	data := gifOf(t, []gifFrame{
		{full: true, c: red, delay: 0, disposal: gif.DisposalNone},
		{at: image.Pt(0, 0), c: blue, delay: 5, disposal: gif.DisposalBackground},
		{at: image.Pt(1, 1), c: green, delay: 10, disposal: gif.DisposalPrevious},
		{at: image.Pt(2, 2), c: yellow, delay: 1, disposal: gif.DisposalNone},
	})
	frames, count, total, err := gifFrames(data, 6)
	if err != nil {
		t.Fatal(err)
	}
	// Delays of 0 and 1 play as 100 ms: 100 + 50 + 100 + 100.
	if count != 4 || len(frames) != 4 || total != 350*time.Millisecond {
		t.Fatalf("count = %d, frames = %d, total = %v", count, len(frames), total)
	}
	at := func(i, x, y int) color.RGBA {
		img, err := png.Decode(bytes.NewReader(frames[i]))
		if err != nil {
			t.Fatal(err)
		}
		r, g, b, a := img.At(x, y).RGBA()
		return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
	}
	clear := color.RGBA{}
	for _, c := range []struct {
		frame, x, y int
		want        color.RGBA
	}{
		{0, 0, 0, red},
		{1, 0, 0, blue},
		{1, 3, 3, red},
		{2, 0, 0, clear}, // frame 1 was disposed to the background
		{2, 1, 1, green},
		{3, 1, 1, red}, // frame 2 was disposed to the previous canvas
		{3, 0, 0, clear},
		{3, 2, 2, yellow},
	} {
		if got := at(c.frame, c.x, c.y); got != c.want {
			t.Errorf("frame %d at (%d,%d) = %v, want %v", c.frame, c.x, c.y, got, c.want)
		}
	}
}

func TestGIFFramesSpread(t *testing.T) {
	var frames []gifFrame
	for i := range 10 {
		frames = append(frames, gifFrame{full: true, c: color.Gray{Y: uint8(i * 20)}, delay: 10})
	}
	got, count, _, err := gifFrames(gifOf(t, frames), 6)
	if err != nil {
		t.Fatal(err)
	}
	if count != 10 || len(got) != 6 {
		t.Fatalf("count = %d, frames = %d; want 10, 6", count, len(got))
	}
	first, _ := png.Decode(bytes.NewReader(got[0]))
	last, _ := png.Decode(bytes.NewReader(got[5]))
	if y := color.GrayModel.Convert(first.At(0, 0)).(color.Gray).Y; y != 0 {
		t.Errorf("first frame gray = %d, want the first frame", y)
	}
	if y := color.GrayModel.Convert(last.At(0, 0)).(color.Gray).Y; y != 180 {
		t.Errorf("last frame gray = %d, want the last frame", y)
	}
}

func TestAnimationsAreSentAsFrames(t *testing.T) {
	h := newHarness(t, 1)
	h.steps[0].answers = []answer{says(picture), says(picture)}
	animated := gifOf(t, []gifFrame{
		{full: true, c: red, delay: 10}, {full: true, c: blue, delay: 10}, {full: true, c: green, delay: 10}, {full: true, c: yellow, delay: 10},
	})
	still := gifOf(t, []gifFrame{{full: true, c: red, delay: 10}, {full: true, c: blue, delay: 10}})
	a := h.image("https://qq/anim", animated)
	a.Role, a.MediaType = otogi.MediaAnimation, "image/gif"
	s := h.image("https://qq/still", still)
	if err := h.handle(a, s); err != nil {
		t.Fatal(err)
	}
	parts := h.steps[0].requests[0].Turns[0].Content.Parts
	if len(parts) != 5 || !strings.Contains(parts[0].Text, "4 frames") || parts[1].Media.MediaType != "image/png" {
		t.Fatalf("animation sent as %d parts, first %q", len(parts), parts[0].Text)
	}
	if parts := h.steps[0].requests[1].Turns[0].Content.Parts; len(parts) != 2 || parts[0].Text != "Describe this picture." {
		t.Fatalf("a GIF of two frames sent as %d parts, first %q", len(parts), parts[0].Text)
	}
	h.host.mu.Lock()
	defer h.host.mu.Unlock()
	if p, _ := h.host.records[0].Payload.(map[string]any); p["frames"] != 4 {
		t.Fatalf("frames recorded = %v, want 4", p["frames"])
	}
}

func TestVideos(t *testing.T) {
	h := newHarness(t, 1)
	h.steps[0].answers = []answer{says("description: 有人在弹琴\ntext:")}
	// The start of an MP4: a file type box naming mp41 among its brands.
	mp4 := append([]byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isommp41"), make([]byte, 64)...)
	v := otogi.Media{Role: otogi.MediaVideo, MediaType: "video/mp4", Source: otogi.MediaSource{Kind: otogi.MediaSourceExternal, URL: "https://qq/v"}}
	h.inv.files["https://qq/v"] = mp4
	if err := h.handle(v); err != nil {
		t.Fatal(err)
	}
	d, ok, err := h.m.Lookup(context.Background(), from, v)
	if err != nil || !ok || d != (contract.Description{Kind: contract.KindVideo, Summary: "有人在弹琴"}) {
		t.Fatalf("Lookup = %+v, %v, %v", d, ok, err)
	}
	req := h.steps[0].requests[0]
	if !strings.Contains(req.System, "watch") || req.Turns[0].Content.Parts[1].Media.Role != otogi.MediaVideo {
		t.Fatalf("request = %+v", req)
	}
}

func TestParse(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer string
		video  bool
		want   contract.Description
		fail   bool
	}{
		{name: "text on the same line", answer: "kind: screenshot\ndescription: 一条推文\ntext: 你好", want: contract.Description{Kind: contract.KindScreenshot, Summary: "一条推文", Text: "你好"}},
		{name: "text over lines", answer: "kind: Photo\ndescription: a desk\ntext:\nline one\nkind: not a section\n", want: contract.Description{Kind: contract.KindPhoto, Summary: "a desk", Text: "line one\nkind: not a section"}},
		{name: "no text", answer: "kind: sticker\r\ndescription: a waving cat\r\ntext:", want: contract.Description{Kind: contract.KindSticker, Summary: "a waving cat"}},
		{name: "video", answer: "description: a song\ntext: la la", video: true, want: contract.Description{Kind: contract.KindVideo, Summary: "a song", Text: "la la"}},
		{name: "missing a section", answer: "kind: photo\ndescription: a desk", fail: true},
		{name: "unknown kind", answer: "kind: painting\ndescription: a desk\ntext:", fail: true},
		{name: "empty description", answer: "kind: photo\ndescription:  \ntext: x", fail: true},
		{name: "out of order", answer: "description: a desk\nkind: photo\ntext:", fail: true},
		{name: "a preamble", answer: "Sure!\nkind: photo\ndescription: a desk\ntext:", fail: true},
		{name: "video with a kind", answer: "kind: video\ndescription: a song\ntext:", video: true, fail: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := parse(c.answer, c.video)
			if c.fail {
				if err == nil {
					t.Fatalf("parse = %+v, want an error", got)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("parse = %+v, %v; want %+v", got, err, c.want)
			}
		})
	}
}

func TestPrune(t *testing.T) {
	h := newHarness(t, 1)
	ctx := context.Background()
	day := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	old, recent := key{"md5", []byte{1}}, key{"md5", []byte{2}}
	d := contract.Description{Kind: contract.KindPhoto, Summary: "x"}
	if err := h.m.store.put(ctx, old, d, "m", day); err != nil {
		t.Fatal(err)
	}
	if err := h.m.store.put(ctx, recent, d, "m", day); err != nil {
		t.Fatal(err)
	}
	if err := h.m.store.seen(ctx, recent, day.AddDate(0, 0, 20)); err != nil {
		t.Fatal(err)
	}
	if err := h.m.store.putSource(ctx, "url:old", old, day); err != nil {
		t.Fatal(err)
	}
	later := day.AddDate(0, 0, 10)
	pic := []otogi.Media{{Role: otogi.MediaImage}}
	if err := h.m.store.putRecent(ctx, from, "old-message", pic, later.Add(-3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := h.m.store.putRecent(ctx, from, "new-message", pic, later.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.m.store.addSticker(ctx, sticker{SHA256: []byte{9}, MediaType: "image/png", Bytes: []byte("png")}, "qq:"+operator, day, h.m.limits); err != nil {
		t.Fatal(err)
	}
	n, err := h.m.store.prune(ctx, later, later.Add(-recentMedia))
	if err != nil || n != 3 {
		t.Fatalf("prune = %d, %v; want 3 rows", n, err)
	}
	if _, ok, _ := h.m.store.recent(ctx, from, "old-message", time.Time{}); ok {
		t.Error("the old message's media is still kept")
	}
	if _, ok, _ := h.m.store.recent(ctx, from, "new-message", time.Time{}); !ok {
		t.Error("the recent message's media was pruned")
	}
	if all, _ := h.m.store.stickers(ctx); len(all) != 1 {
		t.Errorf("stickers after pruning = %d, want 1: stickers are not pruned", len(all))
	}
	if _, ok, _ := h.m.store.get(ctx, old); ok {
		t.Error("the old description is still kept")
	}
	if _, ok, _ := h.m.store.get(ctx, recent); !ok {
		t.Error("the recently seen description was pruned")
	}
	if _, ok, _ := h.m.store.source(ctx, "url:old"); ok {
		t.Error("the old source is still kept")
	}
}

func TestDescriptor(t *testing.T) {
	h := newHarness(t, 1)
	d := h.m.Descriptor()
	if d.Ordering != otogi.OrderNone || !slices.Equal(d.Provides, []string{contract.Service, contract.StickersService}) || d.Allowlists[0].Name != describeList {
		t.Fatalf("descriptor = %+v", d)
	}
	if len(d.Commands) != 1 || d.Commands[0].Name != "sticker" {
		t.Fatalf("commands = %+v", d.Commands)
	}
	if _, ok := h.host.services[contract.Service].(contract.Describer); !ok {
		t.Fatal("the media service was not provided")
	}
	if _, ok := h.host.services[contract.StickersService].(contract.Stickers); !ok {
		t.Fatal("the stickers service was not provided")
	}
	if _, err := New(Config{Steps: []Step{{Provider: "nope", Model: "m"}}}, func(string) (llm.Provider, error) { return nil, otogi.ErrInvalid }, nil); !errors.Is(err, otogi.ErrInvalid) {
		t.Fatalf("New with an unknown provider = %v", err)
	}
}
