package mapper

import (
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	platform, _ := otogi.NewPlatformID("telegram")
	account, _ := otogi.NewAccountID("deployment:42")
	tenant, _ := otogi.NewTenantID("tenant")
	return Config{Platform: platform, Account: account, Tenant: tenant, AuthorizedUserID: 42, IngestedAt: func() time.Time { return time.Unix(200, 0).UTC() }}
}

func TestMapNewAndEditMessages(t *testing.T) {
	m := &tg.Message{ID: 11, PeerID: &tg.PeerUser{UserID: 9}, Date: 100, Message: "hello"}
	m.SetFromID(&tg.PeerUser{UserID: 9})
	m.SetEntities([]tg.MessageEntityClass{&tg.MessageEntityBold{Offset: 0, Length: 5}})
	result, err := New(testConfig(t)).Map(&tg.UpdateNewMessage{Message: m, Pts: 5, PtsCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Event.Action() != otogi.ActionMessageCreated || result.Event.Conversation().String() != "user:9" || result.Event.Direction() != otogi.DirectionIncoming {
		t.Fatalf("event action=%s conversation=%s direction=%s", result.Event.Action(), result.Event.Conversation(), result.Event.Direction())
	}
	if actor, ok := result.Event.Actor(); !ok || actor.String() != "user:9" {
		t.Fatalf("actor=%v,%v", actor, ok)
	}
	parts := result.Event.Message().Parts()
	text, annotations, ok := parts[0].Text()
	if !ok || text != "hello" || len(annotations) != 1 {
		t.Fatalf("text=%q annotations=%d", text, len(annotations))
	}
	if result.SourceKey == "" || result.Box != "common" || result.Position != 5 || len(result.Locators) != 1 {
		t.Fatalf("result=%+v", result)
	}

	m.SetEditDate(150)
	edited, err := New(testConfig(t)).Map(&tg.UpdateEditMessage{Message: m, Pts: 6, PtsCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if edited.Event.Action() != otogi.ActionMessageEdited {
		t.Fatalf("action=%s", edited.Event.Action())
	}
	if got, ok := edited.Event.EditedAt(); !ok || !got.Equal(time.Unix(150, 0)) {
		t.Fatalf("edited=%v,%v", got, ok)
	}
	if edited.SourceKey == result.SourceKey {
		t.Fatal("edit source key did not include action/fingerprint")
	}
	createdID, ok := result.Event.SourceEventID()
	if !ok || createdID.String() != "telegram:common:user:9:11" {
		t.Fatalf("created source ID=%s,%v", createdID, ok)
	}
	editedID, ok := edited.Event.SourceEventID()
	if !ok || editedID != createdID {
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
	if created.Box != "channel:77" || created.Event.Conversation().String() != "channel:77" || created.Event.Direction() != otogi.DirectionOutgoing {
		t.Fatalf("created=%+v", created)
	}

	deleted, err := New(testConfig(t)).Map(&tg.UpdateDeleteChannelMessages{ChannelID: 77, Messages: []int{12, 13}, Pts: 10, PtsCount: 2})
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Box != "channel:77" || len(deleted.Deletes) != 2 || deleted.Deletes[0].Conversation != "channel:77" || deleted.Deletes[0].MessageID != 12 {
		t.Fatalf("delete box=%q deletes=%+v", deleted.Box, deleted.Deletes)
	}

	unresolved, err := New(testConfig(t)).Map(&tg.UpdateDeleteMessages{Messages: []int{99}, Pts: 11, PtsCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(unresolved.Deletes) != 1 || unresolved.Deletes[0].Conversation != "" || !unresolved.Deletes[0].Unresolved {
		t.Fatalf("unresolved=%+v", unresolved.Deletes)
	}
}

func TestMapServiceEmptyAndUnsupported(t *testing.T) {
	service := &tg.MessageService{ID: 5, PeerID: &tg.PeerChat{ChatID: 3}, Date: 100, Action: &tg.MessageActionChatCreate{Title: "group", Users: []int64{1, 2}}}
	mapped, err := New(testConfig(t)).Map(&tg.UpdateNewMessage{Message: service, Pts: 1, PtsCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if mapped.Event.Action() != otogi.ActionMessageService || len(mapped.Event.Message().Parts()) != 1 || mapped.Event.Message().Parts()[0].Kind() != otogi.PartOpaque {
		t.Fatalf("service event=%+v", mapped.Event)
	}

	empty := &tg.MessageEmpty{ID: 7, PeerID: &tg.PeerUser{UserID: 8}}
	empty.SetPeerID(&tg.PeerUser{UserID: 8})
	mapped, err = New(testConfig(t)).Map(&tg.UpdateNewMessage{Message: empty, Pts: 2, PtsCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if mapped.Event.Completeness() != otogi.CompletenessUnavailable {
		t.Fatalf("completeness=%s", mapped.Event.Completeness())
	}

	mapped, err = New(testConfig(t)).Map(&tg.UpdateUserTyping{UserID: 8, Action: &tg.SendMessageTypingAction{}})
	if err != nil {
		t.Fatal(err)
	}
	if mapped.Quarantine == nil || mapped.HasEvent {
		t.Fatalf("unsupported=%+v", mapped)
	}
}

func TestNegativePositionsAreNormalizedInResults(t *testing.T) {
	message := &tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 2}, Date: 100, Message: "position"}
	mapped, err := New(testConfig(t)).Map(&tg.UpdateNewMessage{Message: message, Pts: -2})
	if err != nil {
		t.Fatal(err)
	}
	if mapped.Position != 0 {
		t.Fatalf("message position=%d", mapped.Position)
	}
	deleted, err := New(testConfig(t)).Map(&tg.UpdateDeleteMessages{Messages: []int{1}, Pts: -2})
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Position != 0 {
		t.Fatalf("delete position=%d", deleted.Position)
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
	part := mapped.Event.Message().Parts()[0]
	value, ok := part.Value()
	if !ok {
		t.Fatal("service part not opaque")
	}
	if value.LocalName() != "message-service" {
		t.Fatalf("extension=%s", value.LocalName())
	}
	var decoded tg.MessageService
	buffer := bin.Buffer{Buf: value.Data()}
	if err := decoded.Decode(&buffer); err != nil {
		t.Fatal(err)
	}
	if !decoded.Silent || decoded.TTLPeriod != 60 || decoded.SavedPeerID == nil || decoded.Reactions.Results == nil || decoded.ReplyTo == nil {
		t.Fatalf("decoded service=%+v", decoded)
	}
	relations := mapped.Event.Message().Relations()
	if len(relations) != 2 || relations[0].Kind() != otogi.RelationReply || relations[1].Kind() != otogi.RelationThread {
		t.Fatalf("relations=%+v", relations)
	}
}
