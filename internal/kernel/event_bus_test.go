package kernel

import (
	"context"
	"errors"
	"strings"
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
		Kinds: []otogi.EventKind{otogi.EventKindArticleCreated},
	}, otogi.SubscriptionSpec{
		Name: "match",
	}, func(_ context.Context, event *otogi.Event) error {
		received <- event
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	if err := bus.Publish(context.Background(), newTestEvent("e1", otogi.EventKindArticleCreated)); err != nil {
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
				Kinds: []otogi.EventKind{otogi.EventKindArticleCreated},
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

			if err := bus.Publish(context.Background(), newTestEvent("e1", otogi.EventKindArticleCreated)); err != nil {
				t.Fatalf("publish e1 failed: %v", err)
			}
			select {
			case <-blocked:
			case <-time.After(time.Second):
				t.Fatal("handler did not block as expected")
			}
			if err := bus.Publish(context.Background(), newTestEvent("e2", otogi.EventKindArticleCreated)); err != nil {
				t.Fatalf("publish e2 failed: %v", err)
			}
			if err := bus.Publish(context.Background(), newTestEvent("e3", otogi.EventKindArticleCreated)); err != nil {
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

	err := bus.Publish(context.Background(), newTestEvent("e1", otogi.EventKindArticleCreated))
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

func TestEventBusHandlerTimeoutOverridePrecedence(t *testing.T) {
	t.Parallel()

	bus := NewEventBus(8, 1, 30*time.Millisecond, nil)
	t.Cleanup(func() {
		_ = bus.Close(context.Background())
	})

	type deadlineObservation struct {
		hasDeadline bool
		remaining   time.Duration
	}

	observed := make(chan deadlineObservation, 1)
	_, err := bus.Subscribe(context.Background(), otogi.InterestSet{
		Kinds: []otogi.EventKind{otogi.EventKindArticleCreated},
	}, otogi.SubscriptionSpec{
		Name:           "override-timeout",
		HandlerTimeout: 250 * time.Millisecond,
	}, func(ctx context.Context, _ *otogi.Event) error {
		deadline, hasDeadline := ctx.Deadline()
		remaining := time.Duration(0)
		if hasDeadline {
			remaining = time.Until(deadline)
		}
		observed <- deadlineObservation{
			hasDeadline: hasDeadline,
			remaining:   remaining,
		}

		return nil
	})
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	if err := bus.Publish(context.Background(), newTestEvent("e-timeout-override", otogi.EventKindArticleCreated)); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	select {
	case got := <-observed:
		if !got.hasDeadline {
			t.Fatal("handler context missing deadline")
		}
		if got.remaining < 150*time.Millisecond {
			t.Fatalf("handler deadline remaining = %s, want at least 150ms for override timeout", got.remaining)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler observation")
	}
}

func TestEventBusHandlerTimeoutDefaultsWhenUnset(t *testing.T) {
	t.Parallel()

	bus := NewEventBus(8, 1, 30*time.Millisecond, nil)
	t.Cleanup(func() {
		_ = bus.Close(context.Background())
	})

	type deadlineObservation struct {
		hasDeadline bool
		remaining   time.Duration
	}

	observed := make(chan deadlineObservation, 1)
	_, err := bus.Subscribe(context.Background(), otogi.InterestSet{
		Kinds: []otogi.EventKind{otogi.EventKindArticleCreated},
	}, otogi.SubscriptionSpec{
		Name: "default-timeout",
	}, func(ctx context.Context, _ *otogi.Event) error {
		deadline, hasDeadline := ctx.Deadline()
		remaining := time.Duration(0)
		if hasDeadline {
			remaining = time.Until(deadline)
		}
		observed <- deadlineObservation{
			hasDeadline: hasDeadline,
			remaining:   remaining,
		}

		return nil
	})
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	if err := bus.Publish(context.Background(), newTestEvent("e-timeout-default", otogi.EventKindArticleCreated)); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	select {
	case got := <-observed:
		if !got.hasDeadline {
			t.Fatal("handler context missing deadline")
		}
		if got.remaining <= 0 {
			t.Fatalf("handler deadline remaining = %s, want > 0", got.remaining)
		}
		if got.remaining > 120*time.Millisecond {
			t.Fatalf("handler deadline remaining = %s, want <= 120ms for default timeout", got.remaining)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler observation")
	}
}

func TestEventBusHandlerErrorIncludesTimeoutDiagnostics(t *testing.T) {
	t.Parallel()

	asyncErr := make(chan error, 1)
	bus := NewEventBus(8, 1, 30*time.Millisecond, func(_ context.Context, _ string, err error) {
		select {
		case asyncErr <- err:
		default:
		}
	})
	t.Cleanup(func() {
		_ = bus.Close(context.Background())
	})

	_, err := bus.Subscribe(context.Background(), otogi.InterestSet{
		Kinds: []otogi.EventKind{otogi.EventKindArticleCreated},
	}, otogi.SubscriptionSpec{
		Name: "handler-error-diagnostics",
	}, func(_ context.Context, _ *otogi.Event) error {
		return errors.New("boom")
	})
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	if err := bus.Publish(context.Background(), newTestEvent("e-diagnostics", otogi.EventKindArticleCreated)); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	select {
	case err := <-asyncErr:
		if err == nil {
			t.Fatal("async error is nil")
		}
		text := err.Error()
		if !strings.Contains(text, "handler_timeout=30ms") {
			t.Fatalf("error = %q, want handler_timeout diagnostics", text)
		}
		if !strings.Contains(text, "handler_ctx_has_deadline=true") {
			t.Fatalf("error = %q, want handler_ctx_has_deadline diagnostics", text)
		}
		if !strings.Contains(text, "handler_ctx_deadline=") {
			t.Fatalf("error = %q, want handler_ctx_deadline diagnostics", text)
		}
		if !strings.Contains(text, "handler_ctx_deadline_remaining=") {
			t.Fatalf("error = %q, want handler_ctx_deadline_remaining diagnostics", text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for async error")
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
	case otogi.EventKindArticleCreated:
		event.Article = &otogi.Article{ID: "msg-1", Text: "hello"}
	case otogi.EventKindArticleEdited:
		event.Mutation = &otogi.ArticleMutation{Type: otogi.MutationTypeEdit, TargetArticleID: "msg-1"}
	case otogi.EventKindArticleRetracted:
		event.Mutation = &otogi.ArticleMutation{Type: otogi.MutationTypeRetraction, TargetArticleID: "msg-1"}
	case otogi.EventKindArticleReactionAdded:
		event.Reaction = &otogi.Reaction{ArticleID: "msg-1", Emoji: "👍", Action: otogi.ReactionActionAdd}
	case otogi.EventKindArticleReactionRemoved:
		event.Reaction = &otogi.Reaction{ArticleID: "msg-1", Emoji: "👍", Action: otogi.ReactionActionRemove}
	case otogi.EventKindCommandReceived:
		event.Article = &otogi.Article{ID: "msg-1", Text: "/raw"}
		event.Command = &otogi.CommandInvocation{
			Name:            "raw",
			SourceEventID:   "source-e1",
			SourceEventKind: otogi.EventKindArticleCreated,
			RawInput:        "/raw",
		}
	case otogi.EventKindSystemCommandReceived:
		event.Article = &otogi.Article{ID: "msg-1", Text: "~raw"}
		event.Command = &otogi.CommandInvocation{
			Name:            "raw",
			SourceEventID:   "source-e1",
			SourceEventKind: otogi.EventKindArticleCreated,
			RawInput:        "~raw",
		}
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
