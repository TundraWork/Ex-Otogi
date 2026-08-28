package updates

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

type memoryJournal struct {
	mu        sync.Mutex
	next      int64
	rows      map[int64][]byte
	pending   []int64
	calls     []string
	appendErr error
}

func (m *memoryJournal) AppendJournal(_ context.Context, payload []byte, _ time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.appendErr != nil {
		return 0, m.appendErr
	}
	m.next++
	if m.rows == nil {
		m.rows = map[int64][]byte{}
	}
	m.rows[m.next] = append([]byte(nil), payload...)
	m.pending = append(m.pending, m.next)
	m.calls = append(m.calls, fmt.Sprintf("journal:%d", m.next))
	return m.next, nil
}
func (m *memoryJournal) StartupJournalIDs(_ context.Context, limit int) ([]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.pending) > limit {
		return append([]int64(nil), m.pending[:limit]...), telegramstore.ErrStartupOverflow
	}
	return append([]int64(nil), m.pending...), nil
}
func (m *memoryJournal) Journal(_ context.Context, id int64) (telegramstore.JournalRow, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.rows[id]
	return telegramstore.JournalRow{ID: id, Payload: append([]byte(nil), p...)}, ok, nil
}

type recordingUpdates struct {
	mu    sync.Mutex
	ids   []int
	calls *[]string
}

func (r *recordingUpdates) Handle(ctx context.Context, u tg.UpdatesClass) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var id int
	switch value := u.(type) {
	case *tg.UpdateShort:
		id = value.Date
	case *tg.Updates:
		id = value.Date
		if len(value.Updates) == 1 {
			if status, ok := value.Updates[0].(*tg.UpdateUserStatus); ok {
				id = int(status.UserID)
			}
		}
	default:
		return fmt.Errorf("unexpected update %T", u)
	}
	r.ids = append(r.ids, id)
	if r.calls != nil {
		*r.calls = append(*r.calls, fmt.Sprintf("manager:%d", id))
	}
	if JournalID(ctx) <= 0 {
		return errors.New("missing journal id")
	}
	return nil
}

func (r *recordingUpdates) snapshot() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int(nil), r.ids...)
}

func packet(id int) tg.UpdatesClass {
	return &tg.UpdateShort{Update: &tg.UpdateUserStatus{UserID: int64(id), Status: &tg.UserStatusEmpty{}}, Date: id}
}

func encodePacket(t *testing.T, u tg.UpdatesClass) []byte {
	t.Helper()
	var b bin.Buffer
	if err := (&tg.UpdatesBox{Updates: u}).Encode(&b); err != nil {
		t.Fatal(err)
	}
	return b.Copy()
}

func TestJournalGateBuffersUntilOpenAndReplaysInReceiptOrder(t *testing.T) {
	journal := &memoryJournal{}
	manager := &recordingUpdates{calls: &journal.calls}
	gate := NewJournalGate(journal, manager)
	for i := 1; i <= 3; i++ {
		if err := gate.Handle(context.Background(), packet(i)); err != nil {
			t.Fatal(err)
		}
	}
	if len(manager.ids) != 0 {
		t.Fatalf("manager called before Open: %v", manager.ids)
	}
	if err := gate.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(manager.ids); got != "[1 2 3]" {
		t.Fatalf("replay order = %s", got)
	}
	if err := gate.Handle(context.Background(), packet(4)); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(manager.ids); got != "[1 2 3 4]" {
		t.Fatalf("live order = %s", got)
	}
	want := "[journal:1 journal:2 journal:3 manager:1 manager:2 manager:3 journal:4 manager:4]"
	if got := fmt.Sprint(journal.calls); got != want {
		t.Fatalf("call order = %s, want %s", got, want)
	}
}

func TestJournalGateRejectsSixtyFifthBufferedPacketWithoutBypass(t *testing.T) {
	journal := &memoryJournal{}
	manager := &recordingUpdates{}
	gate := NewJournalGate(journal, manager)
	for i := 1; i <= StartupJournalLimit; i++ {
		if err := gate.Handle(context.Background(), packet(i)); err != nil {
			t.Fatalf("packet %d: %v", i, err)
		}
	}
	if err := gate.Handle(context.Background(), packet(65)); !errors.Is(err, ErrStartupOverflow) {
		t.Fatalf("65th error = %v", err)
	}
	if len(manager.ids) != 0 {
		t.Fatalf("overflow bypassed gate: %v", manager.ids)
	}
	if err := gate.Open(context.Background()); !errors.Is(err, ErrStartupOverflow) {
		t.Fatalf("Open error = %v", err)
	}
}

func TestJournalGateDoesNotCallManagerWhenAppendFails(t *testing.T) {
	journal := &memoryJournal{appendErr: errors.New("disk full")}
	manager := &recordingUpdates{}
	gate := NewJournalGate(journal, manager)
	if err := gate.Handle(context.Background(), packet(1)); err == nil {
		t.Fatal("expected append failure")
	}
	if len(manager.ids) != 0 {
		t.Fatalf("manager called after append failure: %v", manager.ids)
	}
}

