package llmchat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/platform"
)

const (
	defaultThinkingPlaceholder = "Thinking..."
	toolExecutionPlaceholder   = "Using tools..."
	maxThinkingPreviewRunes    = 180
	maxEditInterval            = 10 * time.Second
	maxToolIterations          = 5
)

func (m *Module) streamProviderReply(
	streamCtx context.Context,
	deliveryCtx context.Context,
	target platform.OutboundTarget,
	placeholderMessageID string,
	provider ai.LLMProvider,
	req ai.LLMGenerateRequest,
) error {
	return m.streamProviderReplyWithTools(
		streamCtx,
		deliveryCtx,
		target,
		placeholderMessageID,
		provider,
		req,
		nil,
	)
}

type streamIterationResult struct {
	thinkingChunks       int
	outputChunks         int
	answerText           string
	toolCalls            []ai.LLMToolCall
	lastDeliveredPayload editPayload
}

func (m *Module) streamProviderReplyWithTools(
	streamCtx context.Context,
	deliveryCtx context.Context,
	target platform.OutboundTarget,
	placeholderMessageID string,
	provider ai.LLMProvider,
	req ai.LLMGenerateRequest,
	toolRegistry *ToolRegistry,
) (err error) {
	if streamCtx == nil {
		return fmt.Errorf("stream provider reply: nil stream context")
	}
	if deliveryCtx == nil {
		return fmt.Errorf("stream provider reply: nil delivery context")
	}
	if strings.TrimSpace(placeholderMessageID) == "" {
		return fmt.Errorf("stream provider reply: empty placeholder message id")
	}
	if provider == nil {
		return fmt.Errorf("stream provider reply: nil provider")
	}
	if err := req.Validate(); err != nil {
		return fmt.Errorf("stream provider reply validate request: %w", err)
	}

	lastDeliveredPayload := plainEditPayload(defaultThinkingPlaceholder)
	for iteration := 0; iteration < maxToolIterations; iteration++ {
		m.debugToolIteration(streamCtx, iteration, len(req.Messages))

		result, iterationErr := m.streamProviderReplyIteration(
			streamCtx,
			target,
			placeholderMessageID,
			provider,
			req,
			lastDeliveredPayload,
		)
		if iterationErr != nil {
			return iterationErr
		}
		lastDeliveredPayload = result.lastDeliveredPayload

		finalText := strings.TrimSpace(result.answerText)
		if finalText != "" {
			finalPayload, parseErr := m.parseEditPayload(deliveryCtx, finalText)
			if parseErr != nil {
				m.warnMarkdownParseFallback(deliveryCtx, target, placeholderMessageID, parseErr)
			}
			if finalPayload.Equal(lastDeliveredPayload) {
				return nil
			}

			finalEditErr := m.retryEditMessage(deliveryCtx, platform.EditMessageRequest{
				Target:    target,
				MessageID: placeholderMessageID,
				Text:      finalPayload.Text,
				Entities:  finalPayload.Entities,
			})
			if finalEditErr != nil {
				return fmt.Errorf("stream provider reply finalize placeholder %s: %w", placeholderMessageID, finalEditErr)
			}

			return nil
		}

		if len(result.toolCalls) == 0 {
			if ctxErr := streamCtx.Err(); ctxErr != nil {
				return fmt.Errorf("stream provider reply canceled: %w", ctxErr)
			}

			return fmt.Errorf(
				"stream provider reply: no output text received (thinking_chunks=%d output_chunks=%d deadline_remaining=%s)",
				result.thinkingChunks,
				result.outputChunks,
				describeContextDeadlineRemaining(streamCtx),
			)
		}
		if toolRegistry == nil || !toolRegistry.HasTools() {
			return fmt.Errorf("stream provider reply: model requested tools but no tool registry is configured")
		}
		if iteration == maxToolIterations-1 {
			return fmt.Errorf("stream provider reply: exceeded max tool iterations (%d)", maxToolIterations)
		}

		lastDeliveredPayload = m.deliverPlaceholderText(
			streamCtx,
			target,
			placeholderMessageID,
			toolExecutionPlaceholder,
			lastDeliveredPayload,
		)

		m.debugToolCallsDetected(streamCtx, iteration, result.toolCalls)

		toolMessages, toolErr := m.executeToolCalls(streamCtx, toolRegistry, result.toolCalls)
		if toolErr != nil {
			return fmt.Errorf("stream provider reply execute tools: %w", toolErr)
		}

		req.Messages = append(req.Messages, ai.LLMMessage{
			Role:      ai.LLMMessageRoleAssistant,
			ToolCalls: append([]ai.LLMToolCall(nil), result.toolCalls...),
		})
		req.Messages = append(req.Messages, toolMessages...)
		if err := req.Validate(); err != nil {
			return fmt.Errorf("stream provider reply validate follow-up request: %w", err)
		}
	}

	return fmt.Errorf("stream provider reply: exceeded max tool iterations (%d)", maxToolIterations)
}

