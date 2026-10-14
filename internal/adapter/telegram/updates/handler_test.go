package updates

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/mapper"
	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

type captureStore struct {
	captures []telegramstore.Capture
	locators []telegramstore.Locator
	err      error
	gaps     []string
	next     int64
}

func (s *captureStore) AppendJournal(context.Context, []byte, time.Time) (int64, error) {
	s.next++
	return s.next, nil
}
func (s *captureStore) CaptureBatch(_ context.Context, captures []telegramstore.Capture) (bool, error) {
	s.captures = append(s.captures, captures...)
	return true, s.err
}
func (s *captureStore) RecordGap(_ context.Context, box string, _ int64, reason string) error {
	s.gaps = append(s.gaps, box+":"+reason)
	return nil
}
func (s *captureStore) FindLocators(_ context.Context, namespace string, messageID int64) ([]telegramstore.Locator, error) {
	var found []telegramstore.Locator
	for _, locator := range s.locators {
		if locator.Namespace == namespace && locator.MessageID == messageID {
			found = append(found, locator)
		}
	}
	return found, nil
}

func testMapper(t *testing.T) *mapper.Mapper {
	t.Helper()
	return mapper.New(mapper.Config{Platform: "telegram", Account: "deployment:42", AuthorizedUserID: 42, IngestedAt: func() time.Time { return time.Unix(20, 0).UTC() }})
}

func mappedBatch() *tg.Updates {
	return &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateNewMessage{Message: &tg.Message{ID: 5, PeerID: &tg.PeerUser{UserID: 7}, FromID: &tg.PeerUser{UserID: 7}, Date: 10, Message: "hello"}, Pts: 4, PtsCount: 1}}, Users: []tg.UserClass{&tg.User{ID: 7, AccessHash: 8, FirstName: "A"}}, Date: 10, Seq: -1}
}

func TestDownstreamAtomicallyCapturesMappedEventPeerLocatorAndConsistency(t *testing.T) {
	store := &captureStore{}
	health := NewHealth()
	handler := NewDownstream(store, testMapper(t), func(input otogi.Event) ([]byte, error) { return []byte(input.Action), nil }, health)
	ctx := context.WithValue(context.Background(), journalContextKey{}, int64(9))
	if err := handler.Handle(ctx, mappedBatch()); err != nil {
		t.Fatal(err)
	}
	if len(store.captures) != 1 {
		t.Fatalf("captures = %d", len(store.captures))
	}
	capture := store.captures[0]
	if capture.JournalID != 9 || capture.Inbox == nil || capture.Inbox.SourceKey == "" || capture.Inbox.Conversation != "user:7" || string(capture.Inbox.Payload) != "message.created" {
		t.Fatalf("capture = %+v", capture)
	}
	if capture.Box != "common" || capture.Position != 4 || len(capture.Locators) != 1 || len(capture.Peers) != 1 {
		t.Fatalf("capture metadata = %+v", capture)
	}
	if ready, reasons := health.Ready(); !ready || len(reasons) != 0 {
		t.Fatalf("health = %v %+v", ready, reasons)
	}
}

func TestDownstreamStorageFailureRetainsJournalAndDegrades(t *testing.T) {
	store := &captureStore{err: errors.New("sqlite full")}
	health := NewHealth()
	handler := NewDownstream(store, testMapper(t), func(otogi.Event) ([]byte, error) { return []byte("payload"), nil }, health)
	ctx := context.WithValue(context.Background(), journalContextKey{}, int64(9))
	if err := handler.Handle(ctx, mappedBatch()); err == nil {
		t.Fatal("expected capture failure")
	}
	if len(store.gaps) != 1 {
		t.Fatalf("gaps = %v", store.gaps)
	}
	if ready, _ := health.Ready(); ready {
		t.Fatal("storage failure did not degrade readiness")
	}
}

func TestDownstreamRejectsUpdateQueuedBehindFailedBoundary(t *testing.T) {
	store := &captureStore{}
	health := NewHealth()
	if err := health.arm(); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("first capture failed")
	health.degrade(DegradedReason{Box: "common", Position: 4, Reason: "capture normalized update", Cause: cause})
	handler := NewDownstream(store, testMapper(t), func(otogi.Event) ([]byte, error) { return []byte("payload"), nil }, health)
	if err := handler.Handle(context.Background(), mappedBatch()); !errors.Is(err, cause) {
		t.Fatalf("error=%v", err)
	}
	if store.next != 0 || len(store.captures) != 0 {
		t.Fatalf("journal=%d captures=%d", store.next, len(store.captures))
	}
}

