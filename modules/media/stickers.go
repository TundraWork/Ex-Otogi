package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	contract "github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

const (
	// stickerCommand is the command operators manage stickers with.
	stickerCommand = "sticker"
	// maxStickerBytes keeps a sticker within what QQ takes in one upload,
	// as Otogi's QQ driver uploads: the test bot's /files took 10,000,500
	// bytes and refused 10,485,000 (40093011, too large), and the driver has
	// no chunked upload.
	maxStickerBytes = 9 << 20
	// maxStickers bounds the list of stickers that goes before an agent's
	// every turn: 50 lines, about two thousand characters.
	maxStickers = 50
	// maxStickerTotal keeps the stickers' bytes, which live in the module's
	// database and its backups, small: 50 stickers of 9 MiB would be 450
	// MiB. eku's five galleries on dsh hold 163 pictures in about 130 MiB.
	maxStickerTotal = 128 << 20
	// recentMedia is how long a message's media is kept for /sticker add
	// replying to it: QQ's media URLs last about an hour.
	recentMedia = 2 * time.Hour
	// describeSticker is how long adding a sticker waits for its
	// description.
	describeSticker = 30 * time.Second
)

// stickerTypes are the picture types kept, by content: both drivers send
// them as pictures. Telegram's GIFs are MP4 files, and WebP is untried on
// Telegram.
var stickerTypes = []string{"image/png", "image/jpeg", "image/gif"}

// stickerLimits bound the stickers kept: how many, and their bytes in all.
// They are maxStickers and maxStickerTotal, smaller in tests.
type stickerLimits struct {
	count int
	total int64
}

const stickerUsage = "Usage: /sticker add [note] (replying to a picture, or with one) | list | note <n> [note] | remove <n>"

// runSticker runs /sticker and replies with how it went.
func (m *Module) runSticker(ctx context.Context, e otogi.Event, inv otogi.Invocation) error {
	reply, err := m.sticker(ctx, e, inv)
	if err != nil {
		reply = strings.TrimSpace(reply + "\nSomething went wrong; see the event log.")
	}
	_, sendErr := inv.Send(ctx, "reply", otogi.Reply(reply, e.Ref()))
	return errors.Join(err, sendErr)
}

func (m *Module) sticker(ctx context.Context, e otogi.Event, inv otogi.Invocation) (string, error) {
	if e.Actor.ID == "" || !slices.Contains(m.operators(), e.ActorKey()) {
		return fmt.Sprintf("Only operators can manage stickers. Your ID is %s.", e.ActorKey()), nil
	}
	if e.Conversation.Kind != otogi.ConversationDirect && !m.host.Allowed(describeList, e.ChatKey()) {
		return "This chat is not on media's describe list.", nil
	}
	argv := e.Command.Argv
	if len(argv) == 0 {
		return stickerUsage, nil
	}
	switch strings.ToLower(argv[0]) {
	case "add":
		return m.addStickers(ctx, e, inv, after(e.Command.Args, 1))
	case "list":
		return m.listStickers(ctx)
	case "note", "remove":
		if len(argv) < 2 {
			return stickerUsage, nil
		}
		id, err := strconv.Atoi(argv[1])
		if err != nil {
			return stickerUsage, nil
		}
		if strings.ToLower(argv[0]) == "note" {
			return m.noteSticker(ctx, e, id, after(e.Command.Args, 2))
		}
		return m.removeSticker(ctx, e, id)
	}
	return stickerUsage, nil
}

// after returns args without its first n words.
func after(args string, n int) string {
	for range n {
		args = strings.TrimLeftFunc(args, unicode.IsSpace)
		i := strings.IndexFunc(args, unicode.IsSpace)
		if i < 0 {
			return ""
		}
		args = args[i:]
	}
	return strings.TrimSpace(args)
}

// addStickers keeps the pictures of e's message, or of the message it
// replies to, with note.
func (m *Module) addStickers(ctx context.Context, e otogi.Event, inv otogi.Invocation, note string) (string, error) {
	media := mediaOf(e.Message)
	if len(media) == 0 {
		target, ok := replyTarget(e.Message)
		if !ok {
			return "Reply to a picture with /sticker add, or send the picture with the command.", nil
		}
		found, ok, err := m.store.recent(ctx, e.Destination(), string(target.SourceEvent), m.now().Add(-recentMedia))
		if err != nil {
			return "", err
		}
		if !ok {
			return "I can't find a picture in that message any more; send it again and add it right away.", nil
		}
		media = found
	}
	var lines []string
	var errs []error
	for _, md := range media {
		line, err := m.addSticker(ctx, e, inv, md, note)
		lines = append(lines, line)
		errs = append(errs, err)
	}
	return strings.Join(lines, "\n"), errors.Join(errs...)
}

