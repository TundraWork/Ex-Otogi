package kernel

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// streamer is an effector whose streams show every other update.
type streamer struct {
	messenger
	mu      sync.Mutex
	opened  []otogi.Effect
	updates []string
	closed  []string
}

func (s *streamer) OpenStream(_ context.Context, e otogi.Effect) (otogi.DriverStream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opened = append(s.opened, e)
	return &driverStream{s: s}, nil
}

type driverStream struct{ s *streamer }

func (d *driverStream) Update(_ context.Context, msg otogi.Message) (bool, error) {
	d.s.mu.Lock()
	defer d.s.mu.Unlock()
	d.s.updates = append(d.s.updates, msg.PlainText())
	return len(d.s.updates)%2 == 1, nil
}

func (d *driverStream) Close(_ context.Context, msg otogi.Message) ([]otogi.EventRef, error) {
	d.s.mu.Lock()
	defer d.s.mu.Unlock()
	d.s.closed = append(d.s.closed, msg.PlainText())
	return []otogi.EventRef{{SourceEvent: "m1"}, {SourceEvent: "m2"}}, nil
}

func (s *streamer) state() (opened []otogi.Effect, updates, closed []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opened, s.updates, s.closed
}

func streamRecord(t *testing.T, rec *recorder, event otogi.EventID, kind string) map[string]any {
	t.Helper()
	r, ok := rec.kinds(event)[kind+" module:m"]
	if !ok {
		t.Fatalf("no %s record; have %v", kind, keys(rec.kinds(event)))
	}
	return r.Payload.(map[string]any)
}

func TestStreamThroughDriver(t *testing.T) {
	rec, s := &recorder{}, &streamer{}
	var refs []otogi.EventRef
	k := start(t, Config{Recorder: rec}, []otogi.Module{module("m", func(ctx context.Context, e otogi.Event, inv otogi.Invocation) error {
		stream := inv.Stream(ctx, "reply", e.Destination())
		for _, text := range []string{"a", "ab", "abc"} {
			if err := stream.Update(ctx, otogi.TextMessage(text)); err != nil {
				return err
			}
		}
		var err error
		refs, err = stream.Close(ctx, otogi.TextMessage("abcd"))
		return err
	})}, nil, s)

	o := await(t, submit(t, k, event("c", "hi")))
	opened, updates, closed := s.state()
	if len(opened) != 1 || opened[0].Kind != otogi.EffectStream || opened[0].Key != otogi.IdempotencyKeyFor(string(o.EventID), "m", "reply") || opened[0].To.Conversation != "c" {
		t.Fatalf("opened %+v", opened)
	}
	if len(updates) != 3 || len(closed) != 1 || closed[0] != "abcd" || len(refs) != 2 {
		t.Fatalf("updates %q, closed %q, refs %v", updates, closed, refs)
	}
	p := streamRecord(t, rec, o.EventID, "effect.applied")
	if p["updates"] != 3 || p["shown"] != 2 || p["abandoned"] != false || p["effect"].(otogi.Effect).Message.PlainText() != "abcd" {
		t.Fatalf("record payload %+v", p)
	}
}

func TestAbandonedStreamsAreClosedWithTheirLastContent(t *testing.T) {
	for name, end := range map[string]func(context.Context) error{
		"error":   func(context.Context) error { return errors.New("boom") },
		"panic":   func(context.Context) error { panic("oops") },
		"timeout": func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
	} {
		t.Run(name, func(t *testing.T) {
			rec, s := &recorder{}, &streamer{}
			m := module("m", func(ctx context.Context, e otogi.Event, inv otogi.Invocation) error {
				stream := inv.Stream(ctx, "reply", e.Destination())
				if err := stream.Update(ctx, otogi.TextMessage("partial")); err != nil {
					return err
				}
				return end(ctx)
			})
			m.descriptor.Timeout = 50 * time.Millisecond
			k := start(t, Config{Recorder: rec}, []otogi.Module{m}, nil, s)
			o := await(t, submit(t, k, event("c", "hi")))
			if _, _, closed := s.state(); len(closed) != 1 || closed[0] != "partial" {
				t.Fatalf("closed %q", closed)
			}
			if p := streamRecord(t, rec, o.EventID, "effect.applied"); p["abandoned"] != true {
				t.Fatalf("record payload %+v", p)
			}
		})
	}
}

func TestStreamWithoutStreamerSendsOnClose(t *testing.T) {
	m := &messenger{}
	k := start(t, Config{}, []otogi.Module{module("m", func(ctx context.Context, e otogi.Event, inv otogi.Invocation) error {
		stream := inv.Stream(ctx, "reply", e.Destination())
		if err := stream.Update(ctx, otogi.TextMessage("part")); err != nil {
			return err
		}
		refs, err := stream.Close(ctx, otogi.TextMessage("whole"))
		if err == nil && len(refs) != 1 {
			t.Errorf("refs %v", refs)
		}
		return err
	})}, nil, m)
	o := await(t, submit(t, k, event("c", "hi")))
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sent) != 1 || m.sent[0].Kind != otogi.EffectSend || m.sent[0].Message.PlainText() != "whole" || m.sent[0].Key != otogi.IdempotencyKeyFor(string(o.EventID), "m", "reply") {
		t.Fatalf("sent %+v", m.sent)
	}
}

func TestStreamSlotIsClaimed(t *testing.T) {
	rec := &recorder{}
	var updateErr, closeErr error
	k := start(t, Config{Recorder: rec}, []otogi.Module{module("m", func(ctx context.Context, e otogi.Event, inv otogi.Invocation) error {
		if _, err := inv.Send(ctx, "reply", otogi.TextMessage("first")); err != nil {
			return err
		}
		stream := inv.Stream(ctx, "reply", e.Destination())
		updateErr = stream.Update(ctx, otogi.TextMessage("x"))
		_, closeErr = stream.Close(ctx, otogi.TextMessage("x"))
		return nil
	})}, nil, &streamer{})
	o := await(t, submit(t, k, event("c", "hi")))
	if !errors.Is(updateErr, otogi.ErrInvalid) || !errors.Is(closeErr, otogi.ErrInvalid) {
		t.Fatalf("update %v, close %v", updateErr, closeErr)
	}
	streamRecord(t, rec, o.EventID, "effect.failed")
}
