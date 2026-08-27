package kernel

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

type invocation struct {
	messenger otogi.Messenger
	event     event
	moduleID  otogi.ModuleID
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	slots     map[string]struct{}
	closed    bool
	inflight  sync.WaitGroup
}

func newInvocation(ctx context.Context, messenger otogi.Messenger, admitted event, moduleID otogi.ModuleID) *invocation {
	invocationCtx, cancel := context.WithCancel(ctx)
	return &invocation{messenger: messenger, event: admitted, moduleID: moduleID, ctx: invocationCtx, cancel: cancel, slots: make(map[string]struct{})}
}

func (i *invocation) SendText(ctx context.Context, slot string, text otogi.Text) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if slot == "" || len(slot) > 64 || !utf8.ValidString(slot) || strings.TrimSpace(slot) != slot || text.String() == "" {
		return fmt.Errorf("send text: %w", otogi.ErrInvalid)
	}
	i.mu.Lock()
	if i.closed {
		i.mu.Unlock()
		return fmt.Errorf("send text after invocation close: %w", otogi.ErrInvalidState)
	}
	if _, exists := i.slots[slot]; exists {
		i.mu.Unlock()
		return fmt.Errorf("send text duplicate slot: %w", otogi.ErrInvalidState)
	}
	i.slots[slot] = struct{}{}
	i.inflight.Add(1)
	i.mu.Unlock()
	defer i.inflight.Done()

	key, err := messagingKey(i.event.ID(), i.moduleID, slot)
	if err != nil {
		return err
	}
	message, err := otogi.NewOutboundMessage(otogi.OutboundMessageParams{
		Tenant: i.event.Tenant(), Platform: i.event.Platform(), Account: i.event.Account(), Conversation: i.event.Conversation(),
		OriginatingEvent: i.event.ID(), OriginatingModule: i.moduleID, Text: text, IdempotencyKey: key,
	})
	if err != nil {
		return fmt.Errorf("send text message: %w", err)
	}
	stopCallerCancel := context.AfterFunc(ctx, i.cancel)
	defer stopCallerCancel()
	if err := i.messenger.Send(i.ctx, message); err != nil {
		return fmt.Errorf("send text: %w", err)
	}
	return nil
}

func (i *invocation) close() {
	i.mu.Lock()
	i.closed = true
	i.cancel()
	i.mu.Unlock()
	i.inflight.Wait()
}
