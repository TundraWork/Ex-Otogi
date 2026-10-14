package mapper

import (
	"reflect"
	"testing"

	"github.com/gotd/td/tg"
)

func TestLocatorRoundTrip(t *testing.T) {
	message := MessageLocator{Namespace: NamespaceChannel, Peer: "channel:99", MessageID: 45}
	data, err := EncodeMessageLocator(message)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := DecodeMessageLocator(data); err != nil || got != message {
		t.Fatalf("message locator = %+v, %v", got, err)
	}
	media := MediaLocator{Kind: "document", ID: 1, AccessHash: 2, FileReference: []byte{3, 4}, DCID: 5}
	data, err = EncodeMediaLocator(media)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := DecodeMediaLocator(data); err != nil || !reflect.DeepEqual(got, media) {
		t.Fatalf("media locator = %+v, %v", got, err)
	}
}

func TestRouted(t *testing.T) {
	channelMessage := &tg.Message{ID: 1, PeerID: &tg.PeerChannel{ChannelID: 77}}
	tests := []struct {
		update tg.UpdateClass
		box    string
		pts    int
		ok     bool
	}{
		{&tg.UpdateNewMessage{Message: &tg.Message{PeerID: &tg.PeerUser{UserID: 1}}, Pts: 5}, "common", 5, true},
		{&tg.UpdateEditMessage{Message: &tg.Message{PeerID: &tg.PeerUser{UserID: 1}}, Pts: 6}, "common", 6, true},
		{&tg.UpdateDeleteMessages{Messages: []int{1}, Pts: 7}, "common", 7, true},
		{&tg.UpdateNewChannelMessage{Message: channelMessage, Pts: 8}, "channel:77", 8, true},
		{&tg.UpdateEditChannelMessage{Message: channelMessage, Pts: 9}, "channel:77", 9, true},
		{&tg.UpdateDeleteChannelMessages{ChannelID: 77, Messages: []int{1}, Pts: 10}, "channel:77", 10, true},
		{&tg.UpdateUserTyping{UserID: 1, Action: &tg.SendMessageTypingAction{}}, "", 0, false},
	}
	for _, test := range tests {
		box, pts, ok := Routed(test.update)
		if box != test.box || pts != test.pts || ok != test.ok {
			t.Errorf("Routed(%T) = %q, %d, %v; want %q, %d, %v", test.update, box, pts, ok, test.box, test.pts, test.ok)
		}
	}
}
