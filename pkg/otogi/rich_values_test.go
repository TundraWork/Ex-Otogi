package otogi

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func mustRichExtension(t *testing.T, namespace, local string, data []byte) Extension {
	t.Helper()
	v, err := NewExtension(namespace, local, 1, "application/octet-stream", data)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func mustRichPartID(t *testing.T, value string) PartID {
	t.Helper()
	v, err := NewPartID(value)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestExtensionAndProviderLocatorAreImmutableAndBounded(t *testing.T) {
	raw := []byte{1, 2, 3}
	ext, err := NewExtension("https://example.test/ns", "entity", 1, "application/cbor", raw)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = 9
	got := ext.Data()
	got[1] = 9
	if !bytes.Equal(ext.Data(), []byte{1, 2, 3}) {
		t.Fatalf("extension bytes changed: %v", ext.Data())
	}
	if ext.NamespaceURI() != "https://example.test/ns" || ext.LocalName() != "entity" || ext.SchemaVersion() != 1 || ext.MediaType() != "application/cbor" {
		t.Fatal("extension metadata not preserved")
	}
	if _, err := NewExtension("relative", "entity", 1, "application/cbor", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("relative namespace: %v", err)
	}
	if _, err := NewExtension("https://example.test/ns", "entity", 1, "application/cbor", make([]byte, 64*1024+1)); !errors.Is(err, ErrCapacity) {
		t.Fatalf("oversized extension: %v", err)
	}

	locatorRaw := []byte{4, 5}
	locator, err := NewProviderLocator("https://example.test/locator", 2, locatorRaw)
	if err != nil {
		t.Fatal(err)
	}
	locatorRaw[0] = 0
	copyOut := locator.Data()
	copyOut[1] = 0
	if !bytes.Equal(locator.Data(), []byte{4, 5}) {
		t.Fatal("locator did not defensively own bytes")
	}
	if _, err := NewProviderLocator("https://example.test/locator", 1, make([]byte, 16*1024+1)); !errors.Is(err, ErrCapacity) {
		t.Fatalf("oversized locator: %v", err)
	}
}

func TestTextPartValidatesUTF8RangesAndDefensiveOwnership(t *testing.T) {
	ext := mustRichExtension(t, "https://telegram.org/tl", "entity", []byte{7})
	annotation, err := NewAnnotation("bold-1", 0, len("é"), AnnotationBold, "", []Extension{ext})
	if err != nil {
		t.Fatal(err)
	}
	id := mustRichPartID(t, "body")
	annotations := []Annotation{annotation}
	extensions := []Extension{ext, ext}
	part, err := NewTextPart(id, "éx", annotations, nil, extensions)
	if err != nil {
		t.Fatal(err)
	}
	annotations[0] = Annotation{}
	extensions[0] = Extension{}
	text, gotAnnotations, ok := part.Text()
	if !ok || text != "éx" || len(gotAnnotations) != 1 || gotAnnotations[0].ID() != "bold-1" {
		t.Fatalf("unexpected text payload: %q %#v %v", text, gotAnnotations, ok)
	}
	gotAnnotations[0] = Annotation{}
	if again, _, _ := part.Text(); again != "éx" {
		t.Fatal("part changed through returned annotation slice")
	}
	if len(part.Extensions()) != 2 {
		t.Fatal("duplicate ordered extensions were not preserved")
	}
	bad, _ := NewAnnotation("bad", 1, 1, AnnotationBold, "", nil)
	if _, err := NewTextPart(id, "éx", []Annotation{bad}, nil, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("non-rune boundary accepted: %v", err)
	}
	outside, _ := NewAnnotation("outside", 0, 4, AnnotationBold, "", nil)
	if _, err := NewTextPart(id, "éx", []Annotation{outside}, nil, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("out-of-range annotation accepted: %v", err)
	}
	if _, err := NewTextPart(id, strings.Repeat("a", 64*1024+1), nil, nil, nil); !errors.Is(err, ErrCapacity) {
		t.Fatalf("oversized text: %v", err)
	}
	if _, err := NewTextPart(id, "x", make([]Annotation, 129), nil, nil); !errors.Is(err, ErrCapacity) {
		t.Fatalf("too many annotations: %v", err)
	}
}
func TestTextPartRejectsEmptyWithoutAnnotations(t *testing.T) {
	id := mustRichPartID(t, "empty")
	if _, err := NewTextPart(id, "", nil, nil, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty unannotated text: %v", err)
	}
	annotation, err := NewAnnotation("marker", 0, 0, AnnotationBold, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewTextPart(id, "", []Annotation{annotation}, nil, nil); err != nil {
		t.Fatalf("annotation-only text: %v", err)
	}
}

func TestMediaHashRejectsOversizedValueBeforeCopy(t *testing.T) {
	if _, err := NewMediaHash("sha-256", make([]byte, maxNormalizedMetadata+1)); !errors.Is(err, ErrCapacity) {
		t.Fatalf("oversized hash: %v", err)
	}
}

func TestMediaRequiresExactlyOneBoundedImmutableSource(t *testing.T) {
	inline := []byte{1, 2, 3}
	source, err := NewInlineMediaSource(inline, nil)
	if err != nil {
		t.Fatal(err)
	}
	inline[0] = 9
	out, ok := source.Inline()
	if !ok {
		t.Fatal("inline source lost variant")
	}
	out[1] = 9
	again, _ := source.Inline()
	if !bytes.Equal(again, []byte{1, 2, 3}) {
		t.Fatal("inline source not immutable")
	}
	if _, err := NewInlineMediaSource(make([]byte, 256*1024+1), nil); !errors.Is(err, ErrCapacity) {
		t.Fatalf("oversized inline media: %v", err)
	}
	if _, err := NewExternalMediaSource("relative/path", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("relative media URI: %v", err)
	}
	if _, err := NewMedia(MediaParams{Role: MediaImage, MediaType: "image/png"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing media source: %v", err)
	}
	hashBytes := []byte{8, 9}
	hash, err := NewMediaHash("sha-256", hashBytes)
	if err != nil {
		t.Fatal(err)
	}
	media, err := NewMedia(MediaParams{Role: MediaImage, MediaType: "image/png", Filename: "a.png", ByteSize: 3, Width: 10, Height: 20, Duration: time.Second, Hashes: []MediaHash{hash}, Source: source})
	if err != nil {
		t.Fatal(err)
	}
	hashBytes[0] = 0
	if media.Role() != MediaImage || media.Source().Kind() != MediaSourceInline || len(media.Hashes()) != 1 {
		t.Fatal("media metadata not preserved")
	}
}

func TestMessageRejectsMalformedGraphsAndPreservesOrder(t *testing.T) {
	a := mustRichPartID(t, "a")
	b := mustRichPartID(t, "b")
	pa, _ := NewTextPart(a, "a", nil, []PartID{b}, nil)
	pb, _ := NewTextPart(b, "b", nil, nil, nil)
	ext := mustRichExtension(t, "https://example.test/x", "x", []byte{1})
	message, err := NewMessage([]PartID{a}, []Part{pb, pa}, nil, []Extension{ext, ext})
	if err != nil {
		t.Fatal(err)
	}
	parts := message.Parts()
	if len(parts) != 2 || parts[0].ID() != b || parts[1].ID() != a {
		t.Fatal("part order changed")
	}
	parts[0] = Part{}
	if got, ok := message.Part(b); !ok || got.ID() != b {
		t.Fatal("message leaked part slice")
	}
	if len(message.Extensions()) != 2 {
		t.Fatal("message lost duplicate extensions")
	}
	if _, err := NewMessage([]PartID{a, a}, []Part{pa, pb}, nil, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate root: %v", err)
	}
	if _, err := NewMessage([]PartID{a}, []Part{pa, pa}, nil, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate part ID: %v", err)
	}
	missing, _ := NewTextPart(a, "a", nil, []PartID{mustRichPartID(t, "missing")}, nil)
	if _, err := NewMessage([]PartID{a}, []Part{missing}, nil, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing child: %v", err)
	}
	cycleA, _ := NewTextPart(a, "a", nil, []PartID{b}, nil)
	cycleB, _ := NewTextPart(b, "b", nil, []PartID{a}, nil)
	if _, err := NewMessage([]PartID{a}, []Part{cycleA, cycleB}, nil, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cycle: %v", err)
	}
}
func TestMessageRejectsLeafFirstDepthOverflow(t *testing.T) {
	parts := make([]Part, 17)
	ids := make([]PartID, 17)
	for i := range ids {
		ids[i] = mustRichPartID(t, fmt.Sprintf("leaf-first-%d", i))
	}
	for i := len(parts) - 1; i >= 0; i-- {
		var children []PartID
		if i+1 < len(parts) {
			children = []PartID{ids[i+1]}
		}
		parts[len(parts)-1-i], _ = NewTextPart(ids[i], "x", nil, children, nil)
	}
	if _, err := NewMessage(ids[:1], parts, nil, nil); !errors.Is(err, ErrCapacity) {
		t.Fatalf("leaf-first depth overflow: %v", err)
	}
}

func TestMessageCountsHashBytesInMetadata(t *testing.T) {
	source, err := NewExternalMediaSource("https://example.test/media", nil)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := NewMediaHash("opaque-digest", make([]byte, maxNormalizedMetadata))
	if err != nil {
		t.Fatal(err)
	}
	media, err := NewMedia(MediaParams{Role: MediaOther, MediaType: "application/octet-stream", Hashes: []MediaHash{hash}, Source: source})
	if err != nil {
		t.Fatal(err)
	}
	id := mustRichPartID(t, "media")
	part, err := NewMediaPart(id, PartMedia, media, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewMessage([]PartID{id}, []Part{part}, nil, nil); !errors.Is(err, ErrCapacity) {
		t.Fatalf("uncharged hash bytes: %v", err)
	}
}

func TestMessageLimitsDepthCountsAndAggregateBytes(t *testing.T) {
	parts := make([]Part, 17)
	ids := make([]PartID, 17)
	for i := range ids {
		ids[i] = mustRichPartID(t, string(rune('a'+i)))
	}
	for i := range parts {
		var children []PartID
		if i+1 < len(parts) {
			children = []PartID{ids[i+1]}
		}
		parts[i], _ = NewTextPart(ids[i], "x", nil, children, nil)
	}
	if _, err := NewMessage(ids[:1], parts, nil, nil); !errors.Is(err, ErrCapacity) {
		t.Fatalf("depth overflow: %v", err)
	}
	if _, err := NewMessage(nil, make([]Part, 257), nil, nil); !errors.Is(err, ErrCapacity) {
		t.Fatalf("part count overflow: %v", err)
	}
	if _, err := NewMessage(nil, nil, make([]Relation, 65), nil); !errors.Is(err, ErrCapacity) {
		t.Fatalf("relation count overflow: %v", err)
	}
	large := mustRichExtension(t, "https://example.test/x", "x", make([]byte, 64*1024))
	msgExt := make([]Extension, 9)
	for i := range msgExt {
		msgExt[i] = large
	}
	if _, err := NewMessage(nil, nil, nil, msgExt); !errors.Is(err, ErrCapacity) {
		t.Fatalf("aggregate extension bytes overflow: %v", err)
	}
	if _, err := NewMessage(nil, nil, nil, nil); err != nil {
		t.Fatalf("empty message must represent unavailable/redacted content: %v", err)
	}
}

func TestRelationAndEventRefPreserveProviderIdentity(t *testing.T) {
	platform, _ := NewPlatformID("matrix")
	account, _ := NewAccountID("bot")
	conversation, _ := NewConversationID("room")
	source, _ := NewSourceEventID("event")
	locator, _ := NewProviderLocator("https://matrix.org/event", 1, []byte("opaque"))
	ref, err := NewEventRef(EventRefParams{Platform: platform, Account: account, Conversation: conversation, SourceEvent: source, Locator: locator})
	if err != nil {
		t.Fatal(err)
	}
	relation, err := NewRelation(RelationReplacement, ref, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if relation.Kind() != RelationReplacement || relation.Target().SourceEvent() != source || !relation.Target().HasLocator() {
		t.Fatal("relation identity not preserved")
	}
}
