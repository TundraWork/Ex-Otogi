package mapper

import (
	"bytes"
	"testing"

	"github.com/gotd/td/tg"
)

func TestProviderLocatorRoundTripAndRefetchSelection(t *testing.T) {
	common := MessageLocator{Namespace: NamespaceCommon, Peer: "user:12", MessageID: 44}
	encoded, err := EncodeMessageLocator(common)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeMessageLocator(encoded)
	if err != nil || decoded != common {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
	request, err := RefetchRequest(common, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := request.(*tg.MessagesGetMessagesRequest); !ok {
		t.Fatalf("common request %T", request)
	}

	channel := MessageLocator{Namespace: NamespaceChannel, Peer: "channel:99", MessageID: 45}
	request, err = RefetchRequest(channel, &tg.InputChannel{ChannelID: 99, AccessHash: 7})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := request.(*tg.ChannelsGetMessagesRequest); !ok {
		t.Fatalf("channel request %T", request)
	}
}

func TestMediaLocatorRoundTrip(t *testing.T) {
	in := MediaLocator{Kind: "document", ID: 1, AccessHash: 2, FileReference: []byte{3, 4}, DCID: 5, Variant: "video-720"}
	data, err := EncodeMediaLocator(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := DecodeMediaLocator(data)
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != in.Kind || out.ID != in.ID || out.AccessHash != in.AccessHash || out.DCID != in.DCID || out.Variant != in.Variant || !bytes.Equal(out.FileReference, in.FileReference) {
		t.Fatalf("out=%+v", out)
	}
}

func TestQuarantineUpdateIsBoundedDeterministic(t *testing.T) {
	update := &tg.UpdateUserTyping{UserID: 7, Action: &tg.SendMessageTypingAction{}}
	first, err := Quarantine(update, "account:1", 12)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Quarantine(update, "account:1", 12)
	if err != nil {
		t.Fatal(err)
	}
	if first.SourceKey != second.SourceKey || !bytes.Equal(first.Payload, second.Payload) {
		t.Fatal("quarantine encoding is not deterministic")
	}
	if first.Kind != "telegram.layer228.updateUserTyping" || len(first.Payload) == 0 || len(first.Payload) > MaxPreservationBytes {
		t.Fatalf("quarantine=%+v", first)
	}
}