func (m *Module) streamProviderReplyIteration(
	streamCtx context.Context,
	target platform.OutboundTarget,
	placeholderMessageID string,
	provider ai.LLMProvider,
	req ai.LLMGenerateRequest,
	lastDeliveredPayload editPayload,
) (result streamIterationResult, err error) {
	stream, err := provider.GenerateStream(streamCtx, req)
	if err != nil {
		return streamIterationResult{}, fmt.Errorf("stream provider reply generate stream: %w", err)
	}
	defer func() {
		closeErr := stream.Close()
		if closeErr == nil {
			return
		}

		wrapped := fmt.Errorf("stream provider reply close stream: %w", closeErr)
		if err == nil {
			err = wrapped
			return
		}
		err = errors.Join(err, wrapped)
	}()

	thinkingBuilder := strings.Builder{}
	answerBuilder := strings.Builder{}
	pacer := newEditPacer(m.now())
	toolCalls := make(map[string]*accumulatedToolCall)
	toolCallOrder := make([]string, 0)
	result.lastDeliveredPayload = lastDeliveredPayload

	for {
		chunk, recvErr := stream.Recv(streamCtx)
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				break
			}

			return streamIterationResult{}, fmt.Errorf("stream provider reply receive chunk: %w", recvErr)
		}

		switch chunk.Kind.Normalize() {
		case ai.LLMGenerateChunkKindToolCall:
			if err := accumulateToolCallChunk(toolCalls, &toolCallOrder, chunk); err != nil {
				return streamIterationResult{}, fmt.Errorf("stream provider reply accumulate tool call: %w", err)
			}
			continue
		case ai.LLMGenerateChunkKindThinkingSummary:
			if chunk.Delta == "" {
				continue
			}
			result.thinkingChunks++
			thinkingBuilder.WriteString(chunk.Delta)
		default:
			if chunk.Delta == "" {
				continue
			}
			result.outputChunks++
			answerBuilder.WriteString(chunk.Delta)
		}

		nextText := answerBuilder.String()
		if answerBuilder.Len() == 0 {
			nextText = renderThinkingMessage(thinkingBuilder.String())
		}
		if nextText == "" {
			nextText = defaultThinkingPlaceholder
		}

		now := m.now()
		if !pacer.ShouldAttemptEdit(now) {
			continue
		}

		payload, parseErr := m.parseEditPayload(streamCtx, nextText)
		if parseErr != nil {
			m.warnMarkdownParseFallback(streamCtx, target, placeholderMessageID, parseErr)
		}
		if payload.Equal(result.lastDeliveredPayload) {
			continue
		}

		editErr := m.dispatcher.EditMessage(streamCtx, platform.EditMessageRequest{
			Target:    target,
			MessageID: placeholderMessageID,
			Text:      payload.Text,
			Entities:  payload.Entities,
		})
		if editErr != nil {
			pacer.RecordEditFailure(now, editErr)
			if pacer.consecutiveFailures == 1 {
				m.warnIntermediateEditFailure(streamCtx, target, placeholderMessageID, 1, editErr)
			}
			continue
		}
		pacer.RecordEditSuccess(now)
		result.lastDeliveredPayload = payload
	}

	finalToolCalls, err := finalizeAccumulatedToolCalls(toolCalls, toolCallOrder)
	if err != nil {
		return streamIterationResult{}, fmt.Errorf("stream provider reply finalize tool calls: %w", err)
	}

	result.answerText = answerBuilder.String()
	result.toolCalls = finalToolCalls

	return result, nil
}

