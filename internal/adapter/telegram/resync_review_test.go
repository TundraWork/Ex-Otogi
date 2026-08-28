package telegram

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/gotd/td/tg"
)

func testLimits() ResyncLimits {
	return ResyncLimits{MaxDifferencePages: 4, MaxHistoryPages: 2, HistoryPageSize: 2, MaxReprocessRows: 2, CallTimeout: time.Second, MaxResponseItems: 8, MaxResponseBytes: 64 << 10, MaxPeerPages: 2}
}

func TestDifferenceEmptyPreservesNewerCommonPtsAndQts(t *testing.T) {
	s := &fakeRecoveryStore{records: map[string]telegramstore.RecoveryRecord{"common": {Box: "common", State: telegramstore.RecoveryDegraded}}, state: UpdateState{Pts: 20, Qts: 21, Date: 10, Seq: 11}}
	a := &fakeResyncAPI{state: &tg.UpdatesState{Pts: 100, Qts: 101, Date: 12, Seq: 13}, differences: []tg.UpdatesDifferenceClass{&tg.UpdatesDifferenceEmpty{Date: 12, Seq: 13}}}
	if err := NewResynchronizer(a, s, &fakeReplay{}, testLimits()).Run(context.Background()); err != nil { t.Fatal(err) }
	if s.state != (UpdateState{Pts: 20, Qts: 21, Date: 12, Seq: 13}) { t.Fatalf("state=%+v", s.state) }
	if a.lastDifference == nil { t.Fatal("missing request") }
	if _, ok := a.lastDifference.GetPtsLimit(); !ok { t.Fatal("pts_limit not set") }
	if _, ok := a.lastDifference.GetPtsTotalLimit(); !ok { t.Fatal("pts_total_limit not set") }
	if _, ok := a.lastDifference.GetQtsLimit(); !ok { t.Fatal("qts_limit not set") }
}

func TestDifferenceTooLongBackfillsThenContinuesDifference(t *testing.T) {
	s := &fakeRecoveryStore{records: map[string]telegramstore.RecoveryRecord{"common": {Box: "common", State: telegramstore.RecoveryDegraded}}, state: UpdateState{Pts: 1, Qts: 2, Date: 3, Seq: 4}, peers: []PeerState{{Kind: "chat", ID: 7}}}
	a := &fakeResyncAPI{state: &tg.UpdatesState{Pts: 100}, differences: []tg.UpdatesDifferenceClass{&tg.UpdatesDifferenceTooLong{Pts: 50}, &tg.UpdatesDifferenceEmpty{Date: 9, Seq: 10}}, history: []tg.MessagesMessagesClass{&tg.MessagesMessages{}}}
	if err := NewResynchronizer(a, s, &fakeReplay{}, testLimits()).Run(context.Background()); err != nil { t.Fatal(err) }
	if a.diffCalls != 2 || s.state.Pts != 50 || s.state.Qts != 2 { t.Fatalf("calls=%d state=%+v", a.diffCalls, s.state) }
}

func TestChannelDifferenceReplaysMessagesAndOtherUpdatesBeforeReady(t *testing.T) {
	s := &fakeRecoveryStore{records: map[string]telegramstore.RecoveryRecord{"channel:4": {Box: "channel:4", State: telegramstore.RecoveryDegraded}}, channels: []ChannelState{{ID: 4, AccessHash: 5, Pts: 1}}}
	a := &fakeResyncAPI{channels: []tg.UpdatesChannelDifferenceClass{&tg.UpdatesChannelDifference{Final: true, Pts: 8, NewMessages: []tg.MessageClass{&tg.MessageEmpty{ID: 2}}, OtherUpdates: []tg.UpdateClass{&tg.UpdateUserStatus{UserID: 3, Status: &tg.UserStatusEmpty{}}}}}}
	r := &fakeReplay{}
	if err := NewResynchronizer(a, s, r, testLimits()).Run(context.Background()); err != nil { t.Fatal(err) }
	if r.histories != 1 || r.updates != 1 || s.channels[0].Pts != 8 { t.Fatalf("replay=%+v channel=%+v", r, s.channels) }
}

