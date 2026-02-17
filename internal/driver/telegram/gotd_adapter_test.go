package telegram

import (
	"context"
	"testing"
	"time"

	"github.com/gotd/td/tg"
)

func TestGotdUpdateChannelUpdatesNilContext(t *testing.T) {
	t.Parallel()

	stream, err := NewGotdUpdateChannel(8)
	if err != nil {
		t.Fatalf("new gotd update channel failed: %v", err)
	}

	var nilCtx context.Context
	if _, err := stream.Updates(nilCtx); err == nil {
		t.Fatal("expected nil context error")
	}
}

func TestGotdUpdateChannelHandleFlattensBatch(t *testing.T) {
	t.Parallel()

	stream, err := NewGotdUpdateChannel(16)
	if err != nil {
		t.Fatalf("new gotd update channel failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	updates, err := stream.Updates(ctx)
	if err != nil {
		t.Fatalf("open updates stream failed: %v", err)
	}

	batch := &tg.Updates{
		Date: 1_700_000_010,
		Updates: []tg.UpdateClass{
			&tg.UpdateDeleteChannelMessages{
				ChannelID: 500,
				Messages:  []int{101, 102},
			},
			&tg.UpdateBotMessageReaction{
				Peer:  &tg.PeerChannel{ChannelID: 500},
				MsgID: 200,
				Actor: &tg.PeerUser{UserID: 42},
				OldReactions: []tg.ReactionClass{
					&tg.ReactionEmoji{Emoticon: "😀"},
				},
				NewReactions: []tg.ReactionClass{
					&tg.ReactionEmoji{Emoticon: "😀"},
					&tg.ReactionEmoji{Emoticon: "👍"},
				},
			},
		},
	}

	if err := stream.Handle(ctx, batch); err != nil {
		t.Fatalf("handle failed: %v", err)
	}

	collected := make([]gotdUpdateEnvelope, 0, 3)
	for i := 0; i < 3; i++ {
		select {
		case item := <-updates:
			envelope, ok := item.(gotdUpdateEnvelope)
			if !ok {
				t.Fatalf("item type = %T, want gotdUpdateEnvelope", item)
			}
			collected = append(collected, envelope)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out receiving flattened updates")
		}
	}

	if len(collected) != 3 {
		t.Fatalf("collected = %d, want 3", len(collected))
	}

	deleteCount := 0
	reactionCount := 0
	for _, envelope := range collected {
		if envelope.reaction != nil {
			reactionCount++
			if envelope.reaction.action != UpdateTypeReactionAdd {
				t.Fatalf("reaction action = %s, want %s", envelope.reaction.action, UpdateTypeReactionAdd)
			}
			if envelope.reaction.emoji != "👍" {
				t.Fatalf("reaction emoji = %s, want 👍", envelope.reaction.emoji)
			}
			continue
		}

		typed, ok := envelope.update.(*tg.UpdateDeleteChannelMessages)
		if !ok {
			t.Fatalf("update type = %T, want *tg.UpdateDeleteChannelMessages", envelope.update)
		}
		if len(typed.Messages) != 1 {
			t.Fatalf("delete messages len = %d, want 1", len(typed.Messages))
		}
		deleteCount++
	}

	if deleteCount != 2 {
		t.Fatalf("delete count = %d, want 2", deleteCount)
	}
	if reactionCount != 1 {
		t.Fatalf("reaction count = %d, want 1", reactionCount)
	}
}

func TestGotdUpdateChannelHandleBeforeUpdatesCall(t *testing.T) {
	t.Parallel()

	stream, err := NewGotdUpdateChannel(8)
	if err != nil {
		t.Fatalf("new gotd update channel failed: %v", err)
	}

	if err := stream.Handle(context.Background(), &tg.Updates{
		Updates: []tg.UpdateClass{
			&tg.UpdateDeleteMessages{Messages: []int{1}},
		},
	}); err != nil {
		t.Fatalf("handle failed: %v", err)
	}

	updates, err := stream.Updates(context.Background())
	if err != nil {
		t.Fatalf("open updates stream failed: %v", err)
	}

	select {
	case item := <-updates:
		envelope, ok := item.(gotdUpdateEnvelope)
		if !ok {
			t.Fatalf("item type = %T, want gotdUpdateEnvelope", item)
		}
		if _, ok := envelope.update.(*tg.UpdateDeleteMessages); !ok {
			t.Fatalf("update type = %T, want *tg.UpdateDeleteMessages", envelope.update)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out receiving buffered update")
	}
}

func TestGotdUpdateChannelHandleFlattensMessageReactionsUpdate(t *testing.T) {
	t.Parallel()

	stream, err := NewGotdUpdateChannel(8)
	if err != nil {
		t.Fatalf("new gotd update channel failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	updates, err := stream.Updates(ctx)
	if err != nil {
		t.Fatalf("open updates stream failed: %v", err)
	}

	reactions := tg.MessageReactions{
		Results: []tg.ReactionCount{},
	}
	reactions.SetRecentReactions([]tg.MessagePeerReaction{
		{
			My:       true,
			PeerID:   &tg.PeerUser{UserID: 42},
			Reaction: &tg.ReactionEmoji{Emoticon: "❤️"},
			Date:     1_700_000_100,
		},
	})

	batch := &tg.Updates{
		Date: 1_700_000_100,
		Updates: []tg.UpdateClass{
			&tg.UpdateMessageReactions{
				Peer:      &tg.PeerChannel{ChannelID: 500},
				MsgID:     321,
				Reactions: reactions,
			},
		},
	}
	if err := stream.Handle(ctx, batch); err != nil {
		t.Fatalf("handle failed: %v", err)
	}

	select {
	case item := <-updates:
		envelope, ok := item.(gotdUpdateEnvelope)
		if !ok {
			t.Fatalf("item type = %T, want gotdUpdateEnvelope", item)
		}
		if envelope.reaction != nil {
			t.Fatalf("reaction = %+v, want nil passthrough envelope", envelope.reaction)
		}
		typed, ok := envelope.update.(*tg.UpdateMessageReactions)
		if !ok {
			t.Fatalf("update type = %T, want *tg.UpdateMessageReactions", envelope.update)
		}
		if typed.MsgID != 321 {
			t.Fatalf("message id = %d, want 321", typed.MsgID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out receiving flattened update")
	}
}

func TestGotdUpdateChannelHandleFlattensMessageReactionsChosenFallback(t *testing.T) {
	t.Parallel()

	stream, err := NewGotdUpdateChannel(8)
	if err != nil {
		t.Fatalf("new gotd update channel failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	updates, err := stream.Updates(ctx)
	if err != nil {
		t.Fatalf("open updates stream failed: %v", err)
	}

	chosen := tg.ReactionCount{
		Reaction: &tg.ReactionEmoji{Emoticon: "❤️"},
		Count:    1,
	}
	chosen.SetChosenOrder(2)

	batch := &tg.Updates{
		Date: 1_700_000_101,
		Updates: []tg.UpdateClass{
			&tg.UpdateMessageReactions{
				Peer:  &tg.PeerChannel{ChannelID: 500},
				MsgID: 322,
				Reactions: tg.MessageReactions{
					Results: []tg.ReactionCount{chosen},
				},
			},
		},
	}
	if err := stream.Handle(ctx, batch); err != nil {
		t.Fatalf("handle failed: %v", err)
	}

	select {
	case item := <-updates:
		envelope, ok := item.(gotdUpdateEnvelope)
		if !ok {
			t.Fatalf("item type = %T, want gotdUpdateEnvelope", item)
		}
		if envelope.reaction != nil {
			t.Fatalf("reaction = %+v, want nil passthrough envelope", envelope.reaction)
		}
		typed, ok := envelope.update.(*tg.UpdateMessageReactions)
		if !ok {
			t.Fatalf("update type = %T, want *tg.UpdateMessageReactions", envelope.update)
		}
		if typed.MsgID != 322 {
			t.Fatalf("message id = %d, want 322", typed.MsgID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out receiving flattened update")
	}
}

func TestGotdUpdateChannelHandleFlattensMessageReactionsWithoutSignal(t *testing.T) {
	t.Parallel()

	stream, err := NewGotdUpdateChannel(8)
	if err != nil {
		t.Fatalf("new gotd update channel failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	updates, err := stream.Updates(ctx)
	if err != nil {
		t.Fatalf("open updates stream failed: %v", err)
	}

	batch := &tg.Updates{
		Date: 1_700_000_102,
		Updates: []tg.UpdateClass{
			&tg.UpdateMessageReactions{
				Peer:  &tg.PeerChannel{ChannelID: 500},
				MsgID: 323,
				Reactions: tg.MessageReactions{
					Results: []tg.ReactionCount{},
				},
			},
		},
	}
	if err := stream.Handle(ctx, batch); err != nil {
		t.Fatalf("handle failed: %v", err)
	}

	select {
	case item := <-updates:
		envelope, ok := item.(gotdUpdateEnvelope)
		if !ok {
			t.Fatalf("item type = %T, want gotdUpdateEnvelope", item)
		}
		if envelope.reaction != nil {
			t.Fatalf("reaction = %+v, want nil passthrough envelope", envelope.reaction)
		}
		typed, ok := envelope.update.(*tg.UpdateMessageReactions)
		if !ok {
			t.Fatalf("update type = %T, want *tg.UpdateMessageReactions", envelope.update)
		}
		if typed.MsgID != 323 {
			t.Fatalf("message id = %d, want 323", typed.MsgID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out receiving flattened update")
	}
}