func accumulateToolCallChunk(
	toolCalls map[string]*accumulatedToolCall,
	order *[]string,
	chunk ai.LLMGenerateChunk,
) error {
	id := strings.TrimSpace(chunk.ToolCallID)
	if id == "" {
		return fmt.Errorf("missing tool_call_id")
	}

	call, exists := toolCalls[id]
	if !exists {
		call = &accumulatedToolCall{ID: id}
		toolCalls[id] = call
		*order = append(*order, id)
	}
	if name := strings.TrimSpace(chunk.ToolCallName); name != "" {
		call.Name = name
	}
	if len(chunk.ToolCallThoughtSignature) > 0 && len(call.ThoughtSignature) == 0 {
		call.ThoughtSignature = append([]byte(nil), chunk.ToolCallThoughtSignature...)
	}

	argumentsDelta := chunk.ToolCallArguments
	if argumentsDelta == "" && chunk.Delta != "" {
		argumentsDelta = chunk.Delta
	}
	if argumentsDelta != "" {
		call.Arguments.WriteString(argumentsDelta)
	}

	return nil
}

func finalizeAccumulatedToolCalls(
	toolCalls map[string]*accumulatedToolCall,
	order []string,
) ([]ai.LLMToolCall, error) {
	if len(order) == 0 {
		return nil, nil
	}

	finalized := make([]ai.LLMToolCall, 0, len(order))
	for index, id := range order {
		call, exists := toolCalls[id]
		if !exists || call == nil {
			return nil, fmt.Errorf("tool_calls[%d]: missing accumulated state for id %s", index, id)
		}

		finalizedCall, err := call.ToolCall()
		if err != nil {
			return nil, fmt.Errorf("tool_calls[%d]: %w", index, err)
		}
		finalized = append(finalized, finalizedCall)
	}

	return finalized, nil
}

func (m *Module) deliverPlaceholderText(
	ctx context.Context,
	target platform.OutboundTarget,
	placeholderMessageID string,
	text string,
	lastDeliveredPayload editPayload,
) editPayload {
	payload, parseErr := m.parseEditPayload(ctx, text)
	if parseErr != nil {
		m.warnMarkdownParseFallback(ctx, target, placeholderMessageID, parseErr)
	}
	if payload.Equal(lastDeliveredPayload) {
		return lastDeliveredPayload
	}

	editErr := m.dispatcher.EditMessage(ctx, platform.EditMessageRequest{
		Target:    target,
		MessageID: placeholderMessageID,
		Text:      payload.Text,
		Entities:  payload.Entities,
	})
	if editErr != nil {
		m.warnIntermediateEditFailure(ctx, target, placeholderMessageID, 1, editErr)
		return lastDeliveredPayload
	}

	return payload
}

func (m *Module) executeToolCalls(
	ctx context.Context,
	toolRegistry *ToolRegistry,
	toolCalls []ai.LLMToolCall,
) ([]ai.LLMMessage, error) {
	results := make([]ai.LLMMessage, 0, len(toolCalls))
	for index, toolCall := range toolCalls {
		if err := toolCall.Validate(); err != nil {
			return nil, fmt.Errorf("tool_calls[%d]: %w", index, err)
		}

		m.debugToolExecuteStart(ctx, toolCall)
		execStart := m.now()

		result, err := toolRegistry.Execute(ctx, toolCall.Name, json.RawMessage(toolCall.Arguments))
		if err != nil {
			return nil, fmt.Errorf("tool_calls[%d] execute %s: %w", index, toolCall.Name, err)
		}
		if strings.TrimSpace(result) == "" {
			return nil, fmt.Errorf("tool_calls[%d] execute %s: empty result", index, toolCall.Name)
		}

		m.debugToolExecuteEnd(ctx, toolCall, m.now().Sub(execStart), len(result))

		results = append(results, ai.LLMMessage{
			Role:       ai.LLMMessageRoleTool,
			Content:    result,
			ToolCallID: toolCall.ID,
		})
	}

	return results, nil
}

