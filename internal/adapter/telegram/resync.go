package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// ErrRecoveryBound reports exhausted recovery limits.
var (
	ErrRecoveryBound = errors.New("telegram recovery bound exhausted")
	// ErrRecoveryInProgress reports a concurrent recovery pass.
	ErrRecoveryInProgress = errors.New("telegram recovery already in progress")
)

// UpdateState is the persisted common recovery cursor.
type UpdateState struct {
	// Pts is the common persistent timestamp.
	Pts int
	// Qts is the encrypted-update persistent timestamp.
	Qts int
	// Date is the last update date.
	Date int
	// Seq is the common update sequence.
	Seq int
}

// PeerState is one known dialog identity for bounded history recovery.
type PeerState struct {
	// Kind identifies the Telegram peer constructor family.
	Kind string
	// ID is the peer identifier.
	ID int64
	// AccessHash authorizes peer-scoped history requests.
	AccessHash int64
}

// ChannelState is one known channel recovery cursor and access identity.
type ChannelState struct {
	// ID is the channel identifier.
	ID int64
	// AccessHash authorizes channel difference and history requests.
	AccessHash int64
	// Pts is the last persisted channel position.
	Pts int
}

// ResyncLimits are mandatory hard bounds for one recovery run.
type ResyncLimits struct {
	// MaxDifferencePages bounds difference RPCs per box.
	MaxDifferencePages int
	// MaxHistoryPages bounds history RPCs per peer.
	MaxHistoryPages int
	// HistoryPageSize bounds messages requested per history RPC.
	HistoryPageSize int
	// MaxReprocessRows bounds raw journal and quarantine replay per run.
	MaxReprocessRows int
	// CallTimeout bounds each Telegram RPC.
	CallTimeout time.Duration
	// MaxResponseItems bounds decoded collections in one response.
	MaxResponseItems int
	// MaxResponseBytes bounds deterministic response encoding size.
	MaxResponseBytes int
	// MaxPeerPages bounds pages of known peers visited per run.
	MaxPeerPages int
	// PeerPageSize bounds known peers loaded per page.
	PeerPageSize int
}

// ResyncAPI is the public Telegram API surface required for recovery.
type ResyncAPI interface {
	// UpdatesGetState fetches the current common update state.
	UpdatesGetState(context.Context) (*tg.UpdatesState, error)
	// UpdatesGetDifference fetches one bounded common difference page.
	UpdatesGetDifference(context.Context, *tg.UpdatesGetDifferenceRequest) (tg.UpdatesDifferenceClass, error)
	// UpdatesGetChannelDifference fetches one bounded channel difference page.
	UpdatesGetChannelDifference(context.Context, *tg.UpdatesGetChannelDifferenceRequest) (tg.UpdatesChannelDifferenceClass, error)
	// MessagesGetHistory fetches one bounded peer history page.
	MessagesGetHistory(context.Context, *tg.MessagesGetHistoryRequest) (tg.MessagesMessagesClass, error)
}

// RecoveryStore persists recovery state, cursors, and known peers.
type RecoveryStore interface {
	// BeginRecovery marks one degraded box as resynchronizing.
	BeginRecovery(context.Context, string) error
	// MarkRecoveryReady records a reconciled source position.
	MarkRecoveryReady(context.Context, string, int64) error
	// MarkRecoveryPermanent records an unrecoverable box cause.
	MarkRecoveryPermanent(context.Context, string, string) error
	// RecordGap records a degraded consistency boundary.
	RecordGap(context.Context, string, int64, string) error
	// RequiredRecoveries returns all non-ready boxes requiring work.
	RequiredRecoveries(context.Context) ([]telegramstore.RecoveryRecord, error)
	// SaveChannelState persists one recovered channel cursor.
	SaveChannelState(context.Context, int64, int) error
	// LoadUpdateState returns the persisted common cursor.
	LoadUpdateState(context.Context) (UpdateState, error)
	// SaveUpdateState persists the common cursor.
	SaveUpdateState(context.Context, UpdateState) error
	// RecoveryChannels returns known channel identities and cursors.
	RecoveryChannels(context.Context) ([]ChannelState, error)
	// RecoveryPeers returns one stable page of known peers.
	RecoveryPeers(context.Context, int, int) ([]PeerState, bool, error)
}

// RecoveryReplay durably normalizes recovered difference/history data and reprocesses
// bounded journal/quarantine rows. Implementations retain stable source deduplication.
type RecoveryReplay interface {
	// Difference durably replays one common difference response.
	Difference(context.Context, tg.UpdatesDifferenceClass) error
	// Updates durably replays one ordered Telegram update box.
	Updates(context.Context, string, tg.UpdatesClass) error
	// History durably replays one Telegram history page.
	History(context.Context, string, tg.MessagesMessagesClass) error
	// Reprocess replays bounded pending raw sources.
	Reprocess(context.Context, int) error
}

