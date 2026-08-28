package telegram

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// InboxStore is the durable claim boundary required by InboxDispatcher.
type InboxStore interface {
	// ClaimNextAfter atomically leases the next eligible conversation head.
	ClaimNextAfter(context.Context, string, time.Time, time.Duration, string) (telegramstore.InboxRow, bool, error)
	// ReleaseClaim returns an owned row to pending with its next eligibility time.
	ReleaseClaim(context.Context, string, string, time.Time, string) error
	// CompleteClaim marks an owned row terminal after exact kernel acknowledgement.
	CompleteClaim(context.Context, string, string, time.Time) error
	// FailClaim marks an owned row permanently failed with a bounded reason.
	FailClaim(context.Context, string, string, string, time.Time) error
}

// EventDecoder decodes one bounded normalized inbox payload and observes attempt cancellation.
type EventDecoder func(context.Context, []byte) (otogi.EventInput, error)

// TrustedAdmitter is the driver-facing boundary for deterministic event admission and volatile completion observation.
type TrustedAdmitter interface {
	// SubmitTrusted admits one event with a trusted deterministic runtime ID.
	SubmitTrusted(context.Context, otogi.EventID, otogi.EventInput) error
	// SubscribeCompletion observes volatile completion for one runtime event attempt.
	SubscribeCompletion(context.Context, otogi.EventID) (<-chan otogi.EventID, error)
}

// InboxDispatcherConfig bounds claim ownership, retries, polling, and shutdown cleanup.
type InboxDispatcherConfig struct {
	// Owner identifies this dispatcher instance in durable claims.
	Owner string
	// Lease bounds exclusive ownership across claim, attempt, and cleanup.
	Lease time.Duration
	// ClaimTimeout bounds acquisition latency deducted from the lease.
	ClaimTimeout time.Duration
	// AttemptTimeout bounds decode, admission, and acknowledgement together.
	AttemptTimeout time.Duration
	// CleanupTimeout bounds durable release, completion, or failure updates.
	CleanupTimeout time.Duration
	// PollInterval bounds idle waiting before another claim attempt.
	PollInterval time.Duration
	// RetryBase is the initial retry delay.
	RetryBase time.Duration
	// RetryMax caps exponential retry delay.
	RetryMax time.Duration
	// Now supplies UTC-compatible wall time for durable schedules.
	Now func() time.Time
}

// InboxDispatcher is the single owner of durable Telegram inbox delivery.
type InboxDispatcher struct {
	store       InboxStore
	admitter    TrustedAdmitter
	decode      EventDecoder
	config      InboxDispatcherConfig
	mu          sync.Mutex
	active      bool
	claimPrefix string
	claimCount  uint64
	after       string
}

// NewInboxDispatcher constructs a bounded durable inbox dispatcher.
func NewInboxDispatcher(store InboxStore, admitter TrustedAdmitter, decode EventDecoder, config InboxDispatcherConfig) *InboxDispatcher {
	if store == nil || admitter == nil || decode == nil || config.Owner == "" || len(config.Owner) > 128 || !validDispatchDurations(config) || config.PollInterval <= 0 || config.RetryBase <= 0 || config.RetryMax < config.RetryBase || config.Now == nil {
		panic("telegram: invalid inbox dispatcher dependency or bound")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		panic("telegram: generate inbox claim owner: " + err.Error())
	}
	return &InboxDispatcher{store: store, admitter: admitter, decode: decode, config: config, claimPrefix: config.Owner + ":" + hex.EncodeToString(nonce[:])}
}

func validDispatchDurations(config InboxDispatcherConfig) bool {
	if config.Lease <= 0 || config.ClaimTimeout <= 0 || config.AttemptTimeout <= 0 || config.CleanupTimeout <= 0 || config.ClaimTimeout >= config.Lease {
		return false
	}
	remaining := config.Lease - config.ClaimTimeout
	if config.AttemptTimeout >= remaining {
		return false
	}
	return config.CleanupTimeout < remaining-config.AttemptTimeout
}

// Run claims and dispatches eligible rows until ctx is canceled. Only one Run may own the dispatcher.
func (d *InboxDispatcher) Run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	if d.active {
		d.mu.Unlock()
		return fmt.Errorf("run inbox dispatcher: %w", otogi.ErrInvalidState)
	}
	d.active = true
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.active = false
		d.mu.Unlock()
	}()

	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
		worked, err := d.dispatchOne(ctx)
		if err != nil {
			return err
		}
		if worked {
			timer.Reset(0)
		} else {
			timer.Reset(d.config.PollInterval)
		}
	}
}

