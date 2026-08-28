package telegram

import (
	"context"
	"fmt"
	"time"

	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/gotd/td/tg"
)

// ReprocessStore provides bounded durable raw-source queues.
type ReprocessStore interface {
	// PendingJournal returns at most the requested number of unnormalized journal rows.
	PendingJournal(context.Context, int) ([]telegramstore.JournalRow, error)
	// PendingQuarantine returns at most the requested number of unreprocessed quarantine rows.
	PendingQuarantine(context.Context, int) ([]telegramstore.QuarantineRow, error)
	// MarkJournalNormalized completes one successfully replayed journal row.
	MarkJournalNormalized(context.Context, int64, time.Time) error
	// CompleteQuarantine completes one successfully replayed quarantine row.
	CompleteQuarantine(context.Context, int64) error
}

// RawReprocessor reprocesses one persisted raw payload.
type RawReprocessor func(context.Context, []byte) error

// DurableReprocessor replays bounded raw sources after mapper upgrades. A source
// is completed only after the handler durably captures its stable source key.
type DurableReprocessor struct {
	store               ReprocessStore
	journal, quarantine RawReprocessor
	now                 func() time.Time
}

// NewDurableReprocessor constructs a bounded raw-source reprocessor.
func NewDurableReprocessor(store ReprocessStore, journal, quarantine RawReprocessor, now func() time.Time) *DurableReprocessor {
	if store == nil || journal == nil || quarantine == nil || now == nil {
		panic("telegram: nil reprocessor dependency")
	}
	return &DurableReprocessor{store: store, journal: journal, quarantine: quarantine, now: now}
}

// Reprocess handles at most limit pending journal and quarantine rows.
func (r *DurableReprocessor) Reprocess(ctx context.Context, limit int) error {
	if limit <= 0 {
		return telegramstore.ErrInputTooLarge
	}
	journals, err := r.store.PendingJournal(ctx, limit)
	if err != nil {
		return err
	}
	remaining := limit
	for _, row := range journals {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.journal(ctx, row.Payload); err != nil {
			return fmt.Errorf("reprocess journal %d: %w", row.ID, err)
		}
		if err := r.store.MarkJournalNormalized(ctx, row.ID, r.now().UTC()); err != nil {
			return err
		}
		remaining--
	}
	if remaining == 0 {
		return nil
	}
	rows, err := r.store.PendingQuarantine(ctx, remaining)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.quarantine(ctx, row.Payload); err != nil {
			return fmt.Errorf("reprocess quarantine %d: %w", row.ID, err)
		}
		if err := r.store.CompleteQuarantine(ctx, row.ID); err != nil {
			return err
		}
	}
	return nil
}

var _ RecoveryReplay = (*DurableReprocessorReplay)(nil)

// DurableReprocessorReplay composes protocol recovery capture with raw-source reprocessing.
type DurableReprocessorReplay struct {
	// DifferenceFunc durably normalizes one common difference response.
	DifferenceFunc func(context.Context, tg.UpdatesDifferenceClass) error
	// UpdatesFunc durably normalizes one ordered update box.
	UpdatesFunc func(context.Context, string, tg.UpdatesClass) error
	// HistoryFunc durably normalizes one history page.
	HistoryFunc func(context.Context, string, tg.MessagesMessagesClass) error
	// Raw reprocesses pending raw journal and quarantine sources.
	Raw *DurableReprocessor
}

// Difference durably replays one common Telegram difference.
func (r *DurableReprocessorReplay) Difference(ctx context.Context, value tg.UpdatesDifferenceClass) error {
	return r.DifferenceFunc(ctx, value)
}

// Updates durably replays one ordered Telegram update box.
func (r *DurableReprocessorReplay) Updates(ctx context.Context, box string, value tg.UpdatesClass) error {
	return r.UpdatesFunc(ctx, box, value)
}

// History durably replays one Telegram history page.
func (r *DurableReprocessorReplay) History(ctx context.Context, box string, value tg.MessagesMessagesClass) error {
	return r.HistoryFunc(ctx, box, value)
}

// Reprocess replays bounded pending raw sources.
func (r *DurableReprocessorReplay) Reprocess(ctx context.Context, limit int) error {
	return r.Raw.Reprocess(ctx, limit)
}
