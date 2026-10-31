package mapper

import (
	"errors"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{Platform: "telegram", Account: "deployment:42", IngestedAt: func() time.Time { return time.Unix(200, 0).UTC() }}
}

func TestMapNewAndEditMessages(t *testing.T) {
	m := &tg.Message{ID: 11, PeerID: &tg.PeerUser{UserID: 9}, Date: 100, Message: "hello"}
	m.SetFromID(&tg.PeerUser{UserID: 9})
	m.SetEntities([]tg.MessageEntityClass{&tg.MessageEntityBold{Offset: 0, Length: 5}})
	result, err := New(testConfig(t)).Map(&tg.UpdateNewMessage{Message: m, Pts: 5, PtsCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Events[0].Action != otogi.ActionMessageCreated || result.Events[0].Conversation.ID != "user:9" || result.Events[0].Direction != otogi.DirectionIncoming {
		t.Fatalf("event action=%s conversation=%s direction=%s", result.Events[0].Action, result.Events[0].Conversation, result.Events[0].Direction)
	}
	if result.Events[0].Actor.ID != "user:9" || result.Events[0].Conversation.Kind != otogi.ConversationDirect {
		t.Fatalf("actor=%v conversation=%v", result.Events[0].Actor, result.Events[0].Conversation)
	}
	if part := result.Events[0].Message.Parts[0]; part.Text != "hello" || len(part.Annotations) != 1 {
		t.Fatalf("text=%q annotations=%d", part.Text, len(part.Annotations))
	}
	if err := result.Events[0].Validate(); err != nil {
		t.Fatal(err)
	}
	if len(result.Locators) != 1 || result.Locators[0] != (Locator{Box: "common", MessageID: 11, Conversation: "user:9"}) {
		t.Fatalf("locators=%+v", result.Locators)
	}

	m.SetEditDate(150)
	edited, err := New(testConfig(t)).Map(&tg.UpdateEditMessage{Message: m, Pts: 6, PtsCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if edited.Events[0].Action != otogi.ActionMessageEdited {
		t.Fatalf("action=%s", edited.Events[0].Action)
	}
	if got := edited.Events[0].EditedAt; !got.Equal(time.Unix(150, 0)) {
		t.Fatalf("edited=%v", got)
	}
	createdID := result.Events[0].SourceID
	if createdID != "telegram:common:user:9:11" {
		t.Fatalf("created source ID=%s", createdID)
	}
	editedID := edited.Events[0].SourceID
	if editedID != createdID {
		t.Fatalf("edited source ID=%s created=%s", editedID, createdID)
	}
}

func TestMapChannelAndDeleteUpdates(t *testing.T) {
	m := &tg.Message{ID: 12, PeerID: &tg.PeerChannel{ChannelID: 77}, Date: 100, Message: "post", Out: true}
	m.SetFromID(&tg.PeerChannel{ChannelID: 77})
	created, err := New(testConfig(t)).Map(&tg.UpdateNewChannelMessage{Message: m, Pts: 9, PtsCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if created.Locators[0].Box != "channel:77" || created.Events[0].Conversation.ID != "channel:77" || created.Events[0].Direction != otogi.DirectionOutgoing {
		t.Fatalf("created=%+v", created)
	}

	deleted, err := New(testConfig(t)).Map(&tg.UpdateDeleteChannelMessages{ChannelID: 77, Messages: []int{12, 13}, Pts: 10, PtsCount: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted.Deletes) != 2 || deleted.Deletes[0] != (Delete{Box: "channel:77", MessageID: 12, Conversation: "channel:77"}) {
		t.Fatalf("deletes=%+v", deleted.Deletes)
	}
	if event := New(testConfig(t)).MapDeletion(deleted.Deletes[0], "channel:77"); event.Validate() != nil || event.Action != otogi.ActionMessageDeleted || event.SourceID != "telegram:channel:channel:77:12" {
		t.Fatalf("deletion event=%+v", event)
	}

	unresolved, err := New(testConfig(t)).Map(&tg.UpdateDeleteMessages{Messages: []int{99}, Pts: 11, PtsCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(unresolved.Deletes) != 1 || unresolved.Deletes[0] != (Delete{Box: "common", MessageID: 99}) {
		t.Fatalf("unresolved=%+v", unresolved.Deletes)
	}
}

func TestMapServiceEmptyAndUnsupported(t *testing.T) {
	service := &tg.MessageService{ID: 5, PeerID: &tg.PeerChat{ChatID: 3}, Date: 100, Action: &tg.MessageActionChatCreate{Title: "group", Users: []int64{1, 2}}}
	mapped, err := New(testConfig(t)).Map(&tg.UpdateNewMessage{Message: service, Pts: 1, PtsCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if mapped.Events[0].Action != otogi.ActionMessageService || len(mapped.Events[0].Message.Parts) != 1 || mapped.Events[0].Message.Parts[0].Kind != otogi.PartOpaque {
		t.Fatalf("service event=%+v", mapped.Events)
	}

	empty := &tg.MessageEmpty{ID: 7, PeerID: &tg.PeerUser{UserID: 8}}
	empty.SetPeerID(&tg.PeerUser{UserID: 8})
	mapped, err = New(testConfig(t)).Map(&tg.UpdateNewMessage{Message: empty, Pts: 2, PtsCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if mapped.Events[0].Completeness != otogi.CompletenessUnavailable {
		t.Fatalf("completeness=%s", mapped.Events[0].Completeness)
	}

	if _, err = New(testConfig(t)).Map(&tg.UpdateUserTyping{UserID: 8, Action: &tg.SendMessageTypingAction{}}); !errors.Is(err, otogi.ErrUnsupported) {
		t.Fatalf("unrouted update error = %v", err)
	}
}

func TestServicePreservesCompleteMessageAndRelations(t *testing.T) {
	service := &tg.MessageService{ID: 5, PeerID: &tg.PeerChat{ChatID: 3}, Date: 100, Action: &tg.MessageActionChatCreate{Title: "group", Users: []int64{1, 2}}, Silent: true}
	service.SetReplyTo(&tg.MessageReplyHeader{ForumTopic: true, ReplyToMsgID: 4, ReplyToTopID: 2})
	service.SetSavedPeerID(&tg.PeerUser{UserID: 7})
	service.SetTTLPeriod(60)
	service.SetReactions(tg.MessageReactions{Results: []tg.ReactionCount{{Reaction: &tg.ReactionEmoji{Emoticon: "x"}, Count: 1}}})
	mapped, err := New(testConfig(t)).Map(&tg.UpdateNewMessage{Message: service, Pts: 1})
	if err != nil {
		t.Fatal(err)
	}
	part := mapped.Events[0].Message.Parts[0]
	value := part.Opaque
	if value == nil {
		t.Fatal("service part not opaque")
	}
	if value.Name != "message-service" {
		t.Fatalf("extension=%s", value.Name)
	}
	var decoded tg.MessageService
	buffer := bin.Buffer{Buf: value.Data}
	if err := decoded.Decode(&buffer); err != nil {
		t.Fatal(err)
	}
	if !decoded.Silent || decoded.TTLPeriod != 60 || decoded.SavedPeerID == nil || decoded.Reactions.Results == nil || decoded.ReplyTo == nil {
		t.Fatalf("decoded service=%+v", decoded)
	}
	relations := mapped.Events[0].Message.Relations
	if len(relations) != 2 || relations[0].Kind != otogi.RelationReply || relations[1].Kind != otogi.RelationThread {
		t.Fatalf("relations=%+v", relations)
	}
}

func TestShortSentConfirmationsMapToNothing(t *testing.T) {
	t.Parallel()
	m := New(Config{Platform: "telegram", Account: "main"})
	result, err := m.Map(&tg.UpdateNewMessage{Message: &tg.MessageEmpty{ID: 13}})
	if err != nil || len(result.Events) != 0 || len(result.Locators) != 0 {
		t.Fatalf("result %+v, err %v", result, err)
	}
}
