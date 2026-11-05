package qq

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sethvargo/go-retry"

	qqstore "github.com/TundraWork/Ex-Otogi/internal/adapter/qq/store"
	"github.com/TundraWork/Ex-Otogi/pkg/markdown"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Passive reply windows, with a margin for the clock difference between QQ
// and us: QQ allows 60 minutes and 4 replies in C2C, and 5 minutes and 5
// replies in groups.
var (
	c2cWindow   = qqstore.Window{For: 55 * time.Minute, Replies: 4}
	groupWindow = qqstore.Window{For: 4*time.Minute + 30*time.Second, Replies: 5}
)

// maxText is the most characters one message carries; longer text is split
// into more messages. QQ does not document its limit; Tencent's own plugins
// split at 4500.
const maxText = 4000

// Message types.
const (
	msgText     = 0
	msgMarkdown = 2
	msgMedia    = 7
)

// QQ codes for a passive reply QQ no longer accepts: the message is too old,
// or has had its replies. The send goes out as a proactive one instead.
var passiveRefused = []int{40034005, 304103, 40034024, 40034025, 40034026, 40034128}

// QQ codes for a refusal that retrying does not change: no permission to
// send proactively, the user rejects the bot, the bot is muted or not in
// the group, or the message is not allowed.
var refused = []int{40034100, 40034102, 40034105, 40034122, 40054013, 40054002, 40054003, 40034101, 40034006, 40054010, 40064004, 304023, 304024}

// QQ codes for a failure QQ asks to retry.
var transient = []int{11242, 11252, 11263, 11281, 50055001, 50055002, 40034004}

// duplicateSeq is QQ refusing a msg_id and msg_seq it has seen: the message
// was sent before.
const duplicateSeq = 40054005

// effects sends and recalls messages.
type effects struct {
	api     *api
	store   *qqstore.Store
	account otogi.AccountID
	wake    chan<- struct{}
	log     *slog.Logger
	now     func() time.Time
	// self returns the bot's own user ID and name.
	self func() (string, string)
	// retries is how often a transient failure is retried.
	retries uint64
	backoff time.Duration
}

// Apply performs an effect; see otogi.Effector.
func (x *effects) Apply(ctx context.Context, e otogi.Effect) (otogi.EventRef, error) {
	switch e.Kind {
	case otogi.EffectSend:
		refs, err := x.send(ctx, string(e.Key), e.Origin, e.To.Conversation, e.Message)
		if len(refs) == 0 {
			return otogi.EventRef{}, err
		}
		return refs[0], err
	case otogi.EffectDelete:
		return e.Ref, x.recall(ctx, e.Ref)
	default:
		return otogi.EventRef{}, fmt.Errorf("qq: %s: %w", e.Kind, otogi.ErrUnsupported)
	}
}

// outgoing is one QQ message to send.
type outgoing struct {
	body  map[string]any
	media *otogi.Media // uploaded first, then sent as msg_type 7
	text  string       // what the message says, for the event it becomes
}