// Resynchronizer owns one bounded persisted Telegram recovery pass at a time.
type Resynchronizer struct {
	api    ResyncAPI
	store  RecoveryStore
	replay RecoveryReplay
	limits ResyncLimits
	lease  chan struct{}
}

// NewResynchronizer constructs a bounded Telegram recovery coordinator.
func NewResynchronizer(api ResyncAPI, store RecoveryStore, replay RecoveryReplay, limits ResyncLimits) *Resynchronizer {
	if limits.MaxResponseItems <= 0 {
		limits.MaxResponseItems = telegramstore.MaxCaptureRows
	}
	if limits.MaxResponseBytes <= 0 {
		limits.MaxResponseBytes = telegramstore.MaxRawFieldBytes
	}
	if limits.MaxPeerPages <= 0 {
		limits.MaxPeerPages = limits.MaxHistoryPages
	}
	if limits.PeerPageSize <= 0 {
		limits.PeerPageSize = limits.HistoryPageSize
	}
	if api == nil || store == nil || replay == nil || limits.MaxDifferencePages <= 0 || limits.MaxHistoryPages <= 0 || limits.HistoryPageSize <= 0 || limits.HistoryPageSize > 100 || limits.MaxReprocessRows <= 0 || limits.CallTimeout <= 0 || limits.MaxResponseItems <= 0 || limits.MaxResponseBytes <= 0 || limits.MaxPeerPages <= 0 || limits.PeerPageSize <= 0 {
		panic("telegram: invalid resynchronizer dependency or bound")
	}
	return &Resynchronizer{api: api, store: store, replay: replay, limits: limits, lease: make(chan struct{}, 1)}
}

// Run is the single serialized owner of one persisted recovery pass.
func (r *Resynchronizer) Run(ctx context.Context) error {
	select {
	case r.lease <- struct{}{}:
		defer func() { <-r.lease }()
	case <-ctx.Done():
		return ctx.Err()
	}
	records, err := r.store.RequiredRecoveries(ctx)
	if err != nil {
		return err
	}
	channels, err := r.store.RecoveryChannels(ctx)
	if err != nil {
		return err
	}
	channelByID := make(map[int64]ChannelState, len(channels))
	for _, channel := range channels {
		channelByID[channel.ID] = channel
	}
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.store.BeginRecovery(ctx, record.Box); err != nil {
			return err
		}
		if record.Box == "common" {
			if err := r.recoverCommon(ctx); err != nil {
				_ = r.store.RecordGap(ctx, record.Box, record.Position, err.Error())
				return err
			}
			continue
		}
		id, ok := channelBoxID(record.Box)
		channel, known := channelByID[id]
		if !ok || !known || channel.AccessHash == 0 {
			if err := r.store.MarkRecoveryPermanent(ctx, record.Box, "channel inaccessible: missing access hash"); err != nil {
				return err
			}
			continue
		}
		if err := r.recoverChannel(ctx, record.Box, channel); err != nil {
			_ = r.store.RecordGap(ctx, record.Box, record.Position, err.Error())
			return err
		}
	}
	return r.replay.Reprocess(ctx, r.limits.MaxReprocessRows)
}

func (r *Resynchronizer) callContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, r.limits.CallTimeout)
}

func (r *Resynchronizer) recoverCommon(ctx context.Context) error {
	call, cancel := r.callContext(ctx)
	_, err := r.api.UpdatesGetState(call)
	cancel()
	if err != nil {
		return err
	}
	cursor, err := r.store.LoadUpdateState(ctx)
	if err != nil {
		return err
	}
	for page := 0; page < r.limits.MaxDifferencePages; page++ {
		call, cancel = r.callContext(ctx)
		req := &tg.UpdatesGetDifferenceRequest{Pts: cursor.Pts, Qts: cursor.Qts, Date: cursor.Date}
		req.SetPtsLimit(r.limits.MaxResponseItems)
		req.SetPtsTotalLimit(r.limits.MaxResponseItems)
		req.SetQtsLimit(r.limits.MaxResponseItems)
		diff, callErr := r.api.UpdatesGetDifference(call, req)
		cancel()
		if callErr != nil {
			return callErr
		}
		if err := r.validateResponse(diff); err != nil {
			return err
		}
		if err := r.replay.Difference(ctx, diff); err != nil {
			return err
		}
		switch value := diff.(type) {
		case *tg.UpdatesDifferenceEmpty:
			cursor.Date = max(cursor.Date, value.Date)
			cursor.Seq = max(cursor.Seq, value.Seq)
			return r.finishCommon(ctx, cursor)
		case *tg.UpdatesDifference:
			cursor = monotonicState(cursor, updateState(value.State))
			return r.finishCommon(ctx, cursor)
		case *tg.UpdatesDifferenceSlice:
			next := monotonicState(cursor, updateState(value.IntermediateState))
			if next == cursor {
				return fmt.Errorf("%w: common difference made no progress", ErrRecoveryBound)
			}
			cursor = next
		case *tg.UpdatesDifferenceTooLong:
			if value.Pts < cursor.Pts {
				return fmt.Errorf("%w: common replacement pts regressed", ErrRecoveryBound)
			}
			cursor.Pts = value.Pts
			if err := r.backfillKnown(ctx); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported common difference %T", diff)
		}
	}
	return ErrRecoveryBound
}

