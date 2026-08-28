package telegram

import (
	"context"

	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	gotdupdates "github.com/gotd/td/telegram/updates"
)

// StoreRecovery adapts the durable store to the recovery coordinator without
// exposing generated Telegram types outside the adapter package.
type StoreRecovery struct {
	Store  *telegramstore.Store
	UserID int64
}

func (s StoreRecovery) BeginRecovery(ctx context.Context, box string) error { return s.Store.BeginRecovery(ctx,box) }
func (s StoreRecovery) MarkRecoveryReady(ctx context.Context, box string, position int64) error { return s.Store.MarkRecoveryReady(ctx,box,position) }
func (s StoreRecovery) MarkRecoveryPermanent(ctx context.Context, box, cause string) error { return s.Store.MarkRecoveryPermanent(ctx,box,cause) }
func (s StoreRecovery) RequiredRecoveries(ctx context.Context)([]telegramstore.RecoveryRecord,error){return s.Store.RequiredRecoveries(ctx)}
func (s StoreRecovery) SaveChannelState(ctx context.Context,id int64,pts int)error{return s.Store.SetChannelPts(ctx,s.UserID,id,pts)}
func (s StoreRecovery) LoadUpdateState(ctx context.Context)(UpdateState,error){state,found,err:=s.Store.GetState(ctx,s.UserID);if err!=nil{return UpdateState{},err};if !found{return UpdateState{},nil};return UpdateState{Pts:state.Pts,Qts:state.Qts,Date:state.Date,Seq:state.Seq},nil}
func (s StoreRecovery) SaveUpdateState(ctx context.Context,state UpdateState)error{return s.Store.SetState(ctx,s.UserID,gotdupdates.State{Pts:state.Pts,Qts:state.Qts,Date:state.Date,Seq:state.Seq})}
func (s StoreRecovery) RecoveryChannels(ctx context.Context)([]ChannelState,error){
	var channels []ChannelState
	err:=s.Store.ForEachChannels(ctx,s.UserID,func(ctx context.Context,id int64,pts int)error{hash,found,err:=s.Store.GetChannelAccessHash(ctx,s.UserID,id);if err!=nil{return err};if !found{hash=0};channels=append(channels,ChannelState{ID:id,AccessHash:hash,Pts:pts});return nil})
	return channels,err
}

func(s StoreRecovery)RecoveryPeers(ctx context.Context,limit int)([]PeerState,error){peers,err:=s.Store.KnownPeers(ctx,limit);if err!=nil{return nil,err};result:=make([]PeerState,len(peers));for i,peer:=range peers{result[i]=PeerState{Kind:peer.Kind,ID:peer.ID,AccessHash:peer.AccessHash}};return result,nil}
var _ RecoveryStore = StoreRecovery{}
