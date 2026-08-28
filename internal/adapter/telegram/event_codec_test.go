package telegram

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func TestEventCodecRoundTripsRichGraphDeterministically(t *testing.T) {
	input := richCodecEvent(t)
	first, err := EncodeEventInput(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EncodeEventInput(input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("encoding is not deterministic")
	}
	got, err := DecodeEventInput(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := EncodeEventInput(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, roundTrip) {
		t.Fatalf("round trip differs\nfirst: %x\ngot:   %x", first, roundTrip)
	}
	if len(got.Message().Parts()) != 10 || len(got.Extensions()) != 2 || got.Extensions()[0].LocalName() != "duplicate" || got.Extensions()[1].LocalName() != "duplicate" {
		t.Fatal("rich graph or duplicate extensions lost")
	}
}

func TestEventCodecRejectsInvalidInput(t *testing.T) {
	if _, err := EncodeEventInput(otogi.EventInput{}); !errors.Is(err, otogi.ErrInvalid) {
		t.Fatalf("EncodeEventInput error=%v, want ErrInvalid", err)
	}
}

func TestClassifyCodecErrorPreservesInvalidAndCause(t *testing.T) {
	cause := errors.New("codec cause")
	err := classifyCodecError(cause)
	if !errors.Is(err, otogi.ErrInvalid) {
		t.Fatalf("classifyCodecError error=%v, want ErrInvalid", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("classifyCodecError error=%v, want underlying cause", err)
	}
}

func TestEventCodecRejectsInvalidEnvelope(t *testing.T) {
	valid, err := EncodeEventInput(richCodecEvent(t))
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string][]byte{
		"empty":           nil,
		"truncated":       valid[:len(valid)-1],
		"trailing":        append(append([]byte(nil), valid...), 0),
		"unknown version": func() []byte { b := append([]byte(nil), valid...); binary.BigEndian.PutUint16(b[4:6], 2); return b }(),
		"corrupt":         append([]byte(nil), valid...),
		"oversize":        make([]byte, maxEncodedEventBytes+1),
	}
	tests["corrupt"][0] ^= 0xff
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeEventInput(context.Background(), data); !errors.Is(err, otogi.ErrInvalid) {
				t.Fatalf("DecodeEventInput error=%v, want ErrInvalid", err)
			}
		})
	}
}

func TestEventCodecHonorsCancellationAndCountBounds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := DecodeEventInput(ctx, []byte("anything")); !errors.Is(err, context.Canceled) || errors.Is(err, otogi.ErrInvalid) {
		t.Fatalf("cancellation error=%v", err)
	}
	// A syntactically framed payload declaring too many roots must fail before allocating them.
	w := codecTestPreamble()
	w.u32(maxCodecCollectionCount + 1)
	if _, err := DecodeEventInput(context.Background(), w.Bytes()); !errors.Is(err, otogi.ErrInvalid) {
		t.Fatalf("excessive count error=%v, want ErrInvalid", err)
	}

	// Seventeen linked parts exceed the public graph's maximum depth of sixteen.
	w = codecTestPreamble()
	w.u32(1)
	w.str("p0")
	w.u32(17)
	for i := 0; i < 17; i++ {
		w.str("p" + string(rune('0'+i)))
		w.str(string(otogi.PartText))
		if i < 16 {
			w.u32(1)
			w.str("p" + string(rune('0'+i+1)))
		} else {
			w.u32(0)
		}
		w.u32(0)
		w.str("x")
		w.u32(0)
	}
	w.u32(0)
	w.u32(0)
	w.u32(0)
	if _, err := DecodeEventInput(context.Background(), w.Bytes()); !errors.Is(err, otogi.ErrInvalid) {
		t.Fatalf("excessive depth error=%v, want ErrInvalid", err)
	}
}

func codecTestPreamble() *eventWriter {
	w := &eventWriter{}
	w.Write(codecMagic[:])
	_ = binary.Write(&w.Buffer, binary.BigEndian, codecVersion)
	for _, value := range []string{"platform", "account", "tenant", "conversation", "", "", "message.created", "incoming", "2026-08-29T00:00:00Z", "2026-08-29T00:00:01Z", "", "", "", "partial"} {
		w.str(value)
	}
	w.u8(0)
	return w
}

func FuzzDecodeEventInput(f *testing.F) {
	encoded, err := EncodeEventInput(richCodecEvent(f))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte{})
	f.Add([]byte("OTGE\x00\x01"))
	f.Fuzz(func(t *testing.T, data []byte) {
		decoded, err := DecodeEventInput(context.Background(), data)
		if err != nil {
			return
		}
		if _, err := EncodeEventInput(decoded); err != nil {
			t.Fatalf("decoded invalid event: %v", err)
		}
	})
}

