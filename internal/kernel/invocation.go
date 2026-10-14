package kernel

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// invocation is one unit of module work: an event delivery for now.
type invocation struct {
	kernel *Kernel
	event  otogi.Event
	origin string
	module otogi.ModuleID
	host   *host
	mu     sync.Mutex
	slots  map[string]bool
}

func (i *invocation) Host() otogi.Host { return i.host }

func (i *invocation) Send(ctx context.Context, slot string, msg otogi.Message) (otogi.EventRef, error) {
	if i.event.Conversation.ID == "" {
		return otogi.EventRef{}, fmt.Errorf("send: no event conversation to reply in; use SendTo: %w", otogi.ErrInvalid)
	}
	return i.SendTo(ctx, slot, i.event.Destination(), msg)
}

func (i *invocation) SendTo(ctx context.Context, slot string, to otogi.Destination, msg otogi.Message) (otogi.EventRef, error) {
	if to.Platform == "" || to.Account == "" || to.Conversation == "" {
		return otogi.EventRef{}, fmt.Errorf("send: incomplete destination %+v: %w", to, otogi.ErrInvalid)
	}
	if err := validContent(msg); err != nil {
		return otogi.EventRef{}, err
	}
	return i.apply(ctx, slot, otogi.Effect{Kind: otogi.EffectSend, To: to, Message: msg})
}

func (i *invocation) Edit(ctx context.Context, slot string, ref otogi.EventRef, msg otogi.Message) error {
	if err := validContent(msg); err != nil {
		return err
	}
	_, err := i.applyToRef(ctx, slot, otogi.Effect{Kind: otogi.EffectEdit, Ref: ref, Message: msg})
	return err
}

func (i *invocation) Delete(ctx context.Context, slot string, ref otogi.EventRef) error {
	_, err := i.applyToRef(ctx, slot, otogi.Effect{Kind: otogi.EffectDelete, Ref: ref})
	return err
}

func (i *invocation) React(ctx context.Context, slot string, ref otogi.EventRef, reaction string) error {
	_, err := i.applyToRef(ctx, slot, otogi.Effect{Kind: otogi.EffectReact, Ref: ref, Reaction: reaction})
	return err
}

func (i *invocation) FetchMedia(ctx context.Context, from otogi.Destination, src otogi.MediaSource) ([]byte, error) {
	fetcher, ok := i.kernel.effector.(otogi.MediaFetcher)
	if !ok {
		return nil, fmt.Errorf("fetch media: %w", otogi.ErrUnsupported)
	}
	return fetcher.FetchMedia(ctx, from, src)
}

func (i *invocation) applyToRef(ctx context.Context, slot string, e otogi.Effect) (otogi.EventRef, error) {
	if e.Ref.Platform == "" || e.Ref.Account == "" || e.Ref.SourceEvent == "" {
		return otogi.EventRef{}, fmt.Errorf("%s: reference does not identify a message: %w", e.Kind, otogi.ErrInvalid)
	}
	return i.apply(ctx, slot, e)
}

// apply claims slot, completes e with its idempotency key, hands it to the
// effector, and records the outcome. Reusing a slot within one unit of work
// is an error: both effects would share a key and the second would be lost.
func (i *invocation) apply(ctx context.Context, slot string, e otogi.Effect) (otogi.EventRef, error) {
	if slot == "" {
		return otogi.EventRef{}, fmt.Errorf("%s: empty slot: %w", e.Kind, otogi.ErrInvalid)
	}
	i.mu.Lock()
	if i.slots[slot] {
		i.mu.Unlock()
		return otogi.EventRef{}, fmt.Errorf("%s: slot %q already used: %w", e.Kind, slot, otogi.ErrInvalid)
	}
	if i.slots == nil {
		i.slots = make(map[string]bool)
	}
	i.slots[slot] = true
	i.mu.Unlock()

	e.Module, e.Origin, e.Key = i.module, i.origin, otogi.IdempotencyKeyFor(i.origin, i.module, slot)
	started := time.Now()
	ref, err := i.kernel.effector.Apply(ctx, e)
	component := "module:" + string(i.module)
	if err != nil {
		i.kernel.record(i.event, "effect.failed", component, "error", time.Since(started), map[string]any{"slot": slot, "effect": e, "error": err.Error()})
		return otogi.EventRef{}, err
	}
	i.kernel.record(i.event, "effect.applied", component, "ok", time.Since(started), map[string]any{"slot": slot, "effect": e, "ref": ref})
	return ref, nil
}

func validContent(msg otogi.Message) error {
	if len(msg.Parts) == 0 {
		return fmt.Errorf("empty message: %w", otogi.ErrInvalid)
	}
	return msg.Validate()
}
