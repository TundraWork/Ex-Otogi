package kernel

import (
	"context"
	"sync"
	"testing"
	"time"

	"ex-otogi/pkg/otogi"
)

// TestEventBusPublishDeliversMatchingSubscriptions verifies filtered publish delivery.
func TestEventBusPublishDeliversMatchingSubscriptions(t *testing.T) {
	t.Parallel()

	bus := NewEventBus(8, 1, time.Second, nil)
	t.Cleanup(func() {
		_ = bus.Close(context.Background())
	})

	received := make(chan *otogi.Event, 1)
	_, err := bus.Subscribe(context.Background(), otogi.InterestSet{
		Kinds: []otogi.EventKind{otogi.EventKindMessageCreated},
	}, otogi.SubscriptionSpec{
		Name: "match",
	}, func(_ context.Context, event *otogi.Event) error {
		received <- event
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	if err := bus.Publish(context.Background(), newTestEvent("e1", otogi.EventKindMessageCreated)); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	select {
	case event := <-received:
		if event.ID != "e1" {
			t.Fatalf("event id = %s, want e1", event.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
	}
}

// TestEventBusBackpressurePolicies verifies queue behavior under each backpressure policy.
func TestEventBusBackpressurePolicies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		policy     otogi.BackpressurePolicy
		wantEvents []string
	}{
		{
			name:       "drop newest keeps queued oldest",
			policy:     otogi.BackpressureDropNewest,
			wantEvents: []string{"e1", "e2"},
		},
		{
			name:       "drop oldest keeps latest",
			policy:     otogi.BackpressureDropOldest,
			wantEvents: []string{"e1", "e3"},
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			bus := NewEventBus(1, 1, time.Second, nil)
			t.Cleanup(func() {
				_ = bus.Close(context.Background())
			})

			release := make(chan struct{})
			blocked := make(chan struct{}, 1)
			processed := make([]string, 0, 3)
			var first sync.Once
			var mu sync.Mutex

			_, err := bus.Subscribe(context.Background(), otogi.InterestSet{
				Kinds: []otogi.EventKind{otogi.EventKindMessageCreated},
			}, otogi.SubscriptionSpec{
				Name:         "policy",
				Workers:      1,
				Buffer:       1,
				Backpressure: testCase.policy,
			}, func(_ context.Context, event *otogi.Event) error {
				first.Do(func() {
					blocked <- struct{}{}
					<-release
				})
				mu.Lock()
				processed = append(processed, event.ID)
				mu.Unlock()
				return nil
			})
			if err != nil {
				t.Fatalf("subscribe failed: %v", err)
			}

			if err := bus.Publish(context.Background(), newTestEvent("e1", otogi.EventKindMessageCreated)); err != nil {
				t.Fatalf("publish e1 failed: %v", err)
			}
			select {
			case <-blocked:
			case <-time.After(time.Second):
				t.Fatal("handler did not block as expected")
			}
			if err := bus.Publish(context.Background(), newTestEvent("e2", otogi.EventKindMessageCreated)); err != nil {
				t.Fatalf("publish e2 failed: %v", err)
			}
			if err := bus.Publish(context.Background(), newTestEvent("e3", otogi.EventKindMessageCreated)); err != nil {
				t.Fatalf("publish e3 failed: %v", err)
			}

			close(release)
			eventually(t, 2*time.Second, func() bool {
				mu.Lock()
				defer mu.Unlock()
				return len(processed) == 2
			})

			mu.Lock()
			gotEvents := append([]string(nil), processed...)
			mu.Unlock()
			if gotEvents[0] != testCase.wantEvents[0] || gotEvents[1] != testCase.wantEvents[1] {
				t.Fatalf("processed = %v, want %v", gotEvents, testCase.wantEvents)
			}
		})
	}
}

// TestEventBusCloseRejectsNewPublish verifies publish rejection after bus closure.
func TestEventBusCloseRejectsNewPublish(t *testing.T) {
	t.Parallel()

	bus := NewEventBus(8, 1, time.Second, nil)
	if err := bus.Close(context.Background()); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	err := bus.Publish(context.Background(), newTestEvent("e1", otogi.EventKindMessageCreated))
	if err == nil {
		t.Fatal("expected publish on closed bus to fail")
	}
}

// TestEventBusPublishNilEventReturnsError verifies nil event publish safety.
func TestEventBusPublishNilEventReturnsError(t *testing.T) {
	t.Parallel()

	bus := NewEventBus(8, 1, time.Second, nil)
	t.Cleanup(func() {
		_ = bus.Close(context.Background())
	})

	if err := bus.Publish(context.Background(), nil); err == nil {
		t.Fatal("expected nil event publish to fail")
	}
}

func newTestEvent(id string, kind otogi.EventKind) *otogi.Event {
	event := &otogi.Event{
		ID:         id,
		Kind:       kind,
		OccurredAt: time.Now().UTC(),
		Platform:   otogi.PlatformTelegram,
		Conversation: otogi.Conversation{
			ID:   "chat-1",
			Type: otogi.ConversationTypeGroup,
		},
		Actor: otogi.Actor{ID: "user-1"},
	}

	switch kind {
	case otogi.EventKindMessageCreated:
		event.Message = &otogi.Message{ID: "msg-1", Text: "hello"}
	case otogi.EventKindMessageEdited:
		event.Mutation = &otogi.Mutation{Type: otogi.MutationTypeEdit, TargetMessageID: "msg-1"}
	case otogi.EventKindMessageRetracted:
		event.Mutation = &otogi.Mutation{Type: otogi.MutationTypeRetraction, TargetMessageID: "msg-1"}
	case otogi.EventKindReactionAdded:
		event.Reaction = &otogi.Reaction{MessageID: "msg-1", Emoji: "👍", Action: otogi.ReactionActionAdd}
	case otogi.EventKindReactionRemoved:
		event.Reaction = &otogi.Reaction{MessageID: "msg-1", Emoji: "👍", Action: otogi.ReactionActionRemove}
	case otogi.EventKindMemberJoined:
		event.StateChange = &otogi.StateChange{Type: otogi.StateChangeTypeMember, Member: &otogi.MemberChange{Action: kind, Member: otogi.Actor{ID: "user-1"}}}
	case otogi.EventKindMemberLeft:
		event.StateChange = &otogi.StateChange{Type: otogi.StateChangeTypeMember, Member: &otogi.MemberChange{Action: kind, Member: otogi.Actor{ID: "user-1"}}}
	case otogi.EventKindRoleUpdated:
		event.StateChange = &otogi.StateChange{Type: otogi.StateChangeTypeRole, Role: &otogi.RoleChange{MemberID: "user-1"}}
	case otogi.EventKindChatMigrated:
		event.StateChange = &otogi.StateChange{Type: otogi.StateChangeTypeMigration, Migration: &otogi.ChatMigration{FromConversationID: "old", ToConversationID: "new"}}
	}

	return event
}

func eventually(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatal("condition not met before timeout")
}
