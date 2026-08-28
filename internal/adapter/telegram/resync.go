package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

var ErrRecoveryBound = errors.New("telegram recovery bound exhausted")

// UpdateState is the persisted common recovery cursor.
type UpdateState struct{ Pts, Qts, Date, Seq int }

// PeerState is one known dialog identity for bounded history recovery.
type PeerState struct{ Kind string; ID, AccessHash int64 }
// ChannelState is one known channel recovery cursor and access identity.
type ChannelState struct{ ID, AccessHash int64; Pts int }

// ResyncLimits are mandatory hard bounds for one recovery run.
type ResyncLimits struct {
	MaxDifferencePages int
	MaxHistoryPages    int
	HistoryPageSize    int
	MaxReprocessRows   int
	CallTimeout        time.Duration
}

type ResyncAPI interface {
	UpdatesGetState(context.Context) (*tg.UpdatesState, error)
	UpdatesGetDifference(context.Context, *tg.UpdatesGetDifferenceRequest) (tg.UpdatesDifferenceClass, error)
	UpdatesGetChannelDifference(context.Context, *tg.UpdatesGetChannelDifferenceRequest) (tg.UpdatesChannelDifferenceClass, error)
	MessagesGetHistory(context.Context, *tg.MessagesGetHistoryRequest) (tg.MessagesMessagesClass, error)
}

type RecoveryStore interface {
	BeginRecovery(context.Context, string) error
	MarkRecoveryReady(context.Context, string, int64) error
	MarkRecoveryPermanent(context.Context, string, string) error
	RequiredRecoveries(context.Context) ([]telegramstore.RecoveryRecord, error)
	SaveChannelState(context.Context, int64, int) error
	LoadUpdateState(context.Context) (UpdateState, error)
	SaveUpdateState(context.Context, UpdateState) error
	RecoveryChannels(context.Context) ([]ChannelState, error)
	RecoveryPeers(context.Context, int) ([]PeerState, error)
}

// RecoveryReplay durably normalizes recovered difference/history data and reprocesses
// bounded journal/quarantine rows. Implementations retain stable source deduplication.
type RecoveryReplay interface {
	Difference(context.Context, tg.UpdatesDifferenceClass) error
	History(context.Context, string, tg.MessagesMessagesClass) error
	Reprocess(context.Context, int) error
}

type Resynchronizer struct { api ResyncAPI; store RecoveryStore; replay RecoveryReplay; limits ResyncLimits }

func NewResynchronizer(api ResyncAPI, store RecoveryStore, replay RecoveryReplay, limits ResyncLimits) *Resynchronizer {
	if api == nil || store == nil || replay == nil || limits.MaxDifferencePages <= 0 || limits.MaxHistoryPages <= 0 || limits.HistoryPageSize <= 0 || limits.HistoryPageSize > 100 || limits.MaxReprocessRows <= 0 || limits.CallTimeout <= 0 {
		panic("telegram: invalid resynchronizer dependency or bound")
	}
	return &Resynchronizer{api:api,store:store,replay:replay,limits:limits}
}

// Run is the single serialized owner of one persisted recovery pass.
func (r *Resynchronizer) Run(ctx context.Context) error {
	records, err := r.store.RequiredRecoveries(ctx)
	if err != nil { return err }
	channels, err := r.store.RecoveryChannels(ctx)
	if err != nil { return err }
	channelByID := make(map[int64]ChannelState, len(channels))
	for _, channel := range channels { channelByID[channel.ID] = channel }
	for _, record := range records {
		if err := ctx.Err(); err != nil { return err }
		if err := r.store.BeginRecovery(ctx, record.Box); err != nil { return err }
		if record.Box == "common" {
			if err := r.recoverCommon(ctx); err != nil { return err }
			continue
		}
		id, ok := channelBoxID(record.Box)
		channel, known := channelByID[id]
		if !ok || !known || channel.AccessHash == 0 {
			if err := r.store.MarkRecoveryPermanent(ctx, record.Box, "channel inaccessible: missing access hash"); err != nil { return err }
			continue
		}
		if err := r.recoverChannel(ctx, record.Box, channel); err != nil { return err }
	}
	return r.replay.Reprocess(ctx, r.limits.MaxReprocessRows)
}

func (r *Resynchronizer) callContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, r.limits.CallTimeout)
}

func (r *Resynchronizer) recoverCommon(ctx context.Context) error {
	call, cancel := r.callContext(ctx); remote, err := r.api.UpdatesGetState(call); cancel()
	if err != nil { return err }
	local, err := r.store.LoadUpdateState(ctx); if err != nil { return err }
	cursor := local
	for page := 0; page < r.limits.MaxDifferencePages; page++ {
		call, cancel = r.callContext(ctx)
		diff, callErr := r.api.UpdatesGetDifference(call, &tg.UpdatesGetDifferenceRequest{Pts:cursor.Pts,Qts:cursor.Qts,Date:cursor.Date})
		cancel()
		if callErr != nil { return callErr }
		if err := r.replay.Difference(ctx, diff); err != nil { return err }
		switch value := diff.(type) {
		case *tg.UpdatesDifferenceEmpty:
			cursor.Date, cursor.Seq = value.Date, value.Seq
			cursor.Pts, cursor.Qts = remote.Pts, remote.Qts
			return r.finishCommon(ctx, cursor)
		case *tg.UpdatesDifference:
			cursor = updateState(value.State)
			return r.finishCommon(ctx, cursor)
		case *tg.UpdatesDifferenceSlice:
			next := updateState(value.IntermediateState)
			if next == cursor { return fmt.Errorf("%w: common difference made no progress", ErrRecoveryBound) }
			cursor = next
		case *tg.UpdatesDifferenceTooLong:
			cursor.Pts = value.Pts
			if err := r.backfillKnown(ctx); err != nil { return err }
			return r.finishCommon(ctx, updateState(*remote))
		default:
			return fmt.Errorf("unsupported common difference %T", diff)
		}
	}
	return ErrRecoveryBound
}

