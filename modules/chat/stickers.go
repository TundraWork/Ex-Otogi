package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	mediasvc "github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// stickerTool is the function an agent sends a sticker with. It is chat's
// own, not a module's tool: chat sends the sticker, after the reply, so no
// tool sends messages around chat's count of a reply's messages.
const stickerTool = "send_sticker"

// stickerSlot is the effect slot a reply's sticker is sent in: a replayed
// event sends it once.
const stickerSlot = "sticker"

// sendsStickers reports whether any agent sends stickers.
func (m *Module) sendsStickers() bool {
	for _, p := range m.profiles {
		if p.Media != nil && (len(p.Media.Stickers) > 0 || len(p.Media.Packs) > 0) {
			return true
		}
	}
	return false
}

// stickerPick is the stickers shown to an agent in one reply, every kept
// sticker it may send by number, and the one it picked.
type stickerPick struct {
	list   []mediasvc.Sticker // the agent's, in its order: the list it is shown
	kept   []mediasvc.Sticker // every kept sticker: what it may send by number
	picked *mediasvc.Sticker
	listed bool // whether the picked sticker is on the agent's list
	hidden int  // how many more the agent is given than its list shows
}

// stickersFor is the stickers p is shown in this reply, or nil when it is
// given none or none of them is kept any more: then the reply has no list
// and no send_sticker. Its stickers come first, in its order, then its
// packs', in its order and each pack's by number; each once, those no
// longer kept skipped, and those past its max_stickers left out, counted
// as hidden. A failure to list the kept stickers is logged, and the reply
// goes on without.
func (m *Module) stickersFor(ctx context.Context, p *profile) *stickerPick {
	if p.Media == nil || (len(p.Media.Stickers) == 0 && len(p.Media.Packs) == 0) {
		return nil
	}
	kept, err := m.stickers.List(ctx)
	if err != nil {
		m.host.Logger().WarnContext(ctx, "chat: listing stickers", "profile", p.Name, "error", err)
		return nil
	}
	byID := make(map[int]mediasvc.Sticker, len(kept))
	for _, st := range kept {
		byID[st.ID] = st
	}
	var list []mediasvc.Sticker
	seen := map[int]bool{}
	add := func(st mediasvc.Sticker) {
		if !seen[st.ID] {
			seen[st.ID] = true
			list = append(list, st)
		}
	}
	for _, n := range p.Media.Stickers {
		if st, ok := byID[n]; ok {
			add(st)
		}
	}
	for _, pack := range p.Media.Packs {
		for _, st := range kept { // by number
			if slices.Contains(st.Packs, pack) {
				add(st)
			}
		}
	}
	if len(list) == 0 {
		return nil
	}
	pick := &stickerPick{list: list, kept: kept}
	if len(list) > p.maxStickers {
		pick.list, pick.hidden = list[:p.maxStickers], len(list)-p.maxStickers
	}
	return pick
}

// text is the list as the system prompt has it, one sticker a line, by
// number.
func (s *stickerPick) text() string {
	lines := []string{"Stickers you can send with " + stickerTool + ":"}
	for _, st := range s.list {
		lines = append(lines, fmt.Sprintf("%d. %s", st.ID, strings.Join(strings.Fields(st.Text), " ")))
	}
	return strings.Join(lines, "\n")
}

// tool is send_sticker: it only notes the sticker picked, and ends the
// reply. It takes any kept sticker's number, listed or not, so a sticker
// someone names can be sent. A second pick in the same reply is refused
// without an error, so the reply still ends; a number no sticker has is an
// error the model sees, and it may answer again.
func (s *stickerPick) tool() llm.Tool {
	return llm.NewEndingTool(llm.Function{
		Name:        stickerTool,
		Description: "Send a sticker by its number: one from your list of stickers, one shown as [sticker N: ...] in the chat, or one someone names. It goes as a message of its own after what you wrote. This ends your reply: write anything you want to say before calling it. At most one per reply.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"number":{"type":"integer","description":"the sticker's number"}},"required":["number"]}`),
	}, func(_ context.Context, _ otogi.Event, args json.RawMessage) (any, error) {
		var in struct {
			Number int `json:"number"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("want {\"number\": <a sticker's number>}: %w", err)
		}
		if s.picked != nil {
			return map[string]any{"sent": false, "reason": "one sticker per reply"}, nil
		}
		for _, st := range s.kept {
			if st.ID == in.Number {
				s.picked = &st
				s.listed = slices.ContainsFunc(s.list, func(l mediasvc.Sticker) bool { return l.ID == st.ID })
				return map[string]bool{"sent": true}, nil
			}
		}
		return nil, fmt.Errorf("there is no sticker %d", in.Number)
	})
}

// sentSticker is how chat.reply records a picked sticker; Listed is whether
// it is on the agent's list, and Ref the sent message's source ID, empty when
// it was not sent.
type sentSticker struct {
	ID     int    `json:"id"`
	Text   string `json:"text"`
	Listed bool   `json:"listed"`
	Ref    string `json:"ref,omitempty"`
}

// sendSticker sends st as a message of its own, without the agent's label,
// replying to e when the reply quotes.
func (m *Module) sendSticker(ctx context.Context, e otogi.Event, inv otogi.Invocation, st mediasvc.Sticker, quote bool) (otogi.EventRef, error) {
	md, _, err := m.stickers.Picture(ctx, st.ID)
	if err != nil {
		return otogi.EventRef{}, fmt.Errorf("sticker %d: %w", st.ID, err)
	}
	msg := otogi.Message{Roots: []otogi.PartID{"s"}, Parts: []otogi.Part{{ID: "s", Kind: otogi.PartMedia, Media: &md}}}
	if quote {
		msg.Relations = []otogi.Relation{{Kind: otogi.RelationReply, Target: e.Ref()}}
	}
	ref, err := inv.Send(ctx, stickerSlot, msg)
	if err != nil {
		return ref, fmt.Errorf("send sticker %d: %w", st.ID, err)
	}
	return ref, nil
}

// lineText is a transcript message as the recent messages, quotes, and
// judgments read it: its text and media, a sticker an agent sent reading as
// what it shows rather than as its picture.
func (m *Module) lineText(ctx context.Context, to otogi.Destination, l line, described bool) string {
	return m.lineWith(ctx, to, l, described, false)
}

// shownLine is a transcript message as p's reply reads it: its media
// described when p reads descriptions, with their IDs when p has
// examine_media.
func (m *Module) shownLine(ctx context.Context, to otogi.Destination, l line, p *profile) string {
	return m.lineWith(ctx, to, l, p.Media != nil, p.examine > 0)
}

// lineWith is lineText, the descriptions showing their media's IDs with ids.
func (m *Module) lineWith(ctx context.Context, to otogi.Destination, l line, described, ids bool) string {
	media := "[sticker: " + l.Sticker + "]"
	if l.Sticker == "" {
		media = m.mediaText(ctx, to, l.Media, described, ids)
	}
	return strings.TrimSpace(strings.TrimSpace(l.Text) + " " + media)
}