func TestDownstreamDoesNotCapturePastConcurrentFailureBoundary(t *testing.T) {
	for _, armed := range []bool{false, true} {
		t.Run(fmt.Sprintf("armed=%v", armed), func(t *testing.T) {
			captureEntered := make(chan struct{})
			failCapture := make(chan struct{})
			store := &blockingFailureStore{
				captureStore:   captureStore{err: errors.New("sqlite full")},
				captureEntered: captureEntered,
				failCapture:    failCapture,
			}
			health := NewHealth()
			if armed {
				if err := health.arm(); err != nil {
					t.Fatal(err)
				}
			}
			secondMapped := make(chan struct{})
			continueSecond := make(chan struct{})
			var encodeCalls atomic.Int64
			handler := NewDownstream(store, testMapper(t), func(input otogi.Event) ([]byte, error) {
				if encodeCalls.Add(1) == 2 {
					close(secondMapped)
					<-continueSecond
				}
				return []byte(input.Action), nil
			}, health)

			firstDone := make(chan error, 1)
			go func() { firstDone <- handler.Handle(context.Background(), mappedMessageBatch("first", 5)) }()
			<-captureEntered
			secondDone := make(chan error, 1)
			go func() { secondDone <- handler.Handle(context.Background(), mappedMessageBatch("second", 6)) }()
			<-secondMapped

			close(failCapture)
			firstErr := <-firstDone
			if !errors.Is(firstErr, store.err) {
				t.Fatalf("first error = %v", firstErr)
			}
			close(continueSecond)
			secondErr := <-secondDone
			if !errors.Is(secondErr, store.err) {
				t.Fatalf("second error = %v", secondErr)
			}
			if calls := store.captureCalls.Load(); calls != 1 {
				t.Fatalf("CaptureBatch calls = %d, want 1", calls)
			}
			if len(store.gaps) != 1 {
				t.Fatalf("gaps = %v", store.gaps)
			}
			if ready, reasons := health.Ready(); ready || len(reasons) != 1 {
				t.Fatalf("health ready=%v reasons=%+v", ready, reasons)
			}
		})
	}
}

func TestDownstreamDoesNotCaptureAfterConcurrentGeneralDegradation(t *testing.T) {
	store := &captureStore{}
	health := NewHealth()
	if err := health.arm(); err != nil {
		t.Fatal(err)
	}
	mapped := make(chan struct{})
	continueCapture := make(chan struct{})
	handler := NewDownstream(store, testMapper(t), func(input otogi.Event) ([]byte, error) {
		close(mapped)
		<-continueCapture
		return []byte(input.Action), nil
	}, health)

	done := make(chan error, 1)
	go func() { done <- handler.Handle(context.Background(), mappedBatch()) }()
	<-mapped
	cause := errors.New("state storage failed")
	health.degrade(DegradedReason{Box: "common", Position: 4, Reason: "set state", Cause: cause})
	close(continueCapture)
	if err := <-done; !errors.Is(err, cause) {
		t.Fatalf("error = %v", err)
	}
	if len(store.captures) != 0 {
		t.Fatalf("CaptureBatch calls = %d, want 0", len(store.captures))
	}
}

type blockingFailureStore struct {
	captureStore
	captureCalls   atomic.Int64
	captureEntered chan struct{}
	failCapture    chan struct{}
}

func (s *blockingFailureStore) CaptureBatch(context.Context, []telegramstore.Capture) (bool, error) {
	if s.captureCalls.Add(1) == 1 {
		close(s.captureEntered)
		<-s.failCapture
	}
	return false, s.err
}

func mappedMessageBatch(message string, id int) *tg.Updates {
	return &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateNewMessage{Message: &tg.Message{ID: id, PeerID: &tg.PeerUser{UserID: 7}, FromID: &tg.PeerUser{UserID: 7}, Date: 10, Message: message}, Pts: id, PtsCount: 1}}, Users: []tg.UserClass{&tg.User{ID: 7, AccessHash: 8, FirstName: "A"}}, Date: 10, Seq: -1}
}

func TestDownstreamJournalsManagerDifferenceOutputAndExcludesNegativeSequence(t *testing.T) {
	store := &captureStore{}
	handler := NewDownstream(store, testMapper(t), func(otogi.Event) ([]byte, error) { return []byte("payload"), nil }, NewHealth())
	if err := handler.Handle(context.Background(), mappedBatch()); err != nil {
		t.Fatal(err)
	}
	if store.next != 1 || store.captures[0].JournalID != 1 {
		t.Fatalf("difference journal = %d capture = %+v", store.next, store.captures)
	}
	if store.captures[0].Inbox.Sequence < 0 {
		t.Fatalf("negative sequence persisted: %d", store.captures[0].Inbox.Sequence)
	}
}

