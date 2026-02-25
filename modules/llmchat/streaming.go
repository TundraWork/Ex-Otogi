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
	defaultPlaceholderReply = "..."
	maxEditInterval         = 10 * time.Second
)

func (m *Module) streamProviderReply(
	ctx context.Context,
	target otogi.OutboundTarget,
	replyToMessageID string,
	provider otogi.LLMProvider,
	req otogi.LLMGenerateRequest,
) (err error) {
	if provider == nil {
		return fmt.Errorf("stream provider reply: nil provider")
	}
	if err := req.Validate(); err != nil {
		return fmt.Errorf("stream provider reply validate request: %w", err)
	}

	stream, err := provider.GenerateStream(ctx, req)
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

	placeholder, err := m.dispatcher.SendMessage(ctx, otogi.SendMessageRequest{
		Target:           target,
		Text:             defaultPlaceholderReply,
		ReplyToMessageID: replyToMessageID,
	})
	if err != nil {
		return fmt.Errorf("stream provider reply send placeholder: %w", err)
	}

	builder := strings.Builder{}
	pacer := newEditPacer(m.now())
	for {
		chunk, recvErr := stream.Recv(ctx)
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				break
			}
			return fmt.Errorf("stream provider reply receive chunk: %w", recvErr)
		}
		if chunk.Delta == "" {
			continue
		}
		builder.WriteString(chunk.Delta)

		now := m.now()
		if !pacer.ShouldAttemptEdit(now) {
			continue
		}
		editErr := m.dispatcher.EditMessage(ctx, otogi.EditMessageRequest{
			Target:    target,
			MessageID: placeholder.ID,
			Text:      builder.String(),
		})
		if editErr != nil {
			pacer.RecordEditFailure(now, editErr)
			continue
		}
		pacer.RecordEditSuccess(now)
	}

	finalText := strings.TrimSpace(builder.String())
	if finalText == "" {
		finalText = defaultPlaceholderReply
	}
	finalEditErr := m.dispatcher.EditMessage(ctx, otogi.EditMessageRequest{
		Target:    target,
		MessageID: placeholder.ID,
		Text:      finalText,
	})
	if finalEditErr == nil {
		return nil
	}

	_, sendErr := m.dispatcher.SendMessage(ctx, otogi.SendMessageRequest{
		Target:           target,
		Text:             finalText,
		ReplyToMessageID: replyToMessageID,
	})
	if sendErr != nil {
		return fmt.Errorf(
			"stream provider reply finalize with fallback: %w",
			errors.Join(finalEditErr, sendErr),
		)
	}

	return nil
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
	if p.consecutiveFailures >= 3 {
		return false
	}

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