func richCodecEvent(t testing.TB) otogi.EventInput {
	t.Helper()
	ext := func(name string, data string) otogi.Extension {
		v, err := otogi.NewExtension("https://example.test/ext", name, 7, "application/octet-stream", []byte(data))
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	id := func(s string) otogi.PartID {
		v, err := otogi.NewPartID(s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	ids := []otogi.PartID{id("text"), id("media"), id("file"), id("external"), id("location"), id("contact"), id("poll"), id("interactive"), id("embedded"), id("opaque")}
	ann, err := otogi.NewAnnotation("a", 0, 4, otogi.AnnotationLink, "https://example.test", []otogi.Extension{ext("annotation", "a")})
	if err != nil {
		t.Fatal(err)
	}
	text, err := otogi.NewTextPart(ids[0], "text", []otogi.Annotation{ann}, nil, []otogi.Extension{ext("part", "p")})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := otogi.NewMediaHash("sha256", []byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	locator, err := otogi.NewProviderLocator("https://example.test/locator", 3, []byte("opaque"))
	if err != nil {
		t.Fatal(err)
	}
	opaqueSource, err := otogi.NewOpaqueMediaSource(locator, []otogi.Extension{ext("source", "s")})
	if err != nil {
		t.Fatal(err)
	}
	mediaValue, err := otogi.NewMedia(otogi.MediaParams{Role: otogi.MediaImage, MediaType: "image/png", Filename: "x.png", ByteSize: 3, Width: 2, Height: 1, Duration: time.Second, Hashes: []otogi.MediaHash{hash}, Source: opaqueSource, Extensions: []otogi.Extension{ext("media", "m")}})
	if err != nil {
		t.Fatal(err)
	}
	media, err := otogi.NewMediaPart(ids[1], otogi.PartMedia, mediaValue, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	inline, err := otogi.NewInlineMediaSource([]byte("file"), nil)
	if err != nil {
		t.Fatal(err)
	}
	fileValue, err := otogi.NewMedia(otogi.MediaParams{Role: otogi.MediaDocument, MediaType: "application/octet-stream", Source: inline})
	if err != nil {
		t.Fatal(err)
	}
	file, err := otogi.NewMediaPart(ids[2], otogi.PartFile, fileValue, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	externalSource, err := otogi.NewExternalMediaSource("https://example.test/media", nil)
	if err != nil {
		t.Fatal(err)
	}
	externalValue, err := otogi.NewMedia(otogi.MediaParams{Role: otogi.MediaVideo, MediaType: "video/mp4", Source: externalSource})
	if err != nil {
		t.Fatal(err)
	}
	external, err := otogi.NewMediaPart(ids[3], otogi.PartMedia, externalValue, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	locationValue, _ := otogi.NewLocation(otogi.LocationParams{Latitude: 1.25, Longitude: -2.5, Label: "place"})
	location, _ := otogi.NewLocationPart(ids[4], locationValue, nil, nil)
	contactValue, _ := otogi.NewContact(otogi.ContactParams{DisplayName: "Ada", Address: "ada@example.test"})
	contact, _ := otogi.NewContactPart(ids[5], contactValue, nil, nil)
	pollValue, _ := otogi.NewPoll(otogi.PollParams{Question: "Q?", Options: []string{"A", "B"}, Multiple: true})
	poll, _ := otogi.NewPollPart(ids[6], pollValue, nil, nil)
	interactiveValue, _ := otogi.NewInteractive(otogi.InteractiveParams{Layout: "row", ActionID: "go", Label: "Go"})
	interactive, _ := otogi.NewInteractivePart(ids[7], interactiveValue, []otogi.PartID{ids[0]}, nil)
	embeddedValue, _ := otogi.NewEmbeddedMessage(otogi.EmbeddedMessageParams{Summary: "summary", Reference: "ref"})
	embedded, _ := otogi.NewEmbeddedMessagePart(ids[8], embeddedValue, []otogi.PartID{ids[1]}, nil)
	opaque, _ := otogi.NewOpaquePart(ids[9], ext("opaque", "value"), nil, nil)
	ref, err := otogi.NewEventRef(otogi.EventRefParams{Platform: mustCodec(t, otogi.NewPlatformID, "telegram"), Account: mustCodec(t, otogi.NewAccountID, "account"), Conversation: mustCodec(t, otogi.NewConversationID, "other"), SourceEvent: mustCodec(t, otogi.NewSourceEventID, "target"), Locator: locator})
	if err != nil {
		t.Fatal(err)
	}
	relation, err := otogi.NewRelation(otogi.RelationReply, ref, "key", []otogi.Extension{ext("relation", "r")})
	if err != nil {
		t.Fatal(err)
	}
	message, err := otogi.NewMessage(ids, []otogi.Part{text, media, file, external, location, contact, poll, interactive, embedded, opaque}, []otogi.Relation{relation}, []otogi.Extension{ext("message", "e")})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 29, 1, 2, 3, 456, time.FixedZone("x", 3600))
	input, err := otogi.NewEventInput(otogi.EventInputParams{Platform: mustCodec(t, otogi.NewPlatformID, "telegram"), Account: mustCodec(t, otogi.NewAccountID, "account"), Tenant: mustCodec(t, otogi.NewTenantID, "tenant"), Conversation: mustCodec(t, otogi.NewConversationID, "conversation"), SourceEventID: mustCodec(t, otogi.NewSourceEventID, "source"), Actor: mustCodec(t, otogi.NewActorID, "actor"), Action: otogi.ActionMessageEdited, Direction: otogi.DirectionIncoming, OccurredAt: now, IngestedAt: now.Add(time.Second), EditedAt: now.Add(2 * time.Second), TraceID: mustCodec(t, otogi.NewTraceID, "trace"), CausalEventID: mustCodec(t, otogi.NewCausalEventID, "cause"), Completeness: otogi.CompletenessComplete, ProviderLocator: locator, Message: message, Extensions: []otogi.Extension{ext("duplicate", "one"), ext("duplicate", "two")}})
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func mustCodec[T any](t testing.TB, constructor func(string) (T, error), value string) T {
	t.Helper()
	got, err := constructor(value)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
