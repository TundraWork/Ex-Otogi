package telegram

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

type dispatchStore struct {
	mu       sync.Mutex
	rows     []telegramstore.InboxRow
	claimed  []string
	released []releaseRecord
	complete []string
	failed   []string
}

type releaseRecord struct {
	id     string
	next   time.Time
	reason string
}

func (s *dispatchStore) ClaimNextAfter(_ context.Context, owner string, now time.Time, lease time.Duration, _ string) (telegramstore.InboxRow, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.rows) == 0 {
		return telegramstore.InboxRow{}, false, nil
	}
	row := s.rows[0]
	s.rows = s.rows[1:]
	row.Owner = owner
	row.Status = "claimed"
	row.Attempts++
	until := now.Add(lease)
	row.LeaseUntil = &until
	s.claimed = append(s.claimed, row.ID)
	return row, true, nil
}
func (s *dispatchStore) ReleaseClaim(_ context.Context, id, _ string, next time.Time, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.released = append(s.released, releaseRecord{id: id, next: next, reason: reason})
	return nil
}
func (s *dispatchStore) CompleteClaim(_ context.Context, id, _ string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.complete = append(s.complete, id)
	return nil
}
func (s *dispatchStore) FailClaim(_ context.Context, id, _, _ string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed = append(s.failed, id)
	return nil
}

type dispatchAdmitter struct {
	mu             sync.Mutex
	subscribeErr   error
	submitErr      error
	submitted      []otogi.EventID
	subscribed     []otogi.EventID
	completion     chan otogi.EventID
	autoComplete   bool
	onSubmit       func(otogi.EventID, otogi.EventInput)
}

func (a *dispatchAdmitter) SubscribeCompletion(_ context.Context, id otogi.EventID) (<-chan otogi.EventID, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.subscribed = append(a.subscribed, id)
	if a.subscribeErr != nil {
		return nil, a.subscribeErr
	}
	if a.completion == nil {
		a.completion = make(chan otogi.EventID, 1)
	}
	return a.completion, nil
}
func (a *dispatchAdmitter) SubmitTrusted(_ context.Context, id otogi.EventID, input otogi.EventInput) error {
	a.mu.Lock()
	a.submitted = append(a.submitted, id)
	err := a.submitErr
	hook := a.onSubmit
	completion := a.completion
	auto := a.autoComplete
	a.mu.Unlock()
	if hook != nil {
		hook(id, input)
	}
	if err == nil && auto {
		completion <- id
	}
	return err
}

func TestDispatcherSubscribesBeforeAdmissionAndCompletesAcknowledgedRichEvent(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	row := dispatchRow(t, "018f47a6-f5f7-7cac-9000-111111111111", "chat:1", []byte("rich"))
	store := &dispatchStore{rows: []telegramstore.InboxRow{row}}
	admitter := &dispatchAdmitter{autoComplete: true}
	var decoded bool
	decoder := func(payload []byte) (otogi.EventInput, error) {
		if string(payload) != "rich" {
			t.Fatalf("payload=%q", payload)
		}
		decoded = true
		return dispatchInput(t, "chat:1"), nil
	}
	admitter.onSubmit = func(id otogi.EventID, input otogi.EventInput) {
		if !decoded || input.Action() != otogi.ActionMessageEdited {
			t.Fatal("event was not richly decoded before admission")
		}
		admitter.mu.Lock()
		defer admitter.mu.Unlock()
		if len(admitter.subscribed) != 1 || admitter.subscribed[0] != id {
			t.Fatal("admission happened before completion subscription")
		}
	}
	d := NewInboxDispatcher(store, admitter, decoder, dispatchConfig(now))
	worked, err := d.dispatchOne(context.Background())
	if err != nil || !worked {
		t.Fatalf("dispatchOne() = %v, %v", worked, err)
	}
	if len(store.complete) != 1 || store.complete[0] != row.ID || len(store.released) != 0 || len(store.failed) != 0 {
		t.Fatalf("complete=%v released=%v failed=%v", store.complete, store.released, store.failed)
	}
}

func TestDispatcherDerivesStableEventIDAndReplaysAfterLostAcknowledgement(t *testing.T) {
	inboxID := "018f47a6-f5f7-7cac-9000-222222222222"
	first := deterministicInboxEventID(inboxID)
	if first != deterministicInboxEventID(inboxID) || first == deterministicInboxEventID(inboxID+"x") {
		t.Fatal("event ID derivation is not stable and row-specific")
	}
	now := time.Unix(200, 0).UTC()
	store := &dispatchStore{rows: []telegramstore.InboxRow{dispatchRow(t, inboxID, "chat:1", []byte("event"))}}
	admitter := &dispatchAdmitter{completion: make(chan otogi.EventID)}
	d := NewInboxDispatcher(store, admitter, func([]byte) (otogi.EventInput, error) { return dispatchInput(t, "chat:1"), nil }, dispatchConfig(now))
	ctx, cancel := context.WithCancel(context.Background())
	admitter.onSubmit = func(otogi.EventID, otogi.EventInput) { cancel() }
	if worked, err := d.dispatchOne(ctx); !worked || err != nil {
		t.Fatalf("lost ack dispatch = %v, %v", worked, err)
	}
	if len(store.complete) != 0 || len(store.released) != 1 {
		t.Fatalf("complete=%v released=%v", store.complete, store.released)
	}
	store.rows = []telegramstore.InboxRow{dispatchRow(t, inboxID, "chat:1", []byte("event"))}
	admitter.onSubmit = nil
	admitter.autoComplete = true
	admitter.completion = nil
	if worked, err := d.dispatchOne(context.Background()); err != nil || !worked {
		t.Fatalf("replay = %v, %v", worked, err)
	}
	if len(admitter.submitted) != 2 || admitter.submitted[0] != first || admitter.submitted[1] != first {
		t.Fatalf("submitted IDs=%v want repeated %s", admitter.submitted, first)
	}
}

