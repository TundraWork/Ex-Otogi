package updates

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/mapper"
	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

type downstreamStore interface {
	AppendJournal(context.Context, []byte, time.Time) (int64, error)
	CaptureBatch(context.Context, []telegramstore.Capture) (bool, error)
	FindLocators(context.Context, string, int64) ([]telegramstore.Locator, error)
	RecordGap(context.Context, string, int64, string) error
}

// EventEncoder serializes the repository's immutable rich input for durable
// inbox delivery. Encoding policy is injected so this integration does not
// create a second event wire format.
type EventEncoder func(otogi.EventInput) ([]byte, error)

// Downstream maps manager-ordered batches and atomically captures peers,
// locators, inbox/quarantine rows, and consistency positions.
type Downstream struct {
	store  downstreamStore
	mapper *mapper.Mapper
	encode EventEncoder
	health *Health
}

// NewDownstream constructs the gotd manager's downstream handler.
func NewDownstream(store downstreamStore, valueMapper *mapper.Mapper, encode EventEncoder, health *Health) *Downstream {
	if store == nil || valueMapper == nil || encode == nil || health == nil {
		panic("updates: nil downstream dependency")
	}
	return &Downstream{store: store, mapper: valueMapper, encode: encode, health: health}
}

var _ telegram.UpdateHandler = (*Downstream)(nil)

// Handle implements telegram.UpdateHandler. Gotd cursor writes occur separately
// after this method, so cursor+inbox atomicity is intentionally not claimed.
func (h *Downstream) Handle(ctx context.Context, update tg.UpdatesClass) error {
	journalID := JournalID(ctx)
	if journalID == 0 {
		payload, err := encodeUpdates(update)
		if err != nil {
			return h.fail(ctx, "common", 0, "encode manager difference", err)
		}
		journalID, err = h.store.AppendJournal(ctx, payload, time.Now().UTC())
		if err != nil {
			return h.fail(ctx, "common", 0, "journal manager difference", err)
		}
	}
	batch, err := normalizedBatch(update)
	if err != nil {
		return h.fail(ctx, "common", 0, "normalize manager batch", err)
	}
	if len(batch.Users) > telegramstore.MaxCaptureRows || len(batch.Chats) > telegramstore.MaxCaptureRows-len(batch.Users) {
		return h.fail(ctx, "common", 0, "peer batch too large", telegramstore.ErrInputTooLarge)
	}
	peers, err := encodePeers(batch.Users, batch.Chats)
	if err != nil {
		return h.fail(ctx, "common", 0, "encode peers", err)
	}
	if len(batch.Updates) > telegramstore.MaxCaptureRows {
		return h.fail(ctx, "common", 0, "capture batch too large", telegramstore.ErrInputTooLarge)
	}
	complete := journalComplete(ctx)
	captures := make([]telegramstore.Capture, 0, max(1, len(batch.Updates)))
	peersAttached := false
	for _, value := range batch.Updates {
		if placeholder, ok := shortSentPlaceholder(value); ok {
			payload, err := encodeTL(value)
			if err != nil {
				return h.fail(ctx, "common", 0, "encode short sent placeholder", err)
			}
			capture := telegramstore.Capture{JournalID: journalID, KeepJournalPending: !complete, Box: "common", Position: int64(placeholder.Pts), Quarantine: &telegramstore.QuarantineInput{SourceKey: fmt.Sprintf("telegram:short-sent:%d:%d", placeholder.Message.ID, placeholder.Pts), Kind: "telegram.short-sent.pts-only", Payload: payload}}
			if !peersAttached {
				capture.Peers = peers
				peersAttached = true
			}
			captures = append(captures, capture)
			continue
		}
		result, err := h.mapper.Map(value)
		if err != nil {
			return h.fail(ctx, "common", 0, "map update", err)
		}
		if result.Quarantine != nil && result.Box == "" {
			result.Box, result.Position = updateProgress(value)
		}
		capture := telegramstore.Capture{JournalID: journalID, KeepJournalPending: !complete, Box: result.Box, Position: int64(result.Position)}
		capture.Locators = make([]telegramstore.Locator, len(result.Locators))
		for i, locator := range result.Locators {
			capture.Locators[i] = telegramstore.Locator{Namespace: locator.Namespace, MessageID: int64(locator.MessageID), Conversation: locator.Conversation, Payload: locator.Payload}
		}
		if !peersAttached && len(result.Deletes) == 0 {
			capture.Peers = peers
			peersAttached = true
		}
		if len(captures)+len(result.Deletes) > telegramstore.MaxCaptureRows {
			return h.fail(ctx, capture.Box, capture.Position, "capture batch too large", telegramstore.ErrInputTooLarge)
		}
		if len(result.Deletes) != 0 {
			deletionCaptures, err := h.mapDeletions(ctx, journalID, !complete, capture.Box, capture.Position, telegramstore.MaxCaptureRows-len(captures), result.Deletes)
			if err != nil {
				return h.fail(ctx, capture.Box, capture.Position, "map deletion", err)
			}
			if !peersAttached && len(deletionCaptures) > 0 {
				deletionCaptures[0].Peers = peers
				peersAttached = true
			}
			captures = append(captures, deletionCaptures...)
			continue
		}
		if result.HasEvent {
			payload, err := h.encode(result.Event)
			if err != nil {
				return h.fail(ctx, capture.Box, capture.Position, "encode event", err)
			}
			capture.Inbox = &telegramstore.InboxInput{ID: result.SourceKey, SourceKey: result.SourceKey, Conversation: result.Event.Conversation().String(), Sequence: int64(result.Position), Payload: payload, AvailableAt: result.Event.IngestedAt()}
		}
		if result.Quarantine != nil {
			capture.Quarantine = &telegramstore.QuarantineInput{SourceKey: result.Quarantine.SourceKey, Kind: result.Quarantine.Kind, Payload: result.Quarantine.Payload}
		}
		captures = append(captures, capture)
	}
	if len(captures) == 0 {
		captures = append(captures, telegramstore.Capture{JournalID: journalID, KeepJournalPending: !complete, Peers: peers})
	}
	if _, err := h.store.CaptureBatch(ctx, captures); err != nil {
		box, position := captures[0].Box, captures[0].Position
		if box == "" {
			box = "common"
		}
		return h.fail(ctx, box, position, "capture normalized update", err)
	}
	return nil
}

