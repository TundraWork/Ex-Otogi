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

func (i *invocation) Send(ctx context.Context, slot string, content otogi.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if slot == "" || len(slot) > 64 || !utf8.ValidString(slot) || strings.TrimSpace(slot) != slot {
		return fmt.Errorf("send message: %w", otogi.ErrInvalid)
	}
	key, err := messagingKey(i.event.ID(), i.moduleID, slot)
	if err != nil {
		return err
	}
	message, err := otogi.NewOutboundMessage(otogi.OutboundMessageParams{
		Tenant: i.event.Tenant(), Platform: i.event.Platform(), Account: i.event.Account(), Conversation: i.event.Conversation(),
		OriginatingEvent: i.event.ID(), OriginatingModule: i.moduleID, Message: content, IdempotencyKey: key,
	})
	if err != nil {
		return fmt.Errorf("send rich message: %w", err)
	}
	i.mu.Lock()
	if i.closed {
		i.mu.Unlock()
		return fmt.Errorf("send message after invocation close: %w", otogi.ErrInvalidState)
	}
	if _, exists := i.slots[slot]; exists {
		i.mu.Unlock()
		return fmt.Errorf("send message duplicate slot: %w", otogi.ErrInvalidState)
	}
	i.slots[slot] = struct{}{}
	i.inflight.Add(1)
	i.mu.Unlock()
	defer i.inflight.Done()

	sendCtx, cancelSend := context.WithCancel(ctx)
	stopInvocationCancel := context.AfterFunc(i.ctx, cancelSend)
	defer func() {
		stopInvocationCancel()
		cancelSend()
	}()
	if err := i.messenger.Send(sendCtx, message); err != nil {
		return fmt.Errorf("send message: %w", err)
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
