package mapper

import (
	"testing"

	"github.com/gotd/td/tg"
)

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

func TestAuthorizedUserScopesEventAccount(t *testing.T) {
	message := &tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 2}, Date: 100, Message: "scope"}
	mapped, err := New(testConfig(t)).Map(&tg.UpdateNewMessage{Message: message, Pts: 1})
	if err != nil {
		t.Fatal(err)
	}
	if string(mapped.Event.Account) != "deployment:42:user:42" {
		t.Fatalf("account=%s", mapped.Event.Account)
	}
}