func TestChannelTooLongUsesDialogReplacementPts(t *testing.T) {
	s := &fakeRecoveryStore{records: map[string]telegramstore.RecoveryRecord{"channel:4": {Box: "channel:4", State: telegramstore.RecoveryDegraded}}, channels: []ChannelState{{ID: 4, AccessHash: 5, Pts: 1}}}
	d := &tg.Dialog{Peer:&tg.PeerChannel{ChannelID:4},Pts: 44,NotifySettings:tg.PeerNotifySettings{}}; d.SetPts(44)
	a := &fakeResyncAPI{channels: []tg.UpdatesChannelDifferenceClass{&tg.UpdatesChannelDifferenceTooLong{Final: true, Dialog: d}}, history: []tg.MessagesMessagesClass{&tg.MessagesMessages{}}}
	if err := NewResynchronizer(a, s, &fakeReplay{}, testLimits()).Run(context.Background()); err != nil { t.Fatal(err) }
	if s.channels[0].Pts != 44 { t.Fatalf("pts=%d", s.channels[0].Pts) }
}

func TestMissingChannelCursorStillAttemptsDifferenceFromZero(t *testing.T) {
	s := &fakeRecoveryStore{records: map[string]telegramstore.RecoveryRecord{"channel:4": {Box: "channel:4", State: telegramstore.RecoveryDegraded}}, channels: []ChannelState{{ID: 4, AccessHash: 5}}}
	a := &fakeResyncAPI{channels: []tg.UpdatesChannelDifferenceClass{&tg.UpdatesChannelDifferenceEmpty{Final: true, Pts: 9}}}
	if err := NewResynchronizer(a, s, &fakeReplay{}, testLimits()).Run(context.Background()); err != nil { t.Fatal(err) }
	if a.lastChannel == nil || a.lastChannel.Pts != 0 { t.Fatalf("request=%+v", a.lastChannel) }
}

func TestKnownPeerPaginationExhaustionKeepsCommonDegraded(t *testing.T) {
	s := &fakeRecoveryStore{records: map[string]telegramstore.RecoveryRecord{"common": {Box: "common", State: telegramstore.RecoveryDegraded}}, peers: []PeerState{{Kind: "chat", ID: 1}, {Kind: "chat", ID: 2}, {Kind: "chat", ID: 3}, {Kind: "chat", ID: 4}, {Kind: "chat", ID: 5}}}
	a := &fakeResyncAPI{state: &tg.UpdatesState{}, differences: []tg.UpdatesDifferenceClass{&tg.UpdatesDifferenceTooLong{Pts: 2}}, history: []tg.MessagesMessagesClass{&tg.MessagesMessages{}, &tg.MessagesMessages{}, &tg.MessagesMessages{}, &tg.MessagesMessages{}}}
	limits := testLimits(); limits.MaxPeerPages = 2; limits.PeerPageSize = 2
	err := NewResynchronizer(a, s, &fakeReplay{}, limits).Run(context.Background())
	if !errors.Is(err, ErrRecoveryBound) || s.records["common"].State == telegramstore.RecoveryReady { t.Fatalf("err=%v state=%+v", err, s.records["common"]) }
}

func TestOversizedDifferenceRejectedBeforeReplay(t *testing.T) {
	s := &fakeRecoveryStore{records: map[string]telegramstore.RecoveryRecord{"common": {Box: "common", State: telegramstore.RecoveryDegraded}}}
	a := &fakeResyncAPI{state: &tg.UpdatesState{}, differences: []tg.UpdatesDifferenceClass{&tg.UpdatesDifference{OtherUpdates: []tg.UpdateClass{&tg.UpdateUserStatus{UserID:1,Status:&tg.UserStatusEmpty{}},&tg.UpdateUserStatus{UserID:2,Status:&tg.UserStatusEmpty{}},&tg.UpdateUserStatus{UserID:3,Status:&tg.UserStatusEmpty{}}}}}}
	r := &fakeReplay{}; limits := testLimits(); limits.MaxResponseItems = 2
	err := NewResynchronizer(a, s, r, limits).Run(context.Background())
	if !errors.Is(err, ErrRecoveryBound) || r.differences != 0 { t.Fatalf("err=%v replay=%+v", err, r) }
}