// send sends msg to conversation as one or more QQ messages, each at most
// once per key, and returns them in order.
func (x *effects) send(ctx context.Context, key, origin string, conversation otogi.ConversationID, msg otogi.Message) ([]otogi.EventRef, error) {
	parts, err := x.encode(ctx, msg)
	if err != nil {
		return nil, err
	}
	var refs []otogi.EventRef
	for i, out := range parts {
		partKey := key
		if i > 0 {
			partKey = fmt.Sprintf("%s#%d", key, i)
		}
		ref, err := x.sendOne(ctx, partKey, origin, conversation, out)
		if err != nil {
			return refs, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// sendOne sends one message at most once per key. A passive reply that QQ
// already has (the same msg_id and msg_seq) is not sent twice; a proactive
// send interrupted before QQ answered is not repeated, because QQ could not
// tell a repeat from a new message.
func (x *effects) sendOne(ctx context.Context, key, origin string, conversation otogi.ConversationID, out outgoing) (otogi.EventRef, error) {
	window := c2cWindow
	if strings.HasPrefix(string(conversation), "group:") {
		window = groupWindow
	}
	send, existed, err := x.store.ClaimSend(ctx, key, string(conversation), origin, window, x.now())
	if err != nil {
		return otogi.EventRef{}, err
	}
	ref := otogi.EventRef{Platform: Platform, Account: x.account, Conversation: conversation}
	switch {
	case existed && send.Sent:
		ref.SourceEvent = otogi.SourceEventID(send.MessageID)
		return ref, nil
	case existed && send.MsgID == "":
		return ref, fmt.Errorf("qq: send %s was interrupted before QQ answered and may have been delivered; not sending it again: %w", key, otogi.ErrUnavailable)
	}
	base, err := basePath(conversation)
	if err != nil {
		return otogi.EventRef{}, err
	}
	if out.media != nil {
		info, err := x.upload(ctx, base, *out.media)
		if err != nil {
			return x.release(ctx, key, ref, err)
		}
		out.body["media"] = map[string]string{"file_info": info}
	}
	var resp struct {
		ID        string `json:"id"`
		Timestamp any    `json:"timestamp"`
		ExtInfo   struct {
			RefIdx string `json:"ref_idx"`
		} `json:"ext_info"`
	}
	err = x.post(ctx, base+"/messages", send, out.body, &resp)
	var e *apiError
	if errors.As(err, &e) && slices.Contains(passiveRefused, e.Code) && send.MsgID != "" {
		x.log.Warn("qq: passive reply refused; sending proactively", "key", key, "code", e.Code, "message", e.Message)
		if err := x.store.MakeProactive(ctx, key); err != nil {
			return otogi.EventRef{}, err
		}
		send.MsgID, send.MsgSeq = "", 0
		err = x.post(ctx, base+"/messages", send, out.body, &resp)
	}
	switch {
	case errors.As(err, &e) && e.Code == duplicateSeq:
		// An earlier attempt was delivered; QQ does not say which message.
		return ref, nil
	case errors.As(err, &e):
		return x.release(ctx, key, ref, err)
	case err != nil:
		return ref, err // no answer from QQ: the send stays claimed
	}
	ref.SourceEvent = otogi.SourceEventID(resp.ID)
	if err := x.store.CompleteSend(ctx, key, resp.ID); err != nil {
		return ref, err
	}
	if resp.ExtInfo.RefIdx != "" {
		if err := x.store.PutRef(ctx, resp.ExtInfo.RefIdx, resp.ID, string(conversation), x.now()); err != nil {
			return ref, err
		}
	}
	if strings.HasPrefix(string(conversation), "user:") {
		// QQ echoes the bot's group messages but not its C2C ones; the
		// driver reports those itself, so modules see both sides.
		if err := x.echo(ctx, conversation, resp.ID, resp.ExtInfo.RefIdx, out.text); err != nil {
			return ref, err
		}
	}
	return ref, nil
}

// release forgets a send QQ refused, so it is not mistaken for one that may
// have been delivered, and returns err classified.
func (x *effects) release(ctx context.Context, key string, ref otogi.EventRef, err error) (otogi.EventRef, error) {
	if releaseErr := x.store.ReleaseSend(ctx, key); releaseErr != nil {
		return ref, errors.Join(classify(err), releaseErr)
	}
	return ref, classify(err)
}

// post sends a message body as send's passive reply, or proactively,
// retrying what QQ asks to retry.
func (x *effects) post(ctx context.Context, path string, send qqstore.Send, body map[string]any, out any) error {
	b := make(map[string]any, len(body)+2)
	for k, v := range body {
		b[k] = v
	}
	if send.MsgID != "" {
		b["msg_id"], b["msg_seq"] = send.MsgID, send.MsgSeq
	}
	return x.retry(ctx, func(ctx context.Context) error { return x.api.call(ctx, http.MethodPost, path, b, out) })
}

// retry runs call, retrying failures QQ marks as transient and rate limits.
func (x *effects) retry(ctx context.Context, call func(context.Context) error) error {
	attempt := 0
	return retry.Do(ctx, retry.WithMaxRetries(x.retries, retry.NewExponential(x.backoff)), func(ctx context.Context) error {
		attempt++
		err := call(ctx)
		var e *apiError
		if errors.As(err, &e) && (e.Status == http.StatusTooManyRequests || e.Status >= 500 && e.Code == 0 || slices.Contains(transient, e.Code)) {
			x.log.Warn("qq: call failed; retrying", "attempt", attempt, "error", err)
			return retry.RetryableError(err)
		}
		return err
	})
}

// classify wraps QQ's refusals in otogi.ErrUnavailable, keeping QQ's code
// and message.
func classify(err error) error {
	var e *apiError
	if errors.As(err, &e) && (slices.Contains(refused, e.Code) || slices.Contains(passiveRefused, e.Code)) {
		return fmt.Errorf("%w: %w", err, otogi.ErrUnavailable)
	}
	return err
}

// echo queues the event for a C2C message the bot sent.
func (x *effects) echo(ctx context.Context, conversation otogi.ConversationID, id, refIdx, text string) error {
	self, name := x.self()
	d, err := json.Marshal(sentMessage{ID: id, Content: text, Timestamp: x.now().UTC().Format(time.RFC3339), Peer: strings.TrimPrefix(string(conversation), "user:"), RefIdx: refIdx, Self: self, SelfName: name})
	if err != nil {
		return fmt.Errorf("qq: encode sent message: %w", err)
	}
	f, err := json.Marshal(frame{Op: opDispatch, T: sentType, D: d})
	if err != nil {
		return fmt.Errorf("qq: encode sent message: %w", err)
	}
	if err := x.store.InsertInbox(ctx, "message:"+id, f, x.now()); err != nil {
		return err
	}
	select {
	case x.wake <- struct{}{}:
	default:
	}
	return nil
}

// recall withdraws one of the bot's messages; QQ allows it for 2 minutes.
func (x *effects) recall(ctx context.Context, ref otogi.EventRef) error {
	base, err := basePath(ref.Conversation)
	if err != nil {
		return err
	}
	if ref.SourceEvent == "" {
		return fmt.Errorf("qq: recall: no message ID: %w", otogi.ErrInvalid)
	}
	err = x.retry(ctx, func(ctx context.Context) error {
		return x.api.call(ctx, http.MethodDelete, base+"/messages/"+string(ref.SourceEvent), nil, nil)
	})
	return classify(err)
}

// upload uploads media for a message to the conversation and returns its
// file_info.
func (x *effects) upload(ctx context.Context, base string, media otogi.Media) (string, error) {
	body := map[string]any{"file_type": fileType(media), "srv_send_msg": false}
	switch {
	case media.Source.Kind == otogi.MediaSourceInline:
		body["file_data"] = base64.StdEncoding.EncodeToString(media.Source.Inline)
	case media.Source.Kind == otogi.MediaSourceExternal && media.Source.URL != "":
		body["url"] = media.Source.URL
	default:
		return "", fmt.Errorf("qq: send %s media: %w", media.Source.Kind, otogi.ErrUnsupported)
	}
	if media.Filename != "" {
		body["file_name"] = media.Filename
	}
	var out struct {
		FileInfo string `json:"file_info"`
	}
	if err := x.retry(ctx, func(ctx context.Context) error { return x.api.call(ctx, http.MethodPost, base+"/files", body, &out) }); err != nil {
		return "", err
	}
	return out.FileInfo, nil
}

func fileType(m otogi.Media) int {
	switch m.Role {
	case otogi.MediaImage, otogi.MediaAnimation, otogi.MediaSticker:
		return 1
	case otogi.MediaVideo:
		return 2
	case otogi.MediaVoice:
		return 3
	}
	return 4
}

// encode turns a message into QQ messages: its text, split where it is too
// long, then each media part. A reply quotes the message it answers.
func (x *effects) encode(ctx context.Context, msg otogi.Message) ([]outgoing, error) {
	if err := msg.Validate(); err != nil {
		return nil, err
	}
	var quote map[string]string
	for _, r := range msg.Relations {
		if r.Kind != otogi.RelationReply || r.Target.SourceEvent == "" {
			continue
		}
		idx, ok, err := x.store.FindRefIdx(ctx, string(r.Target.SourceEvent))
		if err != nil {
			return nil, err
		}
		if ok {
			quote = map[string]string{"message_id": idx}
		}
	}
	var text otogi.RichText
	var media []otogi.Media
	for _, p := range msg.Parts {
		switch {
		case p.Kind == otogi.PartText:
			text.Append(p.Text, p.Annotations)
		case p.Media != nil:
			media = append(media, *p.Media)
		}
	}
	var out []outgoing
	rest := &text
	for rest.Len() > 0 {
		var first *otogi.RichText
		first, rest = rest.Split(func(prefix string, _ int) bool { return utf8.RuneCountInString(prefix) <= maxText })
		body, content := encodeText(first)
		if strings.TrimSpace(content) == "" {
			continue
		}
		out = append(out, outgoing{body: body, text: content})
	}
	for i := range media {
		out = append(out, outgoing{body: map[string]any{"msg_type": msgMedia}, media: &media[i], text: "[" + string(media[i].Role) + "]"})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("qq: nothing to send: %w", otogi.ErrInvalid)
	}
	if quote != nil {
		out[0].body["message_reference"] = quote
	}
	return out, nil
}

// encodeText renders text as QQ content: plain text when it has no
// formatting, Markdown when it has. Mentions of QQ users become <@openid>.
func encodeText(r *otogi.RichText) (map[string]any, string) {
	text, annotations := r.String(), r.Annotations()
	formatted := slices.ContainsFunc(annotations, func(a otogi.Annotation) bool {
		return a.Kind != otogi.AnnotationMention || !strings.HasPrefix(a.Target, "user:")
	})
	// Mentions split the text into segments formatted on their own.
	var b strings.Builder
	last := 0
	for _, a := range annotations {
		if a.Kind != otogi.AnnotationMention || !strings.HasPrefix(a.Target, "user:") {
			continue
		}
		b.WriteString(segment(r, last, a.Offset, formatted))
		b.WriteString("<@" + strings.TrimPrefix(a.Target, "user:") + ">")
		last = a.Offset + a.Length
	}
	b.WriteString(segment(r, last, len(text), formatted))
	content := b.String()
	if formatted {
		return map[string]any{"msg_type": msgMarkdown, "markdown": map[string]string{"content": content}}, content
	}
	return map[string]any{"msg_type": msgText, "content": content}, content
}

func segment(r *otogi.RichText, from, to int, formatted bool) string {
	s := r.Slice(from, to)
	if !formatted {
		return s.String()
	}
	return markdown.Format(s.String(), s.Annotations())
}

// basePath is the API path of a conversation.
func basePath(conversation otogi.ConversationID) (string, error) {
	kind, id, ok := strings.Cut(string(conversation), ":")
	switch {
	case ok && kind == "user" && id != "":
		return "/v2/users/" + id, nil
	case ok && kind == "group" && id != "":
		return "/v2/groups/" + id, nil
	}
	return "", fmt.Errorf("qq: conversation %q: %w", conversation, otogi.ErrInvalid)
}