type shortSent struct {
	Message *tg.MessageEmpty
	Pts     int
}

func shortSentPlaceholder(update tg.UpdateClass) (shortSent, bool) {
	value, ok := update.(*tg.UpdateNewMessage)
	if !ok {
		return shortSent{}, false
	}
	message, ok := value.Message.(*tg.MessageEmpty)
	if !ok {
		return shortSent{}, false
	}
	if _, hasPeer := message.GetPeerID(); hasPeer {
		return shortSent{}, false
	}
	return shortSent{Message: message, Pts: value.Pts}, true
}

type deletionTombstone struct {
	Namespace string `json:"namespace"`
	MessageID int    `json:"message_id"`
}

func (h *Downstream) mapDeletions(ctx context.Context, journalID int64, keepPending bool, box string, position int64, remaining int, deletions []mapper.DeleteRecord) ([]telegramstore.Capture, error) {
	captures := make([]telegramstore.Capture, 0, min(remaining, len(deletions)))
	for _, deletion := range deletions {
		conversations := []string{deletion.Conversation}
		if deletion.Unresolved {
			locators, err := h.store.FindLocators(ctx, deletion.Namespace, int64(deletion.MessageID))
			if err != nil {
				return nil, err
			}
			conversations = conversations[:0]
			for _, locator := range locators {
				conversations = append(conversations, locator.Conversation)
			}
		}
		if len(conversations) > remaining-len(captures) {
			return nil, telegramstore.ErrInputTooLarge
		}
		if len(conversations) == 0 {
			payload, err := json.Marshal(deletionTombstone{Namespace: deletion.Namespace, MessageID: deletion.MessageID})
			if err != nil {
				return nil, err
			}
			captures = append(captures, telegramstore.Capture{JournalID: journalID, KeepJournalPending: keepPending, Box: box, Position: position, Quarantine: &telegramstore.QuarantineInput{SourceKey: deletion.SourceKey, Kind: "telegram.delete.unresolved", Payload: payload}})
			continue
		}
		for _, conversation := range conversations {
			event, err := h.deletionEvent(deletion, conversation)
			if err != nil {
				return nil, err
			}
			payload, err := h.encode(event)
			if err != nil {
				return nil, err
			}
			sourceKey := deletion.SourceKey
			if len(conversations) > 1 {
				sourceKey += ":" + conversation
			}
			captures = append(captures, telegramstore.Capture{JournalID: journalID, KeepJournalPending: keepPending, Box: box, Position: position, Inbox: &telegramstore.InboxInput{ID: sourceKey, SourceKey: sourceKey, Conversation: conversation, Sequence: position, Payload: payload, AvailableAt: event.IngestedAt()}})
		}
	}
	return captures, nil
}