// addSticker keeps one picture, md, with note.
func (m *Module) addSticker(ctx context.Context, e otogi.Event, inv otogi.Invocation, md otogi.Media, note string) (string, error) {
	payload := map[string]any{"action": "add", "by": e.ActorKey()}
	skip := func(reason, reply string) (string, error) {
		payload["reason"] = reason
		m.recordSticker(ctx, e, "skipped", payload)
		return reply, nil
	}
	tooLarge := func(size int64) (string, error) {
		payload["bytes"] = size
		return skip("too large", fmt.Sprintf("That picture is %.1f MiB; a sticker can be at most %d MiB.", float64(size)/(1<<20), maxStickerBytes>>20))
	}
	if md.ByteSize > maxStickerBytes {
		return tooLarge(md.ByteSize)
	}
	data := md.Source.Inline
	if md.Source.Kind != otogi.MediaSourceInline {
		var err error
		if data, err = inv.FetchMedia(ctx, e.Destination(), md.Source); err != nil {
			payload["error"] = err.Error()
			return skip("gone", "Couldn't fetch that picture any more; send it again and add it right away.")
		}
	}
	if len(data) > maxStickerBytes {
		return tooLarge(int64(len(data)))
	}
	payload["bytes"] = len(data)
	mediaType := http.DetectContentType(data)
	if !slices.Contains(stickerTypes, mediaType) {
		payload["media_type"] = mediaType
		return skip("not png, jpeg, or gif", "Only PNG, JPEG, and GIF pictures can be kept.")
	}
	sum := sha256.Sum256(data)
	payload["sha256"] = hex.EncodeToString(sum[:])
	kept, ok, err := m.store.stickerByDigest(ctx, sum[:])
	if err != nil {
		return "", err
	}
	if ok {
		payload["id"] = kept
		return skip("already kept", fmt.Sprintf("Already kept as sticker %d.", kept))
	}
	// Checked before describing, so a full collection costs no model call.
	count, total, err := m.store.stickerUsage(ctx)
	switch {
	case err != nil:
		return "", err
	case count >= m.limits.count:
		return skip("full", fmt.Sprintf("There are %d stickers already; remove one first.", m.limits.count))
	case total+int64(len(data)) > m.limits.total:
		return skip("too much in all", fmt.Sprintf("The stickers would take more than %d MiB; remove some first.", m.limits.total>>20))
	}
	d, err := m.stickerDescription(ctx, e, md, data, sum[:])
	if err != nil {
		return "", err
	}
	st := sticker{SHA256: sum[:], MediaType: mediaType, Bytes: data, Kind: d.Kind, Summary: d.Summary, Note: note}
	id, existed, err := m.store.addSticker(ctx, st, e.ActorKey(), m.now())
	switch {
	case err != nil:
		return "", err
	case existed:
		payload["id"] = id
		return skip("already kept", fmt.Sprintf("Already kept as sticker %d.", id))
	}
	st.ID = id
	payload["id"], payload["kind"], payload["text"], payload["media_type"] = id, st.Kind, st.text(), mediaType
	m.recordSticker(ctx, e, "ok", payload)
	if st.text() == "" {
		return fmt.Sprintf("Kept as sticker %d, but it has no description; give it a note with /sticker note %d <note> so agents can use it.", id, id), nil
	}
	return fmt.Sprintf("Kept as sticker %d: %s", id, st.text()), nil
}

// stickerDescription returns the description of a picture being kept: the
// one kept for md or its bytes, else one made now, waiting at most
// describeSticker. A picture that is not described has an empty
// description; only storage errors are returned.
func (m *Module) stickerDescription(ctx context.Context, e otogi.Event, md otogi.Media, data, sum []byte) (contract.Description, error) {
	k, src := mediaKey(md)
	if k.Algorithm == "" && src != "" {
		found, ok, err := m.store.source(ctx, src)
		if err != nil {
			return contract.Description{}, err
		}
		if ok {
			k = found
		}
	}
	byBytes := key{Algorithm: "sha256", Digest: sum}
	for _, known := range []key{k, byBytes} {
		if known.Algorithm == "" {
			continue
		}
		if d, ok, err := m.store.get(ctx, known); err != nil || ok {
			return d, err
		}
	}
	if k.Algorithm == "" {
		k = byBytes
	}
	p, err := m.prepare(md, data)
	if err != nil {
		return contract.Description{}, nil
	}
	dctx, cancel := context.WithTimeout(ctx, describeSticker)
	defer cancel()
	d, model, err := m.describe(dctx, e.ChatKey(), k, p, len(data))
	if err != nil {
		return contract.Description{}, nil // each failure is recorded as media.failed
	}
	return d, m.store.put(ctx, k, d, model, m.now())
}

