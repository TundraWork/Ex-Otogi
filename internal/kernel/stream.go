package kernel

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// abandonedStreamTimeout bounds closing the streams a unit of work left open.
const abandonedStreamTimeout = 10 * time.Second

// stream is the kernel's side of an otogi.MessageStream. It keeps the last
// content so a stream left open can be closed, sends the final content with
// an ordinary send when the destination cannot stream, and writes the one
// record of the stream when it closes.
type stream struct {
	inv     *invocation
	slot    string
	effect  otogi.Effect
	driver  otogi.DriverStream // nil: send on Close
	err     error              // why the stream could not start
	started time.Time

	mu       sync.Mutex
	last     otogi.Message
	updates  int
	shown    int
	closed   bool
	refs     []otogi.EventRef
	closeErr error
}

// streams are the streams of one unit of work, shared by the Invocations
// WithOrigin derives from it.
type streams struct {
	mu   sync.Mutex
	list []*stream
}

func (i *invocation) Stream(ctx context.Context, slot string, to otogi.Destination) otogi.MessageStream {
	s := &stream{inv: i, slot: slot, effect: otogi.Effect{Kind: otogi.EffectStream, To: to}, started: time.Now()}
	switch {
	case to.Platform == "" || to.Account == "" || to.Conversation == "":
		s.err = fmt.Errorf("stream: incomplete destination %+v: %w", to, otogi.ErrInvalid)
	default:
		s.err = i.claim(otogi.EffectStream, slot)
	}
	if s.err == nil {
		s.effect.Module, s.effect.Origin, s.effect.Key = i.module, i.originID, otogi.IdempotencyKeyFor(i.originID, i.module, slot)
		if streamer, ok := i.kernel.effector.(otogi.Streamer); ok {
			d, err := streamer.OpenStream(ctx, s.effect)
			switch {
			case errors.Is(err, otogi.ErrUnsupported): // send on Close
			case err != nil:
				s.err = fmt.Errorf("stream: open: %w", err)
			default:
				s.driver = d
			}
		}
	}
	i.streams.mu.Lock()
	i.streams.list = append(i.streams.list, s)
	i.streams.mu.Unlock()
	return s
}

func (s *stream) Update(ctx context.Context, msg otogi.Message) error {
	if err := validContent(msg); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.closed:
		return fmt.Errorf("stream %q: update after close: %w", s.slot, otogi.ErrInvalid)
	case s.err != nil:
		return s.err
	}
	s.last = msg
	s.updates++
	if s.driver == nil {
		return nil
	}
	shown, err := s.driver.Update(ctx, msg)
	if shown {
		s.shown++
	}
	return err
}

func (s *stream) Close(ctx context.Context, msg otogi.Message) ([]otogi.EventRef, error) {
	if len(msg.Parts) > 0 {
		if err := msg.Validate(); err != nil {
			return nil, err
		}
	}
	return s.close(ctx, msg, false)
}

// close finishes the stream once and records it; later calls return the
// first result.
func (s *stream) close(ctx context.Context, msg otogi.Message, abandoned bool) ([]otogi.EventRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return s.refs, s.closeErr
	}
	s.closed, s.last = true, msg
	switch {
	case s.err != nil:
		s.closeErr = s.err
	case s.driver != nil:
		s.refs, s.closeErr = s.driver.Close(ctx, msg)
	case len(msg.Parts) > 0:
		send := s.effect
		send.Kind, send.Message = otogi.EffectSend, msg
		var ref otogi.EventRef
		if ref, s.closeErr = s.inv.kernel.effector.Apply(ctx, send); s.closeErr == nil {
			s.refs = []otogi.EventRef{ref}
		}
	}
	effect := s.effect
	effect.Message = msg
	payload := map[string]any{"slot": s.slot, "effect": effect, "refs": s.refs, "updates": s.updates, "shown": s.shown, "abandoned": abandoned}
	kind, outcome := "effect.applied", "ok"
	if s.closeErr != nil {
		kind, outcome, payload["error"] = "effect.failed", "error", s.closeErr.Error()
	}
	s.inv.kernel.record(s.inv.event, kind, "module:"+string(s.inv.module), outcome, time.Since(s.started), payload)
	return s.refs, s.closeErr
}

// closeAbandoned closes the streams the unit of work left open, with their
// last content, so no message is left showing a partial reply.
func (i *invocation) closeAbandoned() {
	i.streams.mu.Lock()
	list := i.streams.list
	i.streams.mu.Unlock()
	for _, s := range list {
		s.mu.Lock()
		closed, last := s.closed, s.last
		s.mu.Unlock()
		if closed {
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					i.kernel.log.Error("closing an abandoned stream panicked", "module", i.module, "slot", s.slot, "panic", r)
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), abandonedStreamTimeout)
			defer cancel()
			if _, err := s.close(ctx, last, true); err != nil {
				i.kernel.log.Error("close abandoned stream", "module", i.module, "slot", s.slot, "error", err)
			}
		}()
	}
}