func (r *Resynchronizer) finishCommon(ctx context.Context, state UpdateState) error {
	if err := r.store.SaveUpdateState(ctx, state); err != nil {
		return err
	}
	return r.store.MarkRecoveryReady(ctx, "common", int64(state.Pts))
}

func (r *Resynchronizer) recoverChannel(ctx context.Context, box string, channel ChannelState) error {
	cursor := channel.Pts
	input := &tg.InputChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash}
	for page := 0; page < r.limits.MaxDifferencePages; page++ {
		call, cancel := r.callContext(ctx)
		diff, err := r.api.UpdatesGetChannelDifference(call, &tg.UpdatesGetChannelDifferenceRequest{Channel: input, Filter: &tg.ChannelMessagesFilterEmpty{}, Pts: cursor, Limit: min(r.limits.HistoryPageSize, r.limits.MaxResponseItems)})
		cancel()
		if err != nil {
			if inaccessible(err) {
				return r.store.MarkRecoveryPermanent(ctx, box, "channel inaccessible: "+err.Error())
			}
			return err
		}
		if err := r.validateResponse(diff); err != nil {
			return err
		}
		switch value := diff.(type) {
		case *tg.UpdatesChannelDifferenceEmpty:
			if value.Pts < cursor {
				return fmt.Errorf("%w: channel pts regressed", ErrRecoveryBound)
			}
			cursor = value.Pts
			return r.finishChannel(ctx, box, channel.ID, cursor)
		case *tg.UpdatesChannelDifference:
			if err := r.replay.History(ctx, box, &tg.MessagesMessages{Messages: value.NewMessages, Chats: value.Chats, Users: value.Users}); err != nil {
				return err
			}
			if len(value.OtherUpdates) > 0 {
				if err := r.replay.Updates(ctx, box, &tg.Updates{Updates: value.OtherUpdates, Chats: value.Chats, Users: value.Users}); err != nil {
					return err
				}
			}
			if value.Pts <= cursor && !value.Final {
				return fmt.Errorf("%w: channel difference made no progress", ErrRecoveryBound)
			}
			cursor = value.Pts
			if value.Final {
				return r.finishChannel(ctx, box, channel.ID, cursor)
			}
		case *tg.UpdatesChannelDifferenceTooLong:
			dialog, ok := value.Dialog.(*tg.Dialog)
			if !ok {
				return fmt.Errorf("unsupported channel difference dialog %T", value.Dialog)
			}
			replacement, ok := dialog.GetPts()
			if !ok || replacement < cursor {
				return fmt.Errorf("%w: invalid channel replacement pts", ErrRecoveryBound)
			}
			if err := r.replay.History(ctx, box, &tg.MessagesMessages{Messages: value.Messages, Chats: value.Chats, Users: value.Users}); err != nil {
				return err
			}
			if err := r.backfill(ctx, box, &tg.InputPeerChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash}); err != nil {
				return err
			}
			return r.finishChannel(ctx, box, channel.ID, replacement)
		default:
			return fmt.Errorf("unsupported channel difference %T", diff)
		}
	}
	return ErrRecoveryBound
}

func (r *Resynchronizer) finishChannel(ctx context.Context, box string, id int64, pts int) error {
	if err := r.store.SaveChannelState(ctx, id, pts); err != nil {
		return err
	}
	return r.store.MarkRecoveryReady(ctx, box, int64(pts))
}

func (r *Resynchronizer) backfillKnown(ctx context.Context) error {
	offset := 0
	for page := 0; page < r.limits.MaxPeerPages; page++ {
		peers, more, err := r.store.RecoveryPeers(ctx, offset, r.limits.PeerPageSize)
		if err != nil {
			return err
		}
		for _, peer := range peers {
			var input tg.InputPeerClass
			switch peer.Kind {
			case "channel":
				if peer.AccessHash != 0 {
					input = &tg.InputPeerChannel{ChannelID: peer.ID, AccessHash: peer.AccessHash}
				}
			case "user":
				if peer.AccessHash != 0 {
					input = &tg.InputPeerUser{UserID: peer.ID, AccessHash: peer.AccessHash}
				}
			case "chat":
				input = &tg.InputPeerChat{ChatID: peer.ID}
			}
			if input == nil {
				return fmt.Errorf("known dialog %s:%d inaccessible", peer.Kind, peer.ID)
			}
			if err := r.backfill(ctx, fmt.Sprintf("%s:%d", peer.Kind, peer.ID), input); err != nil {
				return err
			}
		}
		offset += len(peers)
		if !more {
			return nil
		}
		if len(peers) == 0 {
			return fmt.Errorf("%w: peer traversal made no progress", ErrRecoveryBound)
		}
	}
	return fmt.Errorf("%w: known peer traversal truncated", ErrRecoveryBound)
}