func TestCommonBackfillContinuesAcrossFullEffectiveLimitPages(t *testing.T) {
	s := &fakeRecoveryStore{records: map[string]telegramstore.RecoveryRecord{"common": {Box: "common", State: telegramstore.RecoveryDegraded}}, peers: []PeerState{{Kind: "chat", ID: 7}}}
	a := &fakeResyncAPI{
		state: &tg.UpdatesState{},
		differences: []tg.UpdatesDifferenceClass{&tg.UpdatesDifferenceTooLong{Pts: 5}, &tg.UpdatesDifferenceEmpty{}},
		history: []tg.MessagesMessagesClass{
			&tg.MessagesMessages{Messages: []tg.MessageClass{&tg.MessageEmpty{ID: 6}, &tg.MessageEmpty{ID: 5}}},
			&tg.MessagesMessages{Messages: []tg.MessageClass{&tg.MessageEmpty{ID: 4}, &tg.MessageEmpty{ID: 3}}},
			&tg.MessagesMessages{Messages: []tg.MessageClass{&tg.MessageEmpty{ID: 2}}},
		},
	}
	limits := testLimits(); limits.HistoryPageSize = 5; limits.MaxResponseItems = 2; limits.MaxHistoryPages = 4
	if err := NewResynchronizer(a, s, &fakeReplay{}, limits).Run(context.Background()); err != nil { t.Fatal(err) }
	if a.historyCalls != 3 { t.Fatalf("history calls=%d", a.historyCalls) }
}

func TestChannelBackfillContinuesAcrossFullEffectiveLimitPages(t *testing.T) {
	s := &fakeRecoveryStore{records: map[string]telegramstore.RecoveryRecord{"channel:4": {Box: "channel:4", State: telegramstore.RecoveryDegraded}}, channels: []ChannelState{{ID: 4, AccessHash: 5, Pts: 1}}}
	dialog := &tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 4}, Pts: 8, NotifySettings: tg.PeerNotifySettings{}}; dialog.SetPts(8)
	a := &fakeResyncAPI{
		channels: []tg.UpdatesChannelDifferenceClass{&tg.UpdatesChannelDifferenceTooLong{Final: true, Dialog: dialog}},
		history: []tg.MessagesMessagesClass{
			&tg.MessagesMessages{Messages: []tg.MessageClass{&tg.MessageEmpty{ID: 6}, &tg.MessageEmpty{ID: 5}}},
			&tg.MessagesMessages{Messages: []tg.MessageClass{&tg.MessageEmpty{ID: 4}, &tg.MessageEmpty{ID: 3}}},
			&tg.MessagesMessages{Messages: []tg.MessageClass{&tg.MessageEmpty{ID: 2}}},
		},
	}
	limits := testLimits(); limits.HistoryPageSize = 5; limits.MaxResponseItems = 2; limits.MaxHistoryPages = 4
	if err := NewResynchronizer(a, s, &fakeReplay{}, limits).Run(context.Background()); err != nil { t.Fatal(err) }
	if a.historyCalls != 3 { t.Fatalf("history calls=%d", a.historyCalls) }
}

func TestConcurrentRunsSerializeCancellably(t *testing.T) {
	s := &fakeRecoveryStore{records: map[string]telegramstore.RecoveryRecord{"common": {Box: "common", State: telegramstore.RecoveryDegraded}}}
	entered := make(chan struct{}); release := make(chan struct{})
	a := &fakeResyncAPI{state: &tg.UpdatesState{}, differenceHook: func(){ close(entered); <-release }, differences: []tg.UpdatesDifferenceClass{&tg.UpdatesDifferenceEmpty{}}}
	r := NewResynchronizer(a, s, &fakeReplay{}, testLimits())
	done := make(chan error, 1); go func(){done <- r.Run(context.Background())}(); <-entered
	ctx, cancel := context.WithCancel(context.Background()); cancel()
	if err := r.Run(ctx); !errors.Is(err, context.Canceled) { t.Fatalf("second run=%v", err) }
	close(release); if err := <-done; err != nil { t.Fatal(err) }
	if a.diffCalls != 1 { t.Fatalf("calls=%d", a.diffCalls) }
	_ = sync.Mutex{}
}
