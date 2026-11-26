package media

import (
	"cmp"
	"context"
	"fmt"
	"strings"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	contract "github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// An agent asks a question of its own about a piece of media it was shown
// described, with chat's examine_media: the models that describe media
// answer it from the media itself.
const (
	examinePrompt      = `You look at a picture sent in a group chat to answer a question about it from someone who cannot see it. Answer from what the picture shows, in %[1]s, in a few sentences. Where something cannot be made out, say so rather than guess. Copy any text you quote from it exactly as it is.`
	examineVideoPrompt = `You watch a video sent in a group chat to answer a question about it from someone who cannot watch it. Answer from what it shows and what is said in it, in %[1]s, in a few sentences. Where something cannot be made out, say so rather than guess. Copy anything you quote from it exactly as it is.`
)

// Examine answers question about md, seen in from; see contract.Describer.
// The original is md's bytes when it has them: those the asker fetched, or
// a kept sticker's picture. Otherwise it is the one the module keeps from
// that chat. The answer, or each model's failure, is recorded, with where
// the original came from.
func (m *Module) Examine(ctx context.Context, from otogi.Destination, md otogi.Media, question string) (string, error) {
	chat := chatKey(from)
	if !m.host.Allowed(describeList, chat) {
		return "", contract.ErrNotAllowed
	}
	if !describable(md) || md.ByteSize > m.largest {
		return "", contract.ErrNotDescribed
	}
	k, src := mediaKey(md)
	data, origin := md.Source.Inline, "fetched"
	if md.Source.Kind == otogi.MediaSourceInline {
		if _, ok, err := m.store.stickerByKey(ctx, k); err != nil {
			return "", err
		} else if ok {
			origin = "sticker"
		}
	} else {
		var mediaType string
		var err error
		if k, mediaType, data, err = m.keptOriginal(ctx, chat, k, src); err != nil {
			return "", err
		}
		origin, md.MediaType = "kept", mediaType
	}
	if int64(len(data)) > m.largest {
		return "", contract.ErrNotDescribed
	}
	p, err := m.prepare(md, data)
	if err != nil {
		return "", fmt.Errorf("%w: %w", contract.ErrNotDescribed, err)
	}
	return m.examine(ctx, chat, k, p, question, origin, len(data))
}

// Original returns md as the module keeps it from from; see
// contract.Describer.
func (m *Module) Original(ctx context.Context, from otogi.Destination, md otogi.Media) (otogi.Media, error) {
	chat := chatKey(from)
	if !m.host.Allowed(describeList, chat) {
		return otogi.Media{}, contract.ErrNotAllowed
	}
	k, src := mediaKey(md)
	_, mediaType, data, err := m.keptOriginal(ctx, chat, k, src)
	if err != nil {
		return otogi.Media{}, err
	}
	md.MediaType, md.ByteSize = mediaType, int64(len(data))
	md.Source = otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: data}
	return md, nil
}

// keptOriginal is the original the module keeps of the media k, or src when k is
// unknown, from chat: its key, type, and bytes. It fails with
// contract.ErrNotKept when there is none.
func (m *Module) keptOriginal(ctx context.Context, chat string, k key, src string) (key, string, []byte, error) {
	if k.Algorithm == "" {
		var ok bool
		var err error
		if k, ok, err = m.store.source(ctx, src); err != nil || !ok {
			return k, "", nil, cmp.Or(err, contract.ErrNotKept)
		}
	}
	mediaType, data, ok, err := m.store.original(ctx, k, chat)
	if err != nil || !ok {
		return k, "", nil, cmp.Or(err, contract.ErrNotKept)
	}
	return k, mediaType, data, nil
}

// examine tries the steps in order until one answers question about p,
// recording each failure as media.failed and the answer as media.examined.
// It returns otogi.ErrUnavailable when every step failed, and ctx's error
// once ctx ends.
func (m *Module) examine(ctx context.Context, from string, k key, p prepared, question, origin string, size int) (string, error) {
	system := fmt.Sprintf(examinePrompt, m.language)
	if p.video {
		system = fmt.Sprintf(examineVideoPrompt, m.language)
	}
	// The media as describing has it, an animation after its note on the
	// frames, then the question.
	parts := p.parts[1:]
	if p.frames > 0 {
		parts = p.parts
	}
	parts = append(parts[:len(parts):len(parts)], otogi.Part{ID: "q", Kind: otogi.PartText, Text: "Question: " + question})
	var ids []otogi.PartID
	for _, part := range parts {
		ids = append(ids, part.ID)
	}
	turn := llm.Turn{Role: llm.RoleUser, Content: otogi.Message{Roots: ids, Parts: parts}}
	for i, s := range m.steps {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		base := map[string]any{"from": from, "key": k.String(), "origin": origin, "question": question, "provider": s.Provider, "model": s.Model, "step": i + 1}
		started := m.now()
		resp, err := s.provider.Generate(ctx, llm.Request{Model: s.Model, System: system, Thinking: s.Thinking, Turns: []llm.Turn{turn}}, nil)
		took := m.now().Sub(started)
		answer := ""
		switch {
		case err != nil:
			base["error"] = err.Error()
		case resp.Finish != llm.FinishStop:
			base["finish"], base["reason"] = resp.Finish, resp.Reason
		case len(resp.Withheld) > 0:
			base["error"], base["withheld"] = "the model did not get the media", resp.Withheld
		default:
			if answer = strings.TrimSpace(resp.Markdown); answer == "" {
				base["error"] = "the answer is empty"
			}
		}
		if _, failed := base["error"]; failed || base["finish"] != nil {
			m.host.Record(ctx, otogi.Record{Kind: "media.failed", Outcome: "error", Duration: took, Payload: base})
			m.host.Logger().WarnContext(ctx, "media: a step failed to examine", "key", k.String(), "step", i+1, "model", s.Model, "error", base["error"], "finish", base["finish"])
			continue
		}
		base["answer"], base["input_tokens"], base["output_tokens"], base["bytes"] = answer, resp.Usage.Input, resp.Usage.Output, size
		if p.frames > 0 {
			base["frames"] = p.frames
		}
		m.host.Record(ctx, otogi.Record{Kind: "media.examined", Outcome: "ok", Duration: took, Payload: base})
		return answer, nil
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("media: every step failed: %w", otogi.ErrUnavailable)
}