func TestDispatcherRetriesCapacityWithBoundedExponentialSchedule(t *testing.T) {
	now := time.Unix(300, 0).UTC()
	cfg := dispatchConfig(now)
	cfg.RetryBase = 2 * time.Second
	cfg.RetryMax = 5 * time.Second
	for _, attempts := range []int{1, 2, 20} {
		store := &dispatchStore{rows: []telegramstore.InboxRow{dispatchRow(t, "row", "a", []byte("event"))}}
		store.rows[0].Attempts = attempts - 1
		admitter := &dispatchAdmitter{subscribeErr: otogi.ErrCapacity}
		d := NewInboxDispatcher(store, admitter, func([]byte) (otogi.EventInput, error) { return dispatchInput(t, "a"), nil }, cfg)
		if worked, err := d.dispatchOne(context.Background()); err != nil || !worked {
			t.Fatalf("attempt %d = %v, %v", attempts, worked, err)
		}
		want := now.Add(min(5*time.Second, 2*time.Second*time.Duration(1<<min(attempts-1, 2))))
		if len(store.released) != 1 || !store.released[0].next.Equal(want) || store.released[0].reason != "kernel capacity" {
			t.Fatalf("attempt %d release=%+v want=%s", attempts, store.released, want)
		}
	}
}

func TestDispatcherPermanentlyFailsInvalidPayloadWithoutAdmission(t *testing.T) {
	now := time.Unix(400, 0).UTC()
	store := &dispatchStore{rows: []telegramstore.InboxRow{dispatchRow(t, "bad", "a", []byte("invalid"))}}
	admitter := &dispatchAdmitter{}
	d := NewInboxDispatcher(store, admitter, func([]byte) (otogi.EventInput, error) { return otogi.EventInput{}, otogi.ErrInvalid }, dispatchConfig(now))
	if worked, err := d.dispatchOne(context.Background()); err != nil || !worked {
		t.Fatalf("dispatch = %v, %v", worked, err)
	}
	if len(store.failed) != 1 || len(admitter.subscribed) != 0 || len(admitter.submitted) != 0 {
		t.Fatalf("failed=%v subscribed=%v submitted=%v", store.failed, admitter.subscribed, admitter.submitted)
	}
}

func TestDispatcherRunIsSingleOwnerAndStopsOnCancellation(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	cfg := dispatchConfig(now)
	cfg.PollInterval = time.Hour
	d := NewInboxDispatcher(&dispatchStore{}, &dispatchAdmitter{}, func([]byte) (otogi.EventInput, error) { return dispatchInput(t, "a"), nil }, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	for i := 0; i < 100 && !d.running(); i++ {
		time.Sleep(time.Millisecond)
	}
	if err := d.Run(context.Background()); !errors.Is(err, otogi.ErrInvalidState) {
		t.Fatalf("concurrent Run error=%v", err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run leaked after cancellation")
	}
}

func dispatchConfig(now time.Time) InboxDispatcherConfig {
	return InboxDispatcherConfig{Owner: "dispatcher", Lease: 12 * time.Second, AttemptTimeout: 10 * time.Second, CleanupTimeout: time.Second, PollInterval: time.Millisecond, RetryBase: time.Second, RetryMax: time.Minute, Now: func() time.Time { return now }}
}
func dispatchRow(t *testing.T, id, conversation string, payload []byte) telegramstore.InboxRow {
	t.Helper()
	return telegramstore.InboxRow{InboxInput: telegramstore.InboxInput{ID: id, SourceKey: id, Conversation: conversation, Sequence: 1, Payload: payload, AvailableAt: time.Unix(1, 0).UTC()}}
}
func dispatchInput(t *testing.T, conversation string) otogi.EventInput {
	t.Helper()
	platform, _ := otogi.NewPlatformID("telegram")
	account, _ := otogi.NewAccountID("account")
	tenant, _ := otogi.NewTenantID("tenant")
	conversationID, _ := otogi.NewConversationID(conversation)
	source, _ := otogi.NewSourceEventID("source")
	input, err := otogi.NewEventInput(otogi.EventInputParams{Platform: platform, Account: account, Tenant: tenant, Conversation: conversationID, SourceEventID: source, Action: otogi.ActionMessageEdited, Direction: otogi.DirectionIncoming, OccurredAt: time.Unix(1, 0).UTC(), IngestedAt: time.Unix(2, 0).UTC(), Completeness: otogi.CompletenessPartial})
	if err != nil {
		t.Fatal(err)
	}
	return input
}
