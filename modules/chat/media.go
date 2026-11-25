package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	mediasvc "github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// describeWait is the longest a message waits for the media module to
// describe its media, in all, however many pieces it has: the judgment and
// as_text wait until the same deadline. eku waits as long on dsh, where a
// picture is described in 2 to 6 seconds; it holds the message's worker
// that long at most, well within the reply's timeout.
const describeWait = 10 * time.Second

// describedText is how much of a description's text, in characters, is kept
// where it shares a line with other messages: the transcript, a quote, the
// judgment. as_text gives it all.
const describedText = 300

// describes reports whether any agent reads the media module's descriptions.
func (m *Module) describes() bool {
	for _, p := range m.profiles {
		if p.Media != nil {
			return true
		}
	}
	return false
}

// mediaText is how media reads in the transcript, a quote, and the
// judgment: as the media module described it, when described is set and a
// description is kept, or else by its kind. It does not wait.
func (m *Module) mediaText(ctx context.Context, from otogi.Destination, media []otogi.Media, described bool) string {
	words := make([]string, 0, len(media))
	for _, md := range media {
		if described && m.describer != nil {
			d, ok, err := m.describer.Lookup(ctx, from, md)
			if err != nil {
				m.host.Logger().WarnContext(ctx, "chat: reading a media description", "error", err)
			}
			if ok {
				words = append(words, descriptionText(md, d, describedText))
				continue
			}
		}
		words = append(words, "["+mediaWord(md)+"]")
	}
	return strings.Join(words, " ")
}

// awaitDescriptions waits, until until at most, for the media module to
// describe the media of msg, seen at from, so mediaText finds them.
func (m *Module) awaitDescriptions(ctx context.Context, from otogi.Destination, msg otogi.Message, until time.Time) {
	for _, part := range msg.Parts {
		if part.Media != nil {
			_, _ = m.awaitDescription(ctx, from, *part.Media, until)
		}
	}
}

// awaitDescription returns the description of md, seen at from, waiting
// until until at most.
func (m *Module) awaitDescription(ctx context.Context, from otogi.Destination, md otogi.Media, until time.Time) (mediasvc.Description, error) {
	ctx, cancel := context.WithDeadline(ctx, until)
	defer cancel()
	return m.describer.Describe(ctx, from, md)
}

// describedPart is media given to the model as its description, waiting
// until until at most; the media itself is not sent. Without a description,
// it is named by its kind, and why is recorded as chat.media_skipped.
func (m *Module) describedPart(ctx context.Context, from otogi.Destination, md otogi.Media, until time.Time) otogi.Message {
	d, err := m.awaitDescription(ctx, from, md, until)
	if err != nil {
		return m.undescribed(ctx, from, md, err)
	}
	return otogi.TextMessage(descriptionText(md, d, 0))
}

// keptPart is an older message's media given to the model as its kept
// description, without waiting: the media module describes media as it
// arrives and does not come back to it, so a wait would only run out. Without
// a description, it is named by its kind, and why is recorded.
func (m *Module) keptPart(ctx context.Context, from otogi.Destination, md otogi.Media) otogi.Message {
	d, ok, err := m.describer.Lookup(ctx, from, md)
	switch {
	case err != nil:
		return m.undescribed(ctx, from, md, err)
	case !ok:
		return m.undescribed(ctx, from, md, errNotKept)
	}
	return otogi.TextMessage(descriptionText(md, d, 0))
}

// errNotKept reports quoted media the media module has no description of.
var errNotKept = errors.New("no description kept")

// undescribed names media by its kind, recording why it went without a
// description as chat.media_skipped.
func (m *Module) undescribed(ctx context.Context, from otogi.Destination, md otogi.Media, err error) otogi.Message {
	word := mediaWord(md)
	m.host.Logger().WarnContext(ctx, "chat: media not described", "media", word, "error", err)
	m.host.Record(ctx, otogi.Record{Kind: "chat.media_skipped", Outcome: "skipped", Payload: map[string]any{
		"reason": "not described", "from": from, "source": md.Source.Kind, "url": md.Source.URL, "locator": md.Source.Locator,
		"bytes": md.ByteSize, "error": err.Error()}})
	return otogi.TextMessage("[" + word + "]")
}

// descriptionText writes a description in brackets: [sticker: a cat eating
// watermelon], [screenshot: a post Text: "..."], [video 0:12: a song Said:
// "..."]; a kept sticker as its number, [sticker 3: ...], whatever its kind,
// so an agent can send it back. With limit above 0, the text goes on one
// line, cut to limit characters.
func descriptionText(md otogi.Media, d mediasvc.Description, limit int) string {
	head, label := string(d.Kind), "Text"
	switch {
	case d.Sticker != 0:
		head = fmt.Sprintf("sticker %d", d.Sticker)
	case d.Kind == mediasvc.KindVideo:
		label = "Said"
		if md.Duration > 0 {
			s := int(md.Duration.Round(time.Second) / time.Second)
			head += fmt.Sprintf(" %d:%02d", s/60, s%60)
		}
	}
	out := "[" + head + ": " + d.Summary
	if text := d.Text; text != "" {
		if limit > 0 {
			text = strings.Join(strings.Fields(text), " ")
			if r := []rune(text); len(r) > limit {
				text = string(r[:limit]) + "…"
			}
		}
		out += " " + label + ": \"" + text + "\""
	}
	return out + "]"
}
