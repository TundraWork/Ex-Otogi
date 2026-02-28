package llmchat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"ex-otogi/pkg/otogi"
)

const (
	defaultThinkingPlaceholder = "Thinking..."
	maxThinkingPreviewRunes    = 180
	maxEditInterval            = 10 * time.Second
)

func (m *Module) streamProviderReply(
	streamCtx context.Context,
	deliveryCtx context.Context,
	target otogi.OutboundTarget,
	placeholderMessageID string,
	provider otogi.LLMProvider,
	req otogi.LLMGenerateRequest,
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

	stream, err := provider.GenerateStream(streamCtx, req)
	if err != nil {
		return fmt.Errorf("stream provider reply generate stream: %w", err)
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
	thinkingChunks := 0
	outputChunks := 0
	pacer := newEditPacer(m.now())
	lastDeliveredText := defaultThinkingPlaceholder
	for {
		chunk, recvErr := stream.Recv(streamCtx)
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				break
			}

			return fmt.Errorf("stream provider reply receive chunk: %w", recvErr)
		}
		if chunk.Delta == "" {
			continue
		}

		switch chunk.Kind.Normalize() {
		case otogi.LLMGenerateChunkKindThinkingSummary:
			thinkingChunks++
			thinkingBuilder.WriteString(chunk.Delta)
		default:
			outputChunks++
			answerBuilder.WriteString(chunk.Delta)
		}

		nextText := answerBuilder.String()
		if answerBuilder.Len() == 0 {
			nextText = renderThinkingMessage(thinkingBuilder.String())
		}
		if nextText == "" {
			nextText = defaultThinkingPlaceholder
		}
		if nextText == lastDeliveredText {
			continue
		}

		now := m.now()
		if !pacer.ShouldAttemptEdit(now) {
			continue
		}

		editErr := m.dispatcher.EditMessage(streamCtx, otogi.EditMessageRequest{
			Target:    target,
			MessageID: placeholderMessageID,
			Text:      nextText,
		})
		if editErr != nil {
			pacer.RecordEditFailure(now, editErr)
			if pacer.consecutiveFailures == 1 {
				m.warnIntermediateEditFailure(streamCtx, target, placeholderMessageID, 1, editErr)
			}
			continue
		}
		pacer.RecordEditSuccess(now)
		lastDeliveredText = nextText
	}

	finalText := strings.TrimSpace(answerBuilder.String())
	if finalText == "" {
		if ctxErr := streamCtx.Err(); ctxErr != nil {
			return fmt.Errorf("stream provider reply canceled: %w", ctxErr)
		}

		return fmt.Errorf(
			"stream provider reply: no output text received (thinking_chunks=%d output_chunks=%d deadline_remaining=%s)",
			thinkingChunks,
			outputChunks,
			describeContextDeadlineRemaining(streamCtx),
		)
	}
	if finalText == lastDeliveredText {
		return nil
	}

	finalEditErr := m.retryEditMessage(deliveryCtx, otogi.EditMessageRequest{
		Target:    target,
		MessageID: placeholderMessageID,
		Text:      finalText,
	})
	if finalEditErr != nil {
		return fmt.Errorf("stream provider reply finalize placeholder %s: %w", placeholderMessageID, finalEditErr)
	}

	return nil
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
	if _, ok := otogi.AsOutboundRateLimit(err); ok {
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