func renderThinkingMessage(rawSummary string) string {
	summary := shapeThinkingSummary(rawSummary)
	if summary == "" {
		return defaultThinkingPlaceholder
	}

	return defaultThinkingPlaceholder + "\n\n" + summary
}

func shapeThinkingSummary(raw string) string {
	normalized := strings.Join(strings.Fields(raw), " ")
	if normalized == "" {
		return ""
	}

	return trimRunesWithEllipsis(normalized, maxThinkingPreviewRunes)
}

func trimRunesWithEllipsis(raw string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}

	runes := []rune(raw)
	if len(runes) <= maxRunes {
		return raw
	}
	if maxRunes <= 3 {
		return strings.Repeat(".", maxRunes)
	}

	return string(runes[:maxRunes-3]) + "..."
}

type editPacer struct {
	startedAt           time.Time
	nextEditAt          time.Time
	currentInterval     time.Duration
	consecutiveFailures int
}

func newEditPacer(startedAt time.Time) *editPacer {
	return &editPacer{
		startedAt:       startedAt,
		nextEditAt:      startedAt,
		currentInterval: 0,
	}
}

// ShouldAttemptEdit reports whether one intermediate edit should be attempted.
func (p *editPacer) ShouldAttemptEdit(now time.Time) bool {
	return !now.Before(p.nextEditAt)
}

// RecordEditSuccess updates pacing state after one successful edit.
func (p *editPacer) RecordEditSuccess(now time.Time) {
	base := p.baseInterval(now)
	if p.currentInterval <= 0 {
		p.currentInterval = base
	} else if p.currentInterval > base {
		p.currentInterval /= 2
		if p.currentInterval < base {
			p.currentInterval = base
		}
	} else {
		p.currentInterval = base
	}
	p.consecutiveFailures = 0
	p.nextEditAt = now.Add(p.currentInterval)
}

// RecordEditFailure updates pacing state after one failed edit.
func (p *editPacer) RecordEditFailure(now time.Time, err error) {
	p.consecutiveFailures++

	base := p.baseInterval(now)
	if p.currentInterval <= 0 {
		p.currentInterval = base
	}
	if isRateLimitError(err) {
		p.currentInterval = maxEditInterval
	} else {
		next := p.currentInterval * 2
		if next < base {
			next = base
		}
		if next > maxEditInterval {
			next = maxEditInterval
		}
		p.currentInterval = next
	}
	p.nextEditAt = now.Add(p.currentInterval)
}

// baseInterval returns the default interval based on elapsed stream time.
func (p *editPacer) baseInterval(now time.Time) time.Duration {
	elapsed := now.Sub(p.startedAt)
	switch {
	case elapsed < 4*time.Second:
		return 2 * time.Second
	case elapsed < 20*time.Second:
		return 3500 * time.Millisecond
	case elapsed < 60*time.Second:
		return 6 * time.Second
	default:
		return 10 * time.Second
	}
}

func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := platform.AsOutboundRateLimit(err); ok {
		return true
	}

	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "429") {
		return true
	}
	if strings.Contains(lower, "too many requests") {
		return true
	}
	if strings.Contains(lower, "flood") {
		return true
	}

	return false
}

func describeContextDeadlineRemaining(ctx context.Context) string {
	if ctx == nil {
		return "none"
	}

	deadline, hasDeadline := ctx.Deadline()
	if !hasDeadline {
		return "none"
	}

	remaining := time.Until(deadline)
	if remaining < 0 {
		remaining = 0
	}

	return remaining.Round(time.Millisecond).String()
}
