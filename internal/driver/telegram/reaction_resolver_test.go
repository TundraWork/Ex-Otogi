package telegram

import (
	"context"
	"testing"

	"github.com/gotd/td/tg"
)

func TestGotdMessageReactionResolverResolveMessageReactionCountsUsesPrimaryCounts(t *testing.T) {
	t.Parallel()

	api := &stubGotdMessagesReactionsAPI{
		updates: &tg.Updates{
			Updates: []tg.UpdateClass{
				&tg.UpdateMessageReactions{
					MsgID: 900,
					Reactions: tg.MessageReactions{
						Results: []tg.ReactionCount{
							{
								Reaction: &tg.ReactionEmoji{Emoticon: "❤️"},
								Count:    2,
							},
						},
					},
				},
			},
		},
	}
	resolver, err := NewGotdMessageReactionResolver(api)
	if err != nil {
		t.Fatalf("new resolver failed: %v", err)
	}

	got, err := resolver.ResolveMessageReactionCounts(
		context.Background(),
		&tg.InputPeerChannel{ChannelID: 1, AccessHash: 1},
		900,
	)
	if err != nil {
		t.Fatalf("resolve counts failed: %v", err)
	}
	if got["❤️"] != 2 {
		t.Fatalf("heart count = %d, want 2", got["❤️"])
	}
	if api.listCalls != 0 {
		t.Fatalf("list calls = %d, want 0", api.listCalls)
	}
}

func TestGotdMessageReactionResolverResolveMessageReactionCountsFallsBackToList(t *testing.T) {
	t.Parallel()

	firstPage := &tg.MessagesMessageReactionsList{
		Reactions: []tg.MessagePeerReaction{
			{
				PeerID:   &tg.PeerUser{UserID: 1},
				Date:     1,
				Reaction: &tg.ReactionEmoji{Emoticon: "❤️"},
			},
		},
	}
	firstPage.SetNextOffset("page2")
	secondPage := &tg.MessagesMessageReactionsList{
		Reactions: []tg.MessagePeerReaction{
			{
				PeerID:   &tg.PeerUser{UserID: 2},
				Date:     2,
				Reaction: &tg.ReactionEmoji{Emoticon: "❤️"},
			},
			{
				PeerID:   &tg.PeerUser{UserID: 3},
				Date:     3,
				Reaction: &tg.ReactionEmoji{Emoticon: "👍"},
			},
		},
	}

	api := &stubGotdMessagesReactionsAPI{
		updates: &tg.Updates{
			Updates: []tg.UpdateClass{
				&tg.UpdateMessageReactions{
					MsgID: 901,
					Reactions: tg.MessageReactions{
						Results: []tg.ReactionCount{},
					},
				},
			},
		},
		lists: []*tg.MessagesMessageReactionsList{firstPage, secondPage},
	}
	resolver, err := NewGotdMessageReactionResolver(api)
	if err != nil {
		t.Fatalf("new resolver failed: %v", err)
	}

	got, err := resolver.ResolveMessageReactionCounts(
		context.Background(),
		&tg.InputPeerChannel{ChannelID: 1, AccessHash: 1},
		901,
	)
	if err != nil {
		t.Fatalf("resolve counts failed: %v", err)
	}
	if got["❤️"] != 2 {
		t.Fatalf("heart count = %d, want 2", got["❤️"])
	}
	if got["👍"] != 1 {
		t.Fatalf("thumbs up count = %d, want 1", got["👍"])
	}
	if api.listCalls != 2 {
		t.Fatalf("list calls = %d, want 2", api.listCalls)
	}
}

type stubGotdMessagesReactionsAPI struct {
	updates   tg.UpdatesClass
	lists     []*tg.MessagesMessageReactionsList
	listCalls int
}

func (s *stubGotdMessagesReactionsAPI) MessagesGetMessagesReactions(
	context.Context,
	*tg.MessagesGetMessagesReactionsRequest,
) (tg.UpdatesClass, error) {
	return s.updates, nil
}

func (s *stubGotdMessagesReactionsAPI) MessagesGetMessageReactionsList(
	context.Context,
	*tg.MessagesGetMessageReactionsListRequest,
) (*tg.MessagesMessageReactionsList, error) {
	if s.listCalls >= len(s.lists) {
		s.listCalls++
		return &tg.MessagesMessageReactionsList{}, nil
	}

	list := s.lists[s.listCalls]
	s.listCalls++

	return list, nil
}