func (h *Downstream) deletionEvent(deletion mapper.DeleteRecord, conversation string) (otogi.EventInput, error) {
	return h.mapper.MapDeletion(deletion, conversation)
}
func (h *Downstream) fail(ctx context.Context, box string, position int64, reason string, cause error) error {
	recordErr := h.store.RecordGap(ctx, box, position, reason+": "+cause.Error())
	h.health.degrade(DegradedReason{Box: box, Position: position, Reason: reason, Err: recordErr})
	return fmt.Errorf("%s: %w", reason, cause)
}

func normalizedBatch(update tg.UpdatesClass) (*tg.Updates, error) {
	switch value := update.(type) {
	case *tg.Updates:
		return value, nil
	case *tg.UpdatesCombined:
		return &tg.Updates{Updates: value.Updates, Users: value.Users, Chats: value.Chats, Date: value.Date, Seq: value.Seq}, nil
	case *tg.UpdatesTooLong:
		return &tg.Updates{}, nil
	default:
		return nil, fmt.Errorf("unexpected post-manager update %T", update)
	}
}

func updateProgress(update tg.UpdateClass) (string, int) {
	if channelID, pts, _, ok, err := tg.IsChannelPtsUpdate(update); err == nil && ok {
		return fmt.Sprintf("channel:%d", channelID), max(0, pts)
	}
	if pts, _, ok := tg.IsPtsUpdate(update); ok {
		return "common", max(0, pts)
	}
	return "", 0
}

func encodePeers(users []tg.UserClass, chats []tg.ChatClass) ([]telegramstore.Peer, error) {
	peers := make([]telegramstore.Peer, 0, len(users)+len(chats))
	for _, value := range users {
		var id, hash int64
		switch user := value.(type) {
		case *tg.User:
			id, hash = user.ID, user.AccessHash
		case *tg.UserEmpty:
			id = user.ID
		default:
			return nil, fmt.Errorf("unsupported user %T", value)
		}
		payload, err := encodeTL(value)
		if err != nil {
			return nil, err
		}
		peers = append(peers, telegramstore.Peer{Kind: "user", ID: id, AccessHash: hash, Payload: payload})
	}
	for _, value := range chats {
		var kind string
		var id, hash int64
		switch chat := value.(type) {
		case *tg.Channel:
			kind, id, hash = "channel", chat.ID, chat.AccessHash
		case *tg.ChannelForbidden:
			kind, id, hash = "channel", chat.ID, chat.AccessHash
		case *tg.Chat:
			kind, id = "chat", chat.ID
		case *tg.ChatForbidden:
			kind, id = "chat", chat.ID
		case *tg.ChatEmpty:
			kind, id = "chat", chat.ID
		default:
			return nil, fmt.Errorf("unsupported chat %T", value)
		}
		payload, err := encodeTL(value)
		if err != nil {
			return nil, err
		}
		peers = append(peers, telegramstore.Peer{Kind: kind, ID: id, AccessHash: hash, Payload: payload})
	}
	return peers, nil
}

func encodeTL(value interface{ Encode(*bin.Buffer) error }) ([]byte, error) {
	var b bin.Buffer
	if err := value.Encode(&b); err != nil {
		return nil, err
	}
	return b.Copy(), nil
}
