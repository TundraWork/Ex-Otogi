package telegram

import (
	"context"
	"errors"
	"fmt"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/mapper"
	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/gotd/td/tg"
)

type refetchAPI interface {
	MessagesGetMessages(context.Context, []tg.InputMessageClass) (tg.MessagesMessagesClass, error)
	ChannelsGetMessages(context.Context, *tg.ChannelsGetMessagesRequest) (tg.MessagesMessagesClass, error)
}
type peerStore interface {
	GetPeer(context.Context, string, int64) (telegramstore.Peer, bool, error)
}

// Refetcher resolves durable provider locators without downloading media bytes.
type Refetcher struct {
	api   refetchAPI
	peers peerStore
}

// NewRefetcher constructs a locator refetcher that resolves durable peers through peers and performs only metadata RPCs through api.
func NewRefetcher(api refetchAPI, peers peerStore) *Refetcher {
	if api == nil || peers == nil {
		panic("telegram: nil refetch dependency")
	}
	return &Refetcher{api: api, peers: peers}
}

// Origin fetches the message selected by a durable origin locator.
func (r *Refetcher) Origin(ctx context.Context, locator mapper.MessageLocator) (tg.MessageClass, error) {
	var response tg.MessagesMessagesClass
	ids := []tg.InputMessageClass{&tg.InputMessageID{ID: locator.MessageID}}
	switch locator.Namespace {
	case mapper.NamespaceCommon:
		var err error
		response, err = r.api.MessagesGetMessages(ctx, ids)
		if err != nil {
			return nil, err
		}
	case mapper.NamespaceChannel:
		kind, id, err := parsePeer(locator.Peer)
		if err != nil || kind != "channel" {
			return nil, fmt.Errorf("telegram: invalid channel locator")
		}
		record, ok, err := r.peers.GetPeer(ctx, kind, id)
		if err != nil {
			return nil, err
		}
		if !ok || record.AccessHash == 0 {
			return nil, errors.New("telegram: channel access hash missing")
		}
		response, err = r.api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{Channel: &tg.InputChannel{ChannelID: id, AccessHash: record.AccessHash}, ID: ids})
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("telegram: unsupported locator namespace %q", locator.Namespace)
	}
	modified, ok := response.AsModified()
	if !ok {
		return nil, errors.New("telegram: refetch response not modified")
	}
	for _, message := range modified.GetMessages() {
		if message.GetID() == locator.MessageID {
			return message, nil
		}
	}
	return nil, errors.New("telegram: origin message not found")
}

// RefreshMedia refetches the origin and returns refreshed access material only.
func (r *Refetcher) RefreshMedia(ctx context.Context, origin mapper.MessageLocator, stale mapper.MediaLocator) (mapper.MediaLocator, error) {
	message, err := r.Origin(ctx, origin)
	if err != nil {
		return mapper.MediaLocator{}, err
	}
	m, ok := message.(*tg.Message)
	if !ok {
		return mapper.MediaLocator{}, errors.New("telegram: origin message unavailable")
	}
	media, ok := m.GetMedia()
	if !ok {
		return mapper.MediaLocator{}, errors.New("telegram: origin has no media")
	}
	switch v := media.(type) {
	case *tg.MessageMediaDocument:
		if d, ok := v.Document.(*tg.Document); ok && stale.Kind == "document" && d.ID == stale.ID {
			if len(d.FileReference) > 16<<10 {
				return mapper.MediaLocator{}, errors.New("telegram: refreshed document locator too large")
			}
			return mapper.MediaLocator{Kind: "document", ID: d.ID, AccessHash: d.AccessHash, FileReference: append([]byte(nil), d.FileReference...), DCID: d.DCID, Variant: stale.Variant}, nil
		}
	case *tg.MessageMediaPhoto:
		if p, ok := v.Photo.(*tg.Photo); ok && stale.Kind == "photo" && p.ID == stale.ID {
			if len(p.FileReference) > 16<<10 {
				return mapper.MediaLocator{}, errors.New("telegram: refreshed photo locator too large")
			}
			return mapper.MediaLocator{Kind: "photo", ID: p.ID, AccessHash: p.AccessHash, FileReference: append([]byte(nil), p.FileReference...), DCID: p.DCID, Variant: stale.Variant}, nil
		}
	}
	return mapper.MediaLocator{}, errors.New("telegram: media identity not found in origin")
}