func (d *InboxDispatcher) running() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.active
}

func (d *InboxDispatcher) dispatchOne(ctx context.Context) (bool, error) {
	now := d.config.Now().UTC()
	d.mu.Lock()
	d.claimCount++
	owner := fmt.Sprintf("%s:%x", d.claimPrefix, d.claimCount)
	after := d.after
	d.mu.Unlock()
	claimCtx, cancelClaim := context.WithTimeout(ctx, d.config.ClaimTimeout)
	row, ok, err := d.store.ClaimNextAfter(claimCtx, owner, now, d.config.Lease, after)
	cancelClaim()
	if err != nil || !ok {
		return false, err
	}
	d.mu.Lock()
	d.after = row.Conversation
	d.mu.Unlock()

	attempt, cancel := context.WithTimeout(ctx, d.config.AttemptTimeout)
	defer cancel()
	input, err := d.decode(attempt, row.Payload)
	if err != nil {
		if permanentDispatchError(err) {
			return true, d.fail(row, owner, err)
		}
		return true, d.release(row, owner, err)
	}
	id := deterministicInboxEventID(row.ID)
	completion, err := d.admitter.SubscribeCompletion(attempt, id)
	if err != nil {
		if permanentDispatchError(err) {
			return true, d.fail(row, owner, err)
		}
		return true, d.release(row, owner, err)
	}
	if err = d.admitter.SubmitTrusted(attempt, id, input); err != nil {
		cancel()
		if permanentDispatchError(err) {
			return true, d.fail(row, owner, err)
		}
		return true, d.release(row, owner, err)
	}
	select {
	case completed, open := <-completion:
		if !open || completed != id {
			return true, d.release(row, owner, otogi.ErrUnavailable)
		}
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), d.config.CleanupTimeout)
		defer stop()
		return true, d.store.CompleteClaim(cleanup, row.ID, owner, d.config.Now().UTC())
	case <-attempt.Done():
		return true, d.release(row, owner, attempt.Err())
	}
}

func deterministicInboxEventID(inboxID string) otogi.EventID {
	digest := sha256.Sum256([]byte("telegram-inbox\x00" + inboxID))
	id, err := otogi.NewEventID(fmt.Sprintf("evt_tg_%x", digest))
	if err != nil {
		panic("telegram: deterministic inbox event ID is invalid")
	}
	return id
}

func permanentDispatchError(err error) bool {
	return errors.Is(err, otogi.ErrInvalid) || errors.Is(err, otogi.ErrUnsupported)
}

func (d *InboxDispatcher) release(row telegramstore.InboxRow, owner string, cause error) error {
	cleanup, cancel := context.WithTimeout(context.Background(), d.config.CleanupTimeout)
	defer cancel()
	reason := dispatchReason(cause)
	return d.store.ReleaseClaim(cleanup, row.ID, owner, d.config.Now().UTC().Add(d.retryDelay(row.Attempts)), reason)
}

func (d *InboxDispatcher) fail(row telegramstore.InboxRow, owner string, cause error) error {
	cleanup, cancel := context.WithTimeout(context.Background(), d.config.CleanupTimeout)
	defer cancel()
	return d.store.FailClaim(cleanup, row.ID, owner, dispatchReason(cause), d.config.Now().UTC())
}

func (d *InboxDispatcher) retryDelay(attempts int) time.Duration {
	delay := d.config.RetryBase
	for remaining := max(0, attempts-1); remaining > 0; remaining-- {
		if delay >= d.config.RetryMax || delay > d.config.RetryMax/2 {
			return d.config.RetryMax
		}
		delay *= 2
	}
	return min(delay, d.config.RetryMax)
}

func dispatchReason(err error) string {
	switch {
	case errors.Is(err, otogi.ErrCapacity):
		return "kernel capacity"
	case errors.Is(err, otogi.ErrUnavailable), errors.Is(err, otogi.ErrInvalidState):
		return "kernel unavailable"
	case errors.Is(err, context.DeadlineExceeded):
		return "completion timeout"
	case errors.Is(err, context.Canceled):
		return "dispatcher canceled"
	case errors.Is(err, otogi.ErrUnsupported):
		return "unsupported event"
	case errors.Is(err, otogi.ErrInvalid):
		return "invalid event"
	default:
		return "dispatch unavailable"
	}
}
