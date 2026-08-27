package mapper

import (
	"testing"

	"github.com/gotd/td/tg"
)

func TestMapRejectsTypedNilAndOversizedDeletes(t *testing.T) {
	var update *tg.UpdateNewMessage
	if _, err := New(testConfig(t)).Map(update); err == nil {
		t.Fatal("typed-nil update accepted")
	}
	var message *tg.Message
	if _, err := New(testConfig(t)).Map(&tg.UpdateNewMessage{Message: message}); err == nil {
		t.Fatal("typed-nil message accepted")
	}
	if _, err := New(testConfig(t)).Map(&tg.UpdateDeleteMessages{Messages: make([]int, MaxDeleteMessages+1)}); err == nil {
		t.Fatal("oversized delete accepted")
	}
}

func TestNegativePositionDoesNotChangeSourceKey(t *testing.T) {
	message := &tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 2}, Date: 100, Message: "same"}
	negative, err := New(testConfig(t)).Map(&tg.UpdateNewMessage{Message: message, Pts: -3})
	if err != nil {
		t.Fatal(err)
	}
	zero, err := New(testConfig(t)).Map(&tg.UpdateNewMessage{Message: message, Pts: 0})
	if err != nil {
		t.Fatal(err)
	}
	if negative.SourceKey != zero.SourceKey {
		t.Fatalf("negative=%s zero=%s", negative.SourceKey, zero.SourceKey)
	}
}

func TestEntityInputIsBoundedBeforeProjection(t *testing.T) {
	entities := make([]tg.MessageEntityClass, MaxEntities+1)
	for i := range entities {
		entities[i] = &tg.MessageEntityBold{}
	}
	if _, _, err := ConvertEntities("x", entities); err == nil {
		t.Fatal("oversized entity vector accepted")
	}
	unknown := &tg.MessageEntityUnknown{Offset: 0, Length: 1}
	entities = make([]tg.MessageEntityClass, 65)
	for i := range entities {
		entities[i] = unknown
	}
	if _, _, err := ConvertEntities("x", entities); err == nil {
		t.Fatal("oversized entity preservation accepted")
	}
}

func TestAuthorizedUserScopesEventAccount(t *testing.T) {
	message := &tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 2}, Date: 100, Message: "scope"}
	mapped, err := New(testConfig(t)).Map(&tg.UpdateNewMessage{Message: message, Pts: 1})
	if err != nil {
		t.Fatal(err)
	}
	if mapped.Event.Account().String() != "deployment:42:user:42" {
		t.Fatalf("account=%s", mapped.Event.Account())
	}
	config := testConfig(t)
	config.AuthorizedUserID = 0
	if _, err := New(config).Map(&tg.UpdateNewMessage{Message: message, Pts: 1}); err == nil {
		t.Fatal("zero authorized user accepted")
	}
}
