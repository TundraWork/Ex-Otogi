package otogi

import (
	"encoding/json"
	"os"
	"testing"
)

func readFixture[T any](t *testing.T, name string) T {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestTelegramEntityMediaReplyCorpus(t *testing.T) {
	fixture := readFixture[struct {
		Text         string                       `json:"text"`
		Entity       struct{ Offset, Length int } `json:"entity"`
		MediaLocator string                       `json:"media_locator"`
		Reply        string                       `json:"reply"`
	}](t, "telegram")
	entity, _ := NewAnnotation("entity", fixture.Entity.Offset, fixture.Entity.Length, AnnotationBold, "", nil)
	textID := mustRichPartID(t, "text")
	text, err := NewTextPart(textID, fixture.Text, []Annotation{entity}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	locator, _ := NewProviderLocator("https://telegram.org/media", 1, []byte(fixture.MediaLocator))
	source, _ := NewOpaqueMediaSource(locator, nil)
	mediaValue, _ := NewMedia(MediaParams{Role: MediaImage, MediaType: "image/jpeg", Source: source})
	mediaID := mustRichPartID(t, "media")
	media, _ := NewMediaPart(mediaID, PartMedia, mediaValue, nil, nil)
	platform, _ := NewPlatformID("telegram")
	account, _ := NewAccountID("bot")
	conversation, _ := NewConversationID("chat")
	event, _ := NewSourceEventID(fixture.Reply)
	ref, _ := NewEventRef(EventRefParams{Platform: platform, Account: account, Conversation: conversation, SourceEvent: event})
	reply, _ := NewRelation(RelationReply, ref, "", nil)
	message, err := NewMessage([]PartID{textID, mediaID}, []Part{text, media}, []Relation{reply}, nil)
	if err != nil || len(message.Parts()) != 2 || message.Relations()[0].Kind() != RelationReply {
		t.Fatalf("telegram corpus: %v", err)
	}
}

func TestMatrixReplacementCorpus(t *testing.T) {
	fixture := readFixture[struct{ Text, Replacement string }](t, "matrix")
	id := mustRichPartID(t, "body")
	part, _ := NewTextPart(id, fixture.Text, nil, nil, nil)
	platform, _ := NewPlatformID("matrix")
	account, _ := NewAccountID("bot")
	conversation, _ := NewConversationID("room")
	event, _ := NewSourceEventID(fixture.Replacement)
	ref, _ := NewEventRef(EventRefParams{Platform: platform, Account: account, Conversation: conversation, SourceEvent: event})
	relation, _ := NewRelation(RelationReplacement, ref, "", nil)
	message, err := NewMessage([]PartID{id}, []Part{part}, []Relation{relation}, nil)
	if err != nil || message.Relations()[0].Kind() != RelationReplacement {
		t.Fatalf("matrix corpus: %v", err)
	}
}

func TestDiscordComponentAndRedactedCorpus(t *testing.T) {
	fixture := readFixture[struct{ Completeness, Component, Opaque string }](t, "discord")
	if fixture.Completeness != "redacted" {
		t.Fatal("fixture lost completeness")
	}
	codec := mustRichExtension(t, "https://discord.com/components", fixture.Component, []byte(fixture.Opaque))
	id := mustRichPartID(t, "component")
	part, _ := NewTypedPart(id, PartInteractive, codec, nil, nil)
	message, err := NewMessage([]PartID{id}, []Part{part}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, ok := message.Parts()[0].Value()
	if !ok || value.LocalName() != "button" {
		t.Fatal("discord component not represented")
	}
	if _, err := NewMessage(nil, nil, nil, nil); err != nil {
		t.Fatalf("redacted empty message: %v", err)
	}
}

func TestXMPPUnknownExtensionCorpus(t *testing.T) {
	fixture := readFixture[struct{ Namespace, Element string }](t, "xmpp")
	extension := mustRichExtension(t, fixture.Namespace, "x", []byte(fixture.Element))
	id := mustRichPartID(t, "unknown")
	part, _ := NewOpaquePart(id, extension, nil, nil)
	message, err := NewMessage([]PartID{id}, []Part{part}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, _ := message.Parts()[0].Value()
	if string(value.Data()) != fixture.Element {
		t.Fatal("XMPP bytes changed")
	}
}

func TestMIMEContentIDTreeCorpus(t *testing.T) {
	fixture := readFixture[struct{ Roots, Children []string }](t, "mime")
	rootID := mustRichPartID(t, fixture.Roots[0])
	childIDs := []PartID{mustRichPartID(t, fixture.Children[0]), mustRichPartID(t, fixture.Children[1])}
	codec := mustRichExtension(t, "https://www.rfc-editor.org/rfc/rfc2046", "multipart", []byte("mixed"))
	root, _ := NewTypedPart(rootID, PartEmbeddedMessage, codec, childIDs, nil)
	left, _ := NewTextPart(childIDs[0], "a", nil, nil, nil)
	right, _ := NewTextPart(childIDs[1], "b", nil, nil, nil)
	message, err := NewMessage([]PartID{rootID}, []Part{root, left, right}, nil, nil)
	if err != nil || len(message.Parts()[0].Children()) != 2 {
		t.Fatalf("MIME tree: %v", err)
	}
}

func TestQQOrderedReplyForwardOpaqueCorpus(t *testing.T) {
	fixture := readFixture[struct {
		Ordered        []string
		Reply, Forward string
	}](t, "qq")
	parts := make([]Part, len(fixture.Ordered))
	roots := make([]PartID, len(parts))
	for i, name := range fixture.Ordered {
		roots[i] = mustRichPartID(t, name)
		if name == "text" {
			parts[i], _ = NewTextPart(roots[i], "hello", nil, nil, nil)
			continue
		}
		codec := mustRichExtension(t, "https://im.qq.com/elements", name, []byte(name))
		parts[i], _ = NewOpaquePart(roots[i], codec, nil, nil)
	}
	platform, _ := NewPlatformID("qq")
	account, _ := NewAccountID("bot")
	conversation, _ := NewConversationID("group")
	relations := make([]Relation, 0, 2)
	for _, item := range []struct {
		id   string
		kind RelationKind
	}{{fixture.Reply, RelationReply}, {fixture.Forward, RelationForward}} {
		source, _ := NewSourceEventID(item.id)
		ref, _ := NewEventRef(EventRefParams{Platform: platform, Account: account, Conversation: conversation, SourceEvent: source})
		relation, _ := NewRelation(item.kind, ref, "", nil)
		relations = append(relations, relation)
	}
	message, err := NewMessage(roots, parts, relations, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, part := range message.Parts() {
		if part.ID() != roots[i] {
			t.Fatal("QQ order changed")
		}
	}
	if message.Relations()[0].Kind() != RelationReply || message.Relations()[1].Kind() != RelationForward {
		t.Fatal("QQ relations changed")
	}
}