func (r *Resynchronizer) finishCommon(ctx context.Context, state UpdateState) error {
	if err := r.store.SaveUpdateState(ctx,state); err != nil { return err }
	return r.store.MarkRecoveryReady(ctx,"common",int64(state.Pts))
}

func (r *Resynchronizer) recoverChannel(ctx context.Context, box string, channel ChannelState) error {
	cursor := channel.Pts
	input := &tg.InputChannel{ChannelID:channel.ID,AccessHash:channel.AccessHash}
	for page:=0; page<r.limits.MaxDifferencePages; page++ {
		call,cancel:=r.callContext(ctx)
		diff,err:=r.api.UpdatesGetChannelDifference(call,&tg.UpdatesGetChannelDifferenceRequest{Channel:input,Filter:&tg.ChannelMessagesFilterEmpty{},Pts:cursor,Limit:r.limits.HistoryPageSize})
		cancel()
		if err != nil {
			if inaccessible(err) { return r.store.MarkRecoveryPermanent(ctx,box,"channel inaccessible: "+err.Error()) }
			return err
		}
		switch value:=diff.(type) {
		case *tg.UpdatesChannelDifferenceEmpty:
			if value.Pts > 0 { cursor=value.Pts }
			return r.finishChannel(ctx,box,channel.ID,cursor)
		case *tg.UpdatesChannelDifference:
			if err:=r.replay.History(ctx,box,&tg.MessagesMessages{Messages:value.NewMessages,Chats:value.Chats,Users:value.Users}); err!=nil{return err}
			if value.Pts<=cursor && !value.Final{return fmt.Errorf("%w: channel difference made no progress",ErrRecoveryBound)}
			cursor=value.Pts
			if value.Final{return r.finishChannel(ctx,box,channel.ID,cursor)}
		case *tg.UpdatesChannelDifferenceTooLong:
			if err:=r.replay.History(ctx,box,&tg.MessagesMessages{Messages:value.Messages,Chats:value.Chats,Users:value.Users});err!=nil{return err}
			if err:=r.backfill(ctx,box,&tg.InputPeerChannel{ChannelID:channel.ID,AccessHash:channel.AccessHash});err!=nil{return err}
			return r.finishChannel(ctx,box,channel.ID,cursor)
		default:return fmt.Errorf("unsupported channel difference %T",diff)
		}
	}
	return ErrRecoveryBound
}

func(r *Resynchronizer)finishChannel(ctx context.Context,box string,id int64,pts int)error{if err:=r.store.SaveChannelState(ctx,id,pts);err!=nil{return err};return r.store.MarkRecoveryReady(ctx,box,int64(pts))}

func (r *Resynchronizer) backfillKnown(ctx context.Context) error {
	peers,err:=r.store.RecoveryPeers(ctx,r.limits.MaxHistoryPages);if err!=nil{return err}
	for _,peer:=range peers {
		var input tg.InputPeerClass
		switch peer.Kind { case "channel": if peer.AccessHash!=0{input=&tg.InputPeerChannel{ChannelID:peer.ID,AccessHash:peer.AccessHash}};case "user":if peer.AccessHash!=0{input=&tg.InputPeerUser{UserID:peer.ID,AccessHash:peer.AccessHash}};case "chat":input=&tg.InputPeerChat{ChatID:peer.ID} }
		if input==nil{return fmt.Errorf("known dialog %s:%d inaccessible",peer.Kind,peer.ID)}
		if err:=r.backfill(ctx,fmt.Sprintf("%s:%d",peer.Kind,peer.ID),input);err!=nil{return err}
	}
	return nil
}

func (r *Resynchronizer) backfill(ctx context.Context, box string, peer tg.InputPeerClass) error {
	offset:=0
	for page:=0;page<r.limits.MaxHistoryPages;page++{
		call,cancel:=r.callContext(ctx); result,err:=r.api.MessagesGetHistory(call,&tg.MessagesGetHistoryRequest{Peer:peer,OffsetID:offset,Limit:r.limits.HistoryPageSize});cancel();if err!=nil{return err}
		if err:=r.replay.History(ctx,box,result);err!=nil{return err}
		messages:=historyMessages(result);if len(messages)==0||len(messages)<r.limits.HistoryPageSize{return nil}
		oldest:=oldestMessageID(messages);if oldest<=0||(offset!=0&&oldest>=offset){return fmt.Errorf("%w: history made no progress",ErrRecoveryBound)};offset=oldest
	}
	return ErrRecoveryBound
}

func historyMessages(value tg.MessagesMessagesClass) []tg.MessageClass { if modified,ok:=value.AsModified();ok{return modified.GetMessages()};return nil }
func oldestMessageID(messages []tg.MessageClass) int { oldest:=0;for _,message:=range messages { id:=message.GetID();if id>0&&(oldest==0||id<oldest){oldest=id} };return oldest }
func updateState(v tg.UpdatesState) UpdateState{return UpdateState{Pts:v.Pts,Qts:v.Qts,Date:v.Date,Seq:v.Seq}}
func channelBoxID(box string)(int64,bool){if !strings.HasPrefix(box,"channel:"){return 0,false};id,err:=strconv.ParseInt(strings.TrimPrefix(box,"channel:"),10,64);return id,err==nil&&id>0}
func inaccessible(err error)bool{return tgerr.Is(err,"CHANNEL_PRIVATE","CHANNEL_INVALID","CHAT_FORBIDDEN","USER_BANNED_IN_CHANNEL")||tgerr.IsCode(err,401,403)}
