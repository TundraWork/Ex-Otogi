package telegram

import (
	"context"
	"testing"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/mapper"
	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/gotd/td/tg"
)

type refetchStoreFake struct{ peer telegramstore.Peer }

func (s refetchStoreFake) GetPeer(context.Context, string, int64) (telegramstore.Peer, bool, error) {
	return s.peer, true, nil
}

type refetchAPIFake struct {
	common, channel int
	response        tg.MessagesMessagesClass
}

func (a *refetchAPIFake) MessagesGetMessages(context.Context, []tg.InputMessageClass) (tg.MessagesMessagesClass, error) {
	a.common++
	return a.response, nil
}
func (a *refetchAPIFake) ChannelsGetMessages(_ context.Context, r *tg.ChannelsGetMessagesRequest) (tg.MessagesMessagesClass, error) {
	a.channel++
	return a.response, nil
}

func TestOriginRefetchSelectsCommonAndChannelRPC(t *testing.T) {
	for _, tc := range []struct {
		name            string
		locator         mapper.MessageLocator
		peer            telegramstore.Peer
		common, channel int
	}{{"common", mapper.MessageLocator{Namespace: mapper.NamespaceCommon, Peer: "user:7", MessageID: 4}, telegramstore.Peer{Kind: "user", ID: 7, AccessHash: 8}, 1, 0}, {"channel", mapper.MessageLocator{Namespace: mapper.NamespaceChannel, Peer: "channel:9", MessageID: 5}, telegramstore.Peer{Kind: "channel", ID: 9, AccessHash: 10}, 0, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			a := &refetchAPIFake{response: &tg.MessagesMessages{Messages: []tg.MessageClass{&tg.Message{ID: tc.locator.MessageID, Message: "x"}}}}
			r := NewRefetcher(a, refetchStoreFake{peer: tc.peer})
			got, err := r.Origin(context.Background(), tc.locator)
			if err != nil {
				t.Fatal(err)
			}
			if got.GetID() != tc.locator.MessageID || a.common != tc.common || a.channel != tc.channel {
				t.Fatalf("got=%+v calls=%d/%d", got, a.common, a.channel)
			}
		})
	}
}

func TestMediaRefreshRefetchesOriginWithoutDownload(t *testing.T) {
	doc := &tg.Document{ID: 20, AccessHash: 21, FileReference: []byte{22, 23}, DCID: 4, MimeType: "x", Size: 1}
	media := &tg.MessageMediaDocument{}
	media.SetDocument(doc)
	message := &tg.Message{ID: 5, Media: media}
	message.SetMedia(media)
	a := &refetchAPIFake{response: &tg.MessagesMessages{Messages: []tg.MessageClass{message}}}
	r := NewRefetcher(a, refetchStoreFake{peer: telegramstore.Peer{Kind: "channel", ID: 9, AccessHash: 10}})
	got, err := r.RefreshMedia(context.Background(), mapper.MessageLocator{Namespace: mapper.NamespaceChannel, Peer: "channel:9", MessageID: 5}, mapper.MediaLocator{Kind: "document", ID: 20, AccessHash: 1, FileReference: []byte{1}, DCID: 4})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 20 || got.AccessHash != 21 || string(got.FileReference) != string([]byte{22, 23}) || a.channel != 1 {
		t.Fatalf("got=%+v calls=%d", got, a.channel)
	}
}
