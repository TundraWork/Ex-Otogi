package llmchat

import (
	"context"
	"fmt"
	"sync"

	panel "ex-otogi/pkg/otogi/management"
)

type chatState string

const (
	chatStateAccepted       chatState = "accepted"
	chatStateContextReady   chatState = "context_ready"
	chatStateGenerating     chatState = "generating"
	chatStateExecutingTools chatState = "executing_tools"
	chatStateDelivering     chatState = "delivering"
	chatStateCompleted      chatState = "completed"
	chatStateFailed         chatState = "failed"
	chatStateCanceled       chatState = "canceled"
)

type chatLifecycleContextKey struct{}

type chatLifecycle struct {
	mu    sync.Mutex
	state chatState
	m     *Module
	agent Agent
}

func withChatLifecycle(ctx context.Context, lifecycle *chatLifecycle) context.Context {
	return context.WithValue(ctx, chatLifecycleContextKey{}, lifecycle)
}

func chatLifecycleFromContext(ctx context.Context) *chatLifecycle {
	if ctx == nil {
		return nil
	}
	lifecycle, ok := ctx.Value(chatLifecycleContextKey{}).(*chatLifecycle)
	if !ok {
		return nil
	}
	return lifecycle
}

func transitionChatState(ctx context.Context, next chatState) {
	if lifecycle := chatLifecycleFromContext(ctx); lifecycle != nil {
		lifecycle.transition(ctx, next)
	}
}

func (l *chatLifecycle) transition(ctx context.Context, next chatState) {
	if l == nil || next == "" {
		return
	}
	l.mu.Lock()
	previous := l.state
	if previous == next {
		l.mu.Unlock()
		return
	}
	l.state = next
	l.mu.Unlock()

	if l.m == nil || l.m.recorder == nil || terminalChatState(next) {
		return
	}
	_, err := l.m.recorder.RecordEvent(ctx, panel.Event{
		OccurredAt: l.m.now(), Category: panel.EventCategoryLLM, Kind: "chat.request.state_changed",
		Level: panel.EventLevelInfo, Module: l.m.Name(), Component: "orchestrator",
		Subject:     "chat request entered " + string(next),
		Description: panel.TruncateDescription(fmt.Sprintf("%s -> %s", previous, next)),
		Payload: panel.ChatRequestPayload{
			Agent: l.agent.Name, Provider: l.agent.Provider, Model: l.agent.Model,
			Outcome: "in_progress", State: string(next), PreviousState: string(previous),
			MaxAttempts: llmRetryMaxAttempts,
		},
	})
	if err != nil && l.m.logger != nil {
		l.m.logger.ErrorContext(ctx, "record chat request state transition", "error", err)
	}
}

func terminalChatState(state chatState) bool {
	return state == chatStateCompleted || state == chatStateFailed || state == chatStateCanceled
}