func (m *Module) listStickers(ctx context.Context) (string, error) {
	all, err := m.store.stickers(ctx)
	if err != nil {
		return "", err
	}
	if len(all) == 0 {
		return "No stickers yet. Reply to a picture with /sticker add to keep it.", nil
	}
	var total int64
	for _, st := range all {
		total += st.Size
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d of %d stickers, %.1f of %d MiB:", len(all), m.limits.count, float64(total)/(1<<20), m.limits.total>>20)
	for _, st := range all {
		kind, text := string(st.Kind), st.text()
		if kind == "" {
			kind = "-"
		}
		if text == "" {
			text = "(no description; agents don't see it until it has a note)"
		}
		fmt.Fprintf(&b, "\n%d. [%s] %s", st.ID, kind, text)
	}
	return b.String(), nil
}

func (m *Module) noteSticker(ctx context.Context, e otogi.Event, id int, note string) (string, error) {
	st, ok, err := m.store.setStickerNote(ctx, id, note)
	if err != nil {
		return "", err
	}
	if !ok {
		return fmt.Sprintf("There is no sticker %d.", id), nil
	}
	m.recordSticker(ctx, e, "ok", map[string]any{"action": "note", "id": id, "sha256": hex.EncodeToString(st.SHA256), "kind": st.Kind, "text": st.text(), "by": e.ActorKey(), "bytes": st.Size})
	if st.text() == "" {
		return fmt.Sprintf("Sticker %d has no note or description now; agents don't see it.", id), nil
	}
	return fmt.Sprintf("Sticker %d now shows: %s", id, st.text()), nil
}

func (m *Module) removeSticker(ctx context.Context, e otogi.Event, id int) (string, error) {
	st, ok, err := m.store.removeSticker(ctx, id)
	if err != nil {
		return "", err
	}
	if !ok {
		return fmt.Sprintf("There is no sticker %d.", id), nil
	}
	m.recordSticker(ctx, e, "ok", map[string]any{"action": "remove", "id": id, "sha256": hex.EncodeToString(st.SHA256), "kind": st.Kind, "text": st.text(), "by": e.ActorKey(), "bytes": st.Size})
	return fmt.Sprintf("Removed sticker %d.", id), nil
}

// recordSticker records a /sticker action on e as media.sticker.
func (m *Module) recordSticker(ctx context.Context, e otogi.Event, outcome string, payload map[string]any) {
	m.host.Record(ctx, otogi.Record{Kind: "media.sticker", Event: e.ID, Platform: e.Platform, Account: e.Account,
		Conversation: e.Conversation.ID, Outcome: outcome, Payload: payload})
}

// replyTarget returns the message msg replies to.
func replyTarget(msg otogi.Message) (otogi.EventRef, bool) {
	for _, r := range msg.Relations {
		if r.Kind == otogi.RelationReply {
			return r.Target, true
		}
	}
	return otogi.EventRef{}, false
}

// stickers is the stickers service: the module's stickers for agents.
type stickers struct{ m *Module }

// List returns the stickers that have a note or a description.
func (s stickers) List(ctx context.Context) ([]contract.Sticker, error) {
	all, err := s.m.store.stickers(ctx)
	if err != nil {
		return nil, err
	}
	var out []contract.Sticker
	for _, st := range all {
		if st.text() != "" {
			out = append(out, contract.Sticker{ID: st.ID, Kind: st.Kind, Text: st.text()})
		}
	}
	return out, nil
}

// Picture returns sticker id's picture as inline media.
func (s stickers) Picture(ctx context.Context, id int) (otogi.Media, contract.Sticker, error) {
	st, ok, err := s.m.store.stickerPicture(ctx, id)
	if err != nil {
		return otogi.Media{}, contract.Sticker{}, err
	}
	if !ok {
		return otogi.Media{}, contract.Sticker{}, contract.ErrNoSticker
	}
	role, ext := otogi.MediaImage, strings.TrimPrefix(st.MediaType, "image/")
	if st.MediaType == "image/gif" {
		role = otogi.MediaAnimation
	}
	md := otogi.Media{
		Role: role, MediaType: st.MediaType, ByteSize: st.Size, Filename: fmt.Sprintf("sticker-%d.%s", id, ext),
		Hashes: []otogi.MediaHash{{Algorithm: "sha256", Value: st.SHA256}},
		Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: st.Bytes},
	}
	return md, contract.Sticker{ID: st.ID, Kind: st.Kind, Text: st.text()}, nil
}
