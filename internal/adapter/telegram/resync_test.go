package telegram

import (
	"context"
	"errors"
	"testing"
	"time"

	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

type fakeRecoveryStore struct {
	records map[string]telegramstore.RecoveryRecord
	state   UpdateState
	channels []ChannelState
	peers []PeerState
}
func (s *fakeRecoveryStore) BeginRecovery(_ context.Context, box string) error { r:=s.records[box]; r.State=telegramstore.RecoveryResynchronizing; s.records[box]=r; return nil }
func (s *fakeRecoveryStore) MarkRecoveryReady(_ context.Context, box string, p int64) error { r:=s.records[box]; r.State=telegramstore.RecoveryReady; r.Position=p; s.records[box]=r; return nil }
func (s *fakeRecoveryStore) MarkRecoveryPermanent(_ context.Context, box, cause string) error { r:=s.records[box]; r.State=telegramstore.RecoveryPermanentDegraded; r.Cause=cause; s.records[box]=r; return nil }
func(s *fakeRecoveryStore)RecordGap(_ context.Context,box string,p int64,cause string)error{r:=s.records[box];r.State=telegramstore.RecoveryDegraded;r.Position=max(r.Position,p);r.Cause=cause;r.Acknowledged=false;s.records[box]=r;return nil}
func (s *fakeRecoveryStore) RequiredRecoveries(context.Context) ([]telegramstore.RecoveryRecord,error) { out:=make([]telegramstore.RecoveryRecord,0,len(s.records)); for _,r:=range s.records { if r.State != telegramstore.RecoveryReady { out=append(out,r) } }; return out,nil }
func (s *fakeRecoveryStore) LoadUpdateState(context.Context) (UpdateState,error) { return s.state,nil }
func (s *fakeRecoveryStore) SaveUpdateState(_ context.Context, state UpdateState) error { s.state=state; return nil }
func (s *fakeRecoveryStore) SaveChannelState(_ context.Context,id int64,pts int)error{for i:=range s.channels{if s.channels[i].ID==id{s.channels[i].Pts=pts;return nil}};return nil}
func (s *fakeRecoveryStore) RecoveryChannels(context.Context) ([]ChannelState,error) { return append([]ChannelState(nil),s.channels...),nil }
func (s *fakeRecoveryStore) RecoveryPeers(_ context.Context, offset, limit int)([]PeerState,bool,error){if offset>=len(s.peers){return nil,false,nil};end:=min(offset+limit,len(s.peers));return append([]PeerState(nil),s.peers[offset:end]...),end<len(s.peers),nil}

type fakeResyncAPI struct { state *tg.UpdatesState; differences []tg.UpdatesDifferenceClass; channels []tg.UpdatesChannelDifferenceClass; history []tg.MessagesMessagesClass; diffCalls, channelCalls, historyCalls int; channelErr error; err error; lastDifference *tg.UpdatesGetDifferenceRequest; lastChannel *tg.UpdatesGetChannelDifferenceRequest; differenceHook func() }
func (a *fakeResyncAPI) UpdatesGetState(context.Context)(*tg.UpdatesState,error){ return a.state,a.err }
func (a *fakeResyncAPI) UpdatesGetDifference(_ context.Context,r *tg.UpdatesGetDifferenceRequest)(tg.UpdatesDifferenceClass,error){ a.diffCalls++; a.lastDifference=r; if a.differenceHook!=nil{a.differenceHook()}; if a.diffCalls>len(a.differences){return &tg.UpdatesDifferenceEmpty{},a.err}; return a.differences[a.diffCalls-1],a.err }
func (a *fakeResyncAPI) UpdatesGetChannelDifference(_ context.Context,r *tg.UpdatesGetChannelDifferenceRequest)(tg.UpdatesChannelDifferenceClass,error){ a.channelCalls++;a.lastChannel=r; if a.channelErr != nil { return nil,a.channelErr }; if a.channelCalls>len(a.channels){return &tg.UpdatesChannelDifferenceEmpty{Final:true},a.err}; return a.channels[a.channelCalls-1],a.err }
func (a *fakeResyncAPI) MessagesGetHistory(context.Context,*tg.MessagesGetHistoryRequest)(tg.MessagesMessagesClass,error){ a.historyCalls++; if a.historyCalls>len(a.history){return &tg.MessagesMessages{},a.err}; return a.history[a.historyCalls-1],a.err }
type fakeReplay struct{ differences, histories, updates, reprocessed int }
func (f *fakeReplay) Difference(context.Context,tg.UpdatesDifferenceClass) error { f.differences++; return nil }
func (f *fakeReplay) Updates(context.Context,string,tg.UpdatesClass) error { f.updates++; return nil }
func (f *fakeReplay) History(context.Context,string,tg.MessagesMessagesClass) error { f.histories++; return nil }
func (f *fakeReplay) Reprocess(_ context.Context, limit int) error { f.reprocessed=limit; return nil }
func TestResyncCommonConsumesSlicesAndPersistsRemoteState(t *testing.T){
	s:=&fakeRecoveryStore{records:map[string]telegramstore.RecoveryRecord{"common":{Box:"common",State:telegramstore.RecoveryDegraded}},state:UpdateState{Pts:1,Date:2,Qts:3}}
	a:=&fakeResyncAPI{state:&tg.UpdatesState{Pts:9,Qts:8,Date:7,Seq:6},differences:[]tg.UpdatesDifferenceClass{&tg.UpdatesDifferenceSlice{IntermediateState:tg.UpdatesState{Pts:5,Qts:4,Date:3,Seq:2}},&tg.UpdatesDifference{State:tg.UpdatesState{Pts:9,Qts:8,Date:7,Seq:6}}}}
	r:=&fakeReplay{}
	if err:=NewResynchronizer(a,s,r,ResyncLimits{MaxDifferencePages:3,MaxHistoryPages:2,HistoryPageSize:10,MaxReprocessRows:5,CallTimeout:time.Second}).Run(context.Background()); err!=nil { t.Fatal(err) }
	if a.diffCalls!=2 || r.differences!=2 || s.state.Pts!=9 || s.records["common"].State!=telegramstore.RecoveryReady { t.Fatalf("api=%+v replay=%+v state=%+v recovery=%+v",a,r,s.state,s.records) }
}

func TestResyncBoundsDifferencePagesAndRemainsDegraded(t *testing.T){
	s:=&fakeRecoveryStore{records:map[string]telegramstore.RecoveryRecord{"common":{Box:"common",State:telegramstore.RecoveryDegraded}},state:UpdateState{}}
	a:=&fakeResyncAPI{state:&tg.UpdatesState{},differences:[]tg.UpdatesDifferenceClass{&tg.UpdatesDifferenceSlice{},&tg.UpdatesDifferenceSlice{}}}
	err:=NewResynchronizer(a,s,&fakeReplay{},ResyncLimits{MaxDifferencePages:1,MaxHistoryPages:1,HistoryPageSize:1,MaxReprocessRows:1,CallTimeout:time.Second}).Run(context.Background())
	if !errors.Is(err,ErrRecoveryBound) || s.records["common"].State==telegramstore.RecoveryReady { t.Fatalf("err=%v recovery=%+v",err,s.records) }
}

func TestResyncInaccessibleChannelBecomesPermanent(t *testing.T){
	s:=&fakeRecoveryStore{records:map[string]telegramstore.RecoveryRecord{"channel:4":{Box:"channel:4",State:telegramstore.RecoveryDegraded}},channels:[]ChannelState{{ID:4,AccessHash:5,Pts:1}}}
	a:=&fakeResyncAPI{channelErr:&tgerr.Error{Code:400,Type:"CHANNEL_PRIVATE",Message:"CHANNEL_PRIVATE"}}
	if err:=NewResynchronizer(a,s,&fakeReplay{},ResyncLimits{MaxDifferencePages:2,MaxHistoryPages:1,HistoryPageSize:10,MaxReprocessRows:1,CallTimeout:time.Second}).Run(context.Background()); err!=nil { t.Fatal(err) }
	if s.records["channel:4"].State!=telegramstore.RecoveryPermanentDegraded { t.Fatalf("recovery=%+v",s.records) }
}

type fakeSources struct { journal []telegramstore.JournalRow; quarantine []telegramstore.QuarantineRow; normalized,completed []int64 }
func(s *fakeSources)PendingJournal(context.Context,int)([]telegramstore.JournalRow,error){return s.journal,nil}
func(s *fakeSources)PendingQuarantine(context.Context,int)([]telegramstore.QuarantineRow,error){return s.quarantine,nil}
func(s *fakeSources)MarkJournalNormalized(_ context.Context,id int64,_ time.Time)error{s.normalized=append(s.normalized,id);return nil}
func(s *fakeSources)CompleteQuarantine(_ context.Context,id int64)error{s.completed=append(s.completed,id);return nil}

func TestDurableReprocessorCompletesOnlySuccessfullyHandledSources(t *testing.T){
	s:=&fakeSources{journal:[]telegramstore.JournalRow{{ID:1,Payload:[]byte("j")}},quarantine:[]telegramstore.QuarantineRow{{ID:2,Payload:[]byte("q")}}}
	r:=NewDurableReprocessor(s,func(_ context.Context,payload []byte)error{return nil},func(_ context.Context,payload []byte)error{return nil},func()time.Time{return time.Unix(9,0).UTC()})
	if err:=r.Reprocess(context.Background(),2);err!=nil{t.Fatal(err)}
	if len(s.normalized)!=1||s.normalized[0]!=1||len(s.completed)!=1||s.completed[0]!=2{t.Fatalf("normalized=%v completed=%v",s.normalized,s.completed)}
	s=&fakeSources{journal:[]telegramstore.JournalRow{{ID:3,Payload:[]byte("bad")}}}
	r=NewDurableReprocessor(s,func(context.Context,[]byte)error{return errors.New("mapper")},func(context.Context,[]byte)error{return nil},time.Now)
	if err:=r.Reprocess(context.Background(),1);err==nil||len(s.normalized)!=0{t.Fatalf("err=%v normalized=%v",err,s.normalized)}
}
