package updates

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
	"github.com/gotd/td/tg"
)

func TestDownstreamRoutesResolvedChannelDeletion(t *testing.T) {
	store := &captureStore{}
	var routed []otogi.EventInput
	handler := NewDownstream(store, testMapper(t), func(input otogi.EventInput) ([]byte, error) {
		routed = append(routed, input)
		return []byte(input.Action().String()), nil
	}, NewHealth())
	ctx := context.WithValue(context.Background(), journalContextKey{}, int64(9))
	batch := &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateDeleteChannelMessages{ChannelID: 77, Messages: []int{5}, Pts: 8, PtsCount: 1}}}
	if err := handler.Handle(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if len(store.captures) != 1 || store.captures[0].Inbox == nil || len(routed) != 1 {
		t.Fatalf("captures=%+v routed=%d", store.captures, len(routed))
	}
	if routed[0].Action() != otogi.ActionMessageDeleted || routed[0].Conversation().String() != "channel:77" || routed[0].Completeness() != otogi.CompletenessUnavailable {
		t.Fatalf("deletion event = action=%s conversation=%s completeness=%s", routed[0].Action(), routed[0].Conversation(), routed[0].Completeness())
	}
}

func TestDownstreamResolvesCommonDeletionThroughLocator(t *testing.T) {
	store := &captureStore{locators: []telegramstore.Locator{{Namespace: "common", MessageID: 5, Conversation: "user:7", Payload: []byte("locator")}}}
	var routed otogi.EventInput
	handler := NewDownstream(store, testMapper(t), func(input otogi.EventInput) ([]byte, error) {
		routed = input
		return []byte("deleted"), nil
	}, NewHealth())
	ctx := context.WithValue(context.Background(), journalContextKey{}, int64(9))
	batch := &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateDeleteMessages{Messages: []int{5}, Pts: 8, PtsCount: 1}}}
	if err := handler.Handle(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if len(store.captures) != 1 || store.captures[0].Inbox == nil || routed.Conversation().String() != "user:7" || routed.Action() != otogi.ActionMessageDeleted {
		t.Fatalf("capture=%+v event=%s/%s", store.captures, routed.Conversation(), routed.Action())
	}
}

func TestDownstreamStoresStructuredUnresolvedDeletionTombstone(t *testing.T) {
	store := &captureStore{}
	handler := NewDownstream(store, testMapper(t), func(otogi.EventInput) ([]byte, error) { return nil, nil }, NewHealth())
	ctx := context.WithValue(context.Background(), journalContextKey{}, int64(9))
	batch := &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateDeleteMessages{Messages: []int{5}, Pts: 8, PtsCount: 1}}}
	if err := handler.Handle(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if len(store.captures) != 1 || store.captures[0].Quarantine == nil || store.captures[0].Quarantine.Kind != "telegram.delete.unresolved" {
		t.Fatalf("captures=%+v", store.captures)
	}
	var tombstone struct {
		Namespace string `json:"namespace"`
		MessageID int    `json:"message_id"`
	}
	if err := json.Unmarshal(store.captures[0].Quarantine.Payload, &tombstone); err != nil || tombstone.Namespace != "common" || tombstone.MessageID != 5 {
		t.Fatalf("tombstone=%+v err=%v", tombstone, err)
	}
}

func TestDownstreamRejectsOversizedDeletionBeforeCapture(t *testing.T) {
	store := &captureStore{}
	health := NewHealth()
	handler := NewDownstream(store, testMapper(t), func(otogi.EventInput) ([]byte, error) { return []byte("deleted"), nil }, health)
	ctx := context.WithValue(context.Background(), journalContextKey{}, int64(9))
	batch := &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateDeleteChannelMessages{ChannelID: 77, Messages: make([]int, telegramstore.MaxCaptureRows+1), Pts: 8, PtsCount: 1}}}
	for i := range batch.Updates[0].(*tg.UpdateDeleteChannelMessages).Messages {
		batch.Updates[0].(*tg.UpdateDeleteChannelMessages).Messages[i] = i + 1
	}
	if err := handler.Handle(ctx, batch); err == nil {
		t.Fatal("oversized deletion accepted")
	}
	if len(store.captures) != 0 {
		t.Fatalf("oversized deletion reached capture transaction: %d rows", len(store.captures))
	}
	if ready, _ := health.Ready(); ready {
		t.Fatal("oversized deletion did not request resynchronization")
	}
}

func TestDownstreamBoundsLocatorExpandedCommonDeletion(t *testing.T) {
	store := &captureStore{locators: make([]telegramstore.Locator, telegramstore.MaxCaptureRows+1)}
	for i := range store.locators {
		store.locators[i] = telegramstore.Locator{Namespace: "common", MessageID: 5, Conversation: fmt.Sprintf("user:%d", i+1)}
	}
	health := NewHealth()
	handler := NewDownstream(store, testMapper(t), func(otogi.EventInput) ([]byte, error) { return []byte("deleted"), nil }, health)
	ctx := context.WithValue(context.Background(), journalContextKey{}, int64(9))
	batch := &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateDeleteMessages{Messages: []int{5}, Pts: 8, PtsCount: 1}}}
	if err := handler.Handle(ctx, batch); err == nil {
		t.Fatal("locator-expanded deletion accepted")
	}
	if len(store.captures) != 0 {
		t.Fatalf("expanded deletion reached capture: %d", len(store.captures))
	}
	if ready, _ := health.Ready(); ready {
		t.Fatal("expanded deletion did not request resynchronization")
	}
}
