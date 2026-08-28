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
	"github.com/TundraWork/Ex-Otogi/internal/kernel"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// InboxStore is the durable claim boundary required by InboxDispatcher.
type InboxStore interface {
	ClaimNextAfter(context.Context, string, time.Time, time.Duration, string) (telegramstore.InboxRow, bool, error)
	ReleaseClaim(context.Context, string, string, time.Time, string) error
	CompleteClaim(context.Context, string, string, time.Time) error
	FailClaim(context.Context, string, string, string, time.Time) error
}

// EventDecoder decodes one bounded normalized inbox payload.
type EventDecoder func([]byte) (otogi.EventInput, error)

// InboxDispatcherConfig bounds claim ownership, retries, polling, and shutdown cleanup.
type InboxDispatcherConfig struct {
	Owner          string
	Lease          time.Duration
	AttemptTimeout time.Duration
	CleanupTimeout time.Duration
	PollInterval   time.Duration
	RetryBase      time.Duration
	RetryMax       time.Duration
	Now            func() time.Time
}

// InboxDispatcher is the single owner of durable Telegram inbox delivery.
type InboxDispatcher struct {
	store       InboxStore
	admitter    kernel.TrustedAdmitter
	decode      EventDecoder
	config      InboxDispatcherConfig
	mu          sync.Mutex
	active      bool
	claimPrefix string
	claimCount  uint64
	after       string
}

// NewInboxDispatcher constructs a bounded durable inbox dispatcher.
func NewInboxDispatcher(store InboxStore, admitter kernel.TrustedAdmitter, decode EventDecoder, config InboxDispatcherConfig) *InboxDispatcher {
	if store == nil || admitter == nil || decode == nil || config.Owner == "" || len(config.Owner) > 128 || config.Lease <= config.AttemptTimeout+config.CleanupTimeout || config.AttemptTimeout <= 0 || config.CleanupTimeout <= 0 || config.PollInterval <= 0 || config.RetryBase <= 0 || config.RetryMax < config.RetryBase || config.Now == nil {
		panic("telegram: invalid inbox dispatcher dependency or bound")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		panic("telegram: generate inbox claim owner: " + err.Error())
	}
	return &InboxDispatcher{store: store, admitter: admitter, decode: decode, config: config, claimPrefix: config.Owner + ":" + hex.EncodeToString(nonce[:])}
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
	row, ok, err := d.store.ClaimNextAfter(ctx, owner, now, d.config.Lease, after)
	if err != nil || !ok {
		return false, err
	}
	d.mu.Lock()
	d.after = row.Conversation
	d.mu.Unlock()

	input, err := d.decode(row.Payload)
	if err != nil {
		if permanentDispatchError(err) {
			return true, d.fail(row, owner, err)
		}
		return true, d.release(row, owner, err)
	}
	id := deterministicInboxEventID(row.ID)
	attempt, cancel := context.WithTimeout(ctx, d.config.AttemptTimeout)
	defer cancel()
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