func TestJournalGateRecoversPendingRowsOnOpen(t *testing.T) {
	journal := &memoryJournal{next: 2, rows: map[int64][]byte{1: encodePacket(t, packet(1)), 2: encodePacket(t, packet(2))}, pending: []int64{1, 2}}
	manager := &recordingUpdates{}
	gate := NewJournalGate(journal, manager)
	if err := gate.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(manager.ids); got != "[1 2]" {
		t.Fatalf("recovered = %s", got)
	}
}

func TestCorrelatingHandlerPreservesEachSourceAcrossCombinedDelivery(t *testing.T) {
	correlation := newJournalCorrelation()
	first := &tg.UpdateUserStatus{UserID: 1, Status: &tg.UserStatusEmpty{}}
	second := &tg.UpdateUserStatus{UserID: 2, Status: &tg.UserStatusEmpty{}}
	if err := correlation.register(11, &tg.Updates{Updates: []tg.UpdateClass{first}}); err != nil {
		t.Fatal(err)
	}
	if err := correlation.register(12, &tg.Updates{Updates: []tg.UpdateClass{second}}); err != nil {
		t.Fatal(err)
	}
	var ids []int64
	var complete []bool
	handler := correlatingHandler{correlation: correlation, next: telegram.UpdateHandlerFunc(func(ctx context.Context, update tg.UpdatesClass) error {
		ids = append(ids, JournalID(ctx))
		complete = append(complete, journalComplete(ctx))
		if got := len(packetUpdates(update)); got != 1 {
			t.Fatalf("delivery update count = %d", got)
		}
		return nil
	})}
	if err := handler.Handle(context.Background(), &tg.Updates{Updates: []tg.UpdateClass{first, second}}); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(ids); got != "[11 12]" {
		t.Fatalf("journal ids = %s", got)
	}
	if got := fmt.Sprint(complete); got != "[true true]" {
		t.Fatalf("completion = %s", got)
	}
}

func TestCorrelationTracksEveryDerivedDeliveryBeforeCompletingSource(t *testing.T) {
	correlation := newJournalCorrelation()
	first := &tg.UpdateUserStatus{UserID: 1, Status: &tg.UserStatusEmpty{}}
	second := &tg.UpdateUserStatus{UserID: 2, Status: &tg.UserStatusEmpty{}}
	if err := correlation.register(11, &tg.Updates{Updates: []tg.UpdateClass{first, second}}); err != nil {
		t.Fatal(err)
	}
	var complete []bool
	handler := correlatingHandler{correlation: correlation, next: telegram.UpdateHandlerFunc(func(ctx context.Context, _ tg.UpdatesClass) error {
		if JournalID(ctx) != 11 {
			t.Fatalf("journal id = %d", JournalID(ctx))
		}
		complete = append(complete, journalComplete(ctx))
		return nil
	})}
	for _, update := range []tg.UpdateClass{first, second} {
		if err := handler.Handle(context.Background(), &tg.Updates{Updates: []tg.UpdateClass{update}}); err != nil {
			t.Fatal(err)
		}
	}
	if got := fmt.Sprint(complete); got != "[false true]" {
		t.Fatalf("completion = %s", got)
	}
}

func TestCorrelationSurvivesShortUpdateTransformation(t *testing.T) {
	correlation := newJournalCorrelation()
	short := &tg.UpdateShortMessage{ID: 5, UserID: 7, Message: "hello", Pts: 9, PtsCount: 1, Date: 10}
	if err := correlation.register(11, short); err != nil {
		t.Fatal(err)
	}
	transformed := &tg.UpdateNewMessage{Message: &tg.Message{ID: 5, PeerID: &tg.PeerUser{UserID: 7}, Message: "hello", Date: 10}, Pts: 9, PtsCount: 1}
	var journalID int64
	handler := correlatingHandler{correlation: correlation, next: telegram.UpdateHandlerFunc(func(ctx context.Context, _ tg.UpdatesClass) error {
		journalID = JournalID(ctx)
		return nil
	})}
	if err := handler.Handle(context.Background(), &tg.Updates{Updates: []tg.UpdateClass{transformed}}); err != nil {
		t.Fatal(err)
	}
	if journalID != 11 {
		t.Fatalf("journal id = %d", journalID)
	}
}

func TestCorrelationCompletesSupersededSourcesWithDifferenceOutput(t *testing.T) {
	correlation := newJournalCorrelation()
	for id := int64(11); id <= 12; id++ {
		if err := correlation.register(id, &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateUserStatus{UserID: id, Status: &tg.UserStatusEmpty{}}}}); err != nil {
			t.Fatal(err)
		}
	}
	var ids []int64
	handler := correlatingHandler{correlation: correlation, next: telegram.UpdateHandlerFunc(func(ctx context.Context, _ tg.UpdatesClass) error {
		ids = append(ids, JournalID(ctx))
		if !journalComplete(ctx) {
			t.Fatal("superseded source was not completed")
		}
		return nil
	})}
	difference := &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateUserName{UserID: 99, FirstName: "new"}}}
	if err := handler.Handle(context.Background(), difference); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(ids); got != "[11 12]" {
		t.Fatalf("superseded journal ids = %s", got)
	}
}