func (r *Resynchronizer) backfill(ctx context.Context, box string, peer tg.InputPeerClass) error {
	offset := 0
	for page := 0; page < r.limits.MaxHistoryPages; page++ {
		effectiveLimit := min(r.limits.HistoryPageSize, r.limits.MaxResponseItems)
		call, cancel := r.callContext(ctx)
		result, err := r.api.MessagesGetHistory(call, &tg.MessagesGetHistoryRequest{Peer: peer, OffsetID: offset, Limit: effectiveLimit})
		cancel()
		if err != nil {
			return err
		}
		if err := r.validateResponse(result); err != nil {
			return err
		}
		if err := r.replay.History(ctx, box, result); err != nil {
			return err
		}
		messages := historyMessages(result)
		if len(messages) == 0 || len(messages) < effectiveLimit {
			return nil
		}
		oldest := oldestMessageID(messages)
		if oldest <= 0 || (offset != 0 && oldest >= offset) {
			return fmt.Errorf("%w: history made no progress", ErrRecoveryBound)
		}
		offset = oldest
	}
	return ErrRecoveryBound
}

func historyMessages(value tg.MessagesMessagesClass) []tg.MessageClass {
	if modified, ok := value.AsModified(); ok {
		return modified.GetMessages()
	}
	return nil
}
func oldestMessageID(messages []tg.MessageClass) int {
	oldest := 0
	for _, message := range messages {
		id := message.GetID()
		if id > 0 && (oldest == 0 || id < oldest) {
			oldest = id
		}
	}
	return oldest
}
func updateState(v tg.UpdatesState) UpdateState {
	return UpdateState{Pts: v.Pts, Qts: v.Qts, Date: v.Date, Seq: v.Seq}
}

func monotonicState(current, next UpdateState) UpdateState {
	return UpdateState{Pts: max(current.Pts, next.Pts), Qts: max(current.Qts, next.Qts), Date: max(current.Date, next.Date), Seq: max(current.Seq, next.Seq)}
}

func (r *Resynchronizer) validateResponse(value interface{ Encode(*bin.Buffer) error }) error {
	var buffer bin.Buffer
	if err := value.Encode(&buffer); err != nil {
		return err
	}
	if buffer.Len() > r.limits.MaxResponseBytes {
		return fmt.Errorf("%w: response bytes %d", ErrRecoveryBound, buffer.Len())
	}
	count := 0
	switch v := any(value).(type) {
	case *tg.UpdatesDifference:
		count = len(v.NewMessages) + len(v.NewEncryptedMessages) + len(v.OtherUpdates) + len(v.Chats) + len(v.Users)
	case *tg.UpdatesDifferenceSlice:
		count = len(v.NewMessages) + len(v.NewEncryptedMessages) + len(v.OtherUpdates) + len(v.Chats) + len(v.Users)
	case *tg.UpdatesChannelDifference:
		count = len(v.NewMessages) + len(v.OtherUpdates) + len(v.Chats) + len(v.Users)
	case *tg.UpdatesChannelDifferenceTooLong:
		count = len(v.Messages) + len(v.Chats) + len(v.Users)
	case *tg.MessagesMessages:
		count = len(v.Messages) + len(v.Chats) + len(v.Users) + len(v.Topics)
	case *tg.MessagesMessagesSlice:
		count = len(v.Messages) + len(v.Chats) + len(v.Users) + len(v.Topics)
	case *tg.MessagesChannelMessages:
		count = len(v.Messages) + len(v.Chats) + len(v.Users) + len(v.Topics)
	}
	if count > r.limits.MaxResponseItems {
		return fmt.Errorf("%w: response items %d", ErrRecoveryBound, count)
	}
	return nil
}
func channelBoxID(box string) (int64, bool) {
	if !strings.HasPrefix(box, "channel:") {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(box, "channel:"), 10, 64)
	return id, err == nil && id > 0
}
func inaccessible(err error) bool {
	return tgerr.Is(err, "CHANNEL_PRIVATE", "CHANNEL_INVALID", "CHAT_FORBIDDEN", "USER_BANNED_IN_CHANNEL") || tgerr.IsCode(err, 401, 403)
}