func TestDownstreamCompletesShortSentPlaceholderWithoutRouting(t *testing.T) {
	store := &captureStore{}
	handler := NewDownstream(store, testMapper(t), func(otogi.Event) ([]byte, error) {
		t.Fatal("short-sent placeholder was routed")
		return nil, nil
	}, NewHealth())
	ctx := context.WithValue(context.Background(), journalContextKey{}, int64(9))
	batch := &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateNewMessage{Message: &tg.MessageEmpty{ID: 5}, Pts: 9, PtsCount: 1}}}
	if err := handler.Handle(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if len(store.captures) != 1 || store.captures[0].Quarantine == nil || store.captures[0].KeepJournalPending {
		t.Fatalf("captures=%+v", store.captures)
	}
}

func TestDownstreamAttachesPeersToFirstEmittedCaptureAfterPlaceholder(t *testing.T) {
	store := &captureStore{}
	handler := NewDownstream(store, testMapper(t), func(otogi.Event) ([]byte, error) { return []byte("event"), nil }, NewHealth())
	ctx := context.WithValue(context.Background(), journalContextKey{}, int64(9))
	placeholder := &tg.UpdateNewMessage{Message: &tg.MessageEmpty{ID: 4}, Pts: 4, PtsCount: 1}
	message := mappedBatch().Updates[0]
	batch := &tg.Updates{Updates: []tg.UpdateClass{placeholder, message}, Users: []tg.UserClass{&tg.User{ID: 7, AccessHash: 8, FirstName: "A"}}}
	if err := handler.Handle(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if len(store.captures) != 2 || len(store.captures[0].Peers) != 1 || len(store.captures[1].Peers) != 0 {
		t.Fatalf("captures=%+v", store.captures)
	}
}

func TestDownstreamBoundsCombinedPeerCountBeforeEncoding(t *testing.T) {
	store := &captureStore{}
	health := NewHealth()
	handler := NewDownstream(store, testMapper(t), func(otogi.Event) ([]byte, error) { return nil, nil }, health)
	users := make([]tg.UserClass, telegramstore.MaxCaptureRows)
	for i := range users {
		users[i] = &tg.UserEmpty{ID: int64(i + 1)}
	}
	batch := &tg.Updates{Users: users, Chats: []tg.ChatClass{&tg.ChatEmpty{ID: 1}}}
	ctx := context.WithValue(context.Background(), journalContextKey{}, int64(9))
	if err := handler.Handle(ctx, batch); err == nil {
		t.Fatal("oversized peers accepted")
	}
	if len(store.captures) != 0 {
		t.Fatal("oversized peers reached capture")
	}
}

func TestDownstreamQuarantineAdvancesCommonAndChannelConsistency(t *testing.T) {
	for _, tc := range []struct {
		name   string
		update tg.UpdateClass
		box    string
		pos    int64
	}{
		{name: "common", update: &tg.UpdateReadHistoryInbox{Peer: &tg.PeerUser{UserID: 7}, MaxID: 5, Pts: 11, PtsCount: 1}, box: "common", pos: 11},
		{name: "channel", update: &tg.UpdateReadChannelInbox{ChannelID: 77, MaxID: 5, Pts: 12}, box: "channel:77", pos: 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &captureStore{}
			handler := NewDownstream(store, testMapper(t), func(otogi.Event) ([]byte, error) { return nil, nil }, NewHealth())
			ctx := context.WithValue(context.Background(), journalContextKey{}, int64(9))
			if err := handler.Handle(ctx, &tg.Updates{Updates: []tg.UpdateClass{tc.update}}); err != nil {
				t.Fatal(err)
			}
			if len(store.captures) != 1 || store.captures[0].Quarantine == nil || store.captures[0].Box != tc.box || store.captures[0].Position != tc.pos {
				t.Fatalf("capture=%+v", store.captures)
			}
		})
	}
}

func TestDownstreamCapturesUpdateMessageIDCorrelation(t *testing.T) {
	store := &captureStore{}
	handler := NewDownstream(store, testMapper(t), func(otogi.Event) ([]byte, error) { return []byte("event"), nil }, NewHealth())
	packet := &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateMessageID{RandomID: 77, ID: 88}}}
	if err := handler.Handle(context.Background(), packet); err != nil {
		t.Fatal(err)
	}
	if len(store.captures) != 1 || len(store.captures[0].SendCorrelations) != 1 || store.captures[0].SendCorrelations[0].RandomID != 77 || store.captures[0].SendCorrelations[0].MessageID != 88 {
		t.Fatalf("captures=%+v", store.captures)
	}
}
