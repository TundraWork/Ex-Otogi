package mapper

import (
	"testing"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
	"github.com/gotd/td/tg"
)

func TestMessageMetadataExtensionsAreOrdered(t *testing.T) {
	message := &tg.Message{ID: 20, PeerID: &tg.PeerChannel{ChannelID: 4}, Date: 100, Message: "metadata"}
	message.SetFwdFrom(tg.MessageFwdHeader{Date: 80, FromName: "source"})
	message.SetReplyTo(&tg.MessageReplyHeader{ReplyToMsgID: 19})
	message.SetGroupedID(123)
	message.SetReactions(tg.MessageReactions{Results: []tg.ReactionCount{{Reaction: &tg.ReactionEmoji{Emoticon: "👍"}, Count: 1}}})
	mapped, err := New(testConfig(t)).Map(&tg.UpdateNewChannelMessage{Message: message, Pts: 7, PtsCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	extensions := mapped.Event.Message.Extensions
	want := []string{"message-reply", "message-forward", "message-grouped", "message-reactions", "message-raw"}
	if len(extensions) != len(want) {
		t.Fatalf("extensions=%d", len(extensions))
	}
	for i := range want {
		if extensions[i].Name != want[i] {
			t.Fatalf("extension[%d]=%s want %s", i, extensions[i].Name, want[i])
		}
	}
}

func TestReplyMapsToTypedRelation(t *testing.T) {
	message := &tg.Message{ID: 20, PeerID: &tg.PeerUser{UserID: 4}, Date: 100, Message: "reply"}
	message.SetReplyTo(&tg.MessageReplyHeader{ReplyToMsgID: 19})
	mapped, err := New(testConfig(t)).Map(&tg.UpdateNewMessage{Message: message, Pts: 7, PtsCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	relations := mapped.Event.Message.Relations
	if len(relations) != 1 || relations[0].Kind != otogi.RelationReply || string(relations[0].Target.SourceEvent) != "telegram:common:user:4:19" {
		t.Fatalf("relations=%+v", relations)
	}
}

func TestReplyUsesTargetPeerAndMapsTopic(t *testing.T) {
	message := &tg.Message{ID: 20, PeerID: &tg.PeerChannel{ChannelID: 4}, Date: 100, Message: "reply"}
	header := &tg.MessageReplyHeader{ForumTopic: true, ReplyToMsgID: 19}
	header.SetReplyToPeerID(&tg.PeerUser{UserID: 8})
	header.SetReplyToTopID(10)
	message.SetReplyTo(header)
	mapped, err := New(testConfig(t)).Map(&tg.UpdateNewChannelMessage{Message: message, Pts: 7, PtsCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	relations := mapped.Event.Message.Relations
	if len(relations) != 2 {
		t.Fatalf("relations=%d", len(relations))
	}
	if relations[0].Kind != otogi.RelationReply || relations[0].Target.Conversation != "user:8" {
		t.Fatalf("reply=%+v", relations[0])
	}
	if relations[1].Kind != otogi.RelationThread || string(relations[1].Target.SourceEvent) != "telegram:common:user:8:10" {
		t.Fatalf("thread=%+v", relations[1])
	}
}
