// Package twitter answers links to posts on X (Twitter) in allowlisted chats
// with a card: an image of the post laid out as X's own post page shows it,
// drawn from an HTML template by chrome-headless-shell. Posts come from
// FxTwitter's API, or from X's embed endpoint when FxTwitter fails.
package twitter

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"html/template"

	"github.com/TundraWork/Ex-Otogi/pkg/cardimage"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Config is the module's settings.
type Config struct {
	Chrome       string     `json:"chrome" minLength:"1" doc:"The path of the chrome-headless-shell binary that draws the cards."`
	Timezone     string     `json:"timezone,omitempty" doc:"Where the cards' times are read, an IANA time zone such as Asia/Shanghai; default the server's."`
	Translate    *Translate `json:"translate,omitempty" doc:"When set, the card of a post not in Chinese also shows the post translated by a language model, under the original."`
	MaxPosts     int        `json:"max_posts,omitempty" minimum:"1" default:"3" doc:"How many posts of one message get a card, the first ones it links."`
	MaxText      int        `json:"max_text,omitempty" minimum:"50" default:"560" doc:"How much of a post's text a card shows, in characters; a longer one is cut where X would offer Show more."`
	MaxQuoteText int        `json:"max_quote_text,omitempty" minimum:"50" default:"280" doc:"How much of a quoted post's text a card shows, in characters."`
	Videos       *bool      `json:"videos,omitempty" default:"true" doc:"Send a post's videos and GIFs after its card, as videos. Whether a post whose pictures the card censors sends them is Content safety's."`
	MaxVideoMB   int        `json:"max_video_mb,omitempty" minimum:"1" default:"50" unit:"MB" doc:"The largest video sent, in megabytes; a larger one is skipped. A video is held in memory while it is sent. From the embed endpoint, the encoding of highest bitrate that fits is sent."`
}

// Module is the twitter module.
type Module struct {
	maxPosts   int
	text       textLimits
	videos     bool               // send a post's videos after its card
	maxVideo   int64              // bytes
	censor     cardimage.Pictures // how cards censor pictures; set in Start
	template   *template.Template // the card's, drawing pictures as censor says
	zone       *time.Location
	api        *client
	translator *translator // nil: no translated cards
	render     func(context.Context, card) ([]byte, error)
	host       otogi.Host
	now        func() time.Time
}

// New validates cfg and returns the module; providers finds the provider a
// translation names.
func New(cfg Config, providers func(string) (llm.Provider, error)) (*Module, error) {
	var errs []error
	if err := cardimage.Check(cfg.Chrome); err != nil {
		errs = append(errs, &otogi.SettingError{Path: "chrome", Err: err})
	}
	zone := time.Local
	if cfg.Timezone != "" {
		var err error
		if zone, err = time.LoadLocation(cfg.Timezone); err != nil {
			errs = append(errs, &otogi.SettingError{Path: "timezone", Err: err})
		}
	}
	var tr *translator
	if t := cfg.Translate; t != nil {
		p, err := providers(t.Provider)
		switch {
		case err != nil:
			errs = append(errs, &otogi.SettingError{Path: "translate.provider", Err: err})
		case t.Model == "":
			errs = append(errs, &otogi.SettingError{Path: "translate.model", Err: errors.New("required")})
		default:
			tr = &translator{cfg: *t, provider: p}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	renderer := cardimage.Renderer{Chrome: cfg.Chrome}
	maxVideoMB := cmp.Or(cfg.MaxVideoMB, 50)
	m := &Module{
		maxPosts: cmp.Or(cfg.MaxPosts, 3), text: textLimits{post: cmp.Or(cfg.MaxText, 560), quote: cmp.Or(cfg.MaxQuoteText, 280)},
		videos: cfg.Videos == nil || *cfg.Videos, maxVideo: int64(maxVideoMB) << 20,
		zone: zone, translator: tr,
		api: &client{fx: "https://api.fxtwitter.com", syndication: "https://cdn.syndication.twimg.com", http: &http.Client{Timeout: 20 * time.Second}},
		now: time.Now,
	}
	m.render = func(ctx context.Context, c card) ([]byte, error) { return render(ctx, renderer, m.template, c) }
	return m, nil
}

// Descriptor declares the module: people's new messages, in the chats on
// its allowlist.
func (*Module) Descriptor() otogi.ModuleDescriptor {
	return otogi.ModuleDescriptor{
		ID:         "twitter",
		Subscribes: []otogi.EventAction{otogi.ActionMessageCreated},
		Ordering:   otogi.OrderNone,
		Timeout:    5 * time.Minute, // a translation waits on a language model
		Requires:   []string{cardimage.ServicePictures},
		Allowlists: []otogi.AllowlistSpec{{Name: list, Title: "X", Description: "Links to posts on X get a card."}},
	}
}

// list is the module's allowlist.
const list = "twitter"

// Start keeps the host for records, and takes how cards censor pictures
// from the censor module.
func (m *Module) Start(_ context.Context, host otogi.Host) error {
	p, err := otogi.Lookup[cardimage.Pictures](host, cardimage.ServicePictures)
	if err != nil {
		return err
	}
	m.host, m.censor, m.template = host, p, newTemplate(p)
	return nil
}

// Handle answers each post a message links with its card, as a reply.
func (m *Module) Handle(ctx context.Context, e otogi.Event, inv otogi.Invocation) error {
	if e.Command != nil || !m.host.Allowed(list, e.ChatKey()) {
		return nil
	}
	for _, id := range postsIn(e.Message, m.maxPosts) {
		if err := m.answer(ctx, e, inv, id); err != nil {
			return err
		}
	}
	return nil
}

// answer sends one post's card. A post that cannot be looked up gets no
// card; that is recorded and does not fail the message's other posts.
func (m *Module) answer(ctx context.Context, e otogi.Event, inv otogi.Invocation, id string) error {
	started := m.now()
	t, source, err := m.api.status(ctx, id)
	var gone *errGone
	switch {
	case errors.As(err, &gone):
		m.host.Record(ctx, otogi.Record{Kind: "twitter.card", Outcome: "skipped", Payload: map[string]any{"post": id, "source": source, "reason": gone.reason}})
		return nil
	case err != nil:
		m.host.Logger().Warn("twitter: look up failed", "post", id, "error", err)
		m.host.Record(ctx, otogi.Record{Kind: "twitter.card", Outcome: "error", Payload: map[string]any{"post": id, "step": "look up", "error": err.Error()}})
		return nil
	}
	payload := map[string]any{"post": id, "source": source, "author": t.Author.Handle}
	m.parent(ctx, &t, payload)
	fetched := m.now()
	payload["fetched"] = fetched.Sub(started).String()
	pics, picErr := m.pictures(ctx, t)
	if picErr != nil {
		// The card goes out with what it has; the missing pictures are noted.
		m.host.Logger().Warn("twitter: pictures not downloaded", "post", id, "error", picErr)
		payload["picture_error"] = picErr.Error()
	}
	c := newCard(t, pics, m.zone, m.text)
	if m.translator != nil {
		if err := m.translate(ctx, t, &c); err != nil {
			payload["translation_error"] = err.Error()
		}
	}
	drawing := m.now()
	msg := m.message(ctx, e, t, c, payload)
	payload["rendered"] = m.now().Sub(drawing).String()
	_, err = inv.Send(ctx, "card:"+id, msg)
	if errors.Is(err, otogi.ErrRefused) && m.censor.CoverRefused && payload["render_error"] == nil && c.cover() {
		// The platform refused what the pictures show: they go out covered.
		m.host.Logger().Warn("twitter: card refused; sending it with its pictures covered", "post", id, "error", err)
		payload["refused"] = err.Error()
		_, err = inv.Send(ctx, "card:"+id+":covered", m.message(ctx, e, t, c, payload))
	}
	if err != nil {
		return err
	}
	outcome := "ok"
	if payload["render_error"] != nil || payload["refused"] != nil || payload["translation_error"] != nil || payload["parent_error"] != nil {
		outcome = "degraded"
	}
	m.host.Record(ctx, otogi.Record{Kind: "twitter.card", Outcome: outcome, Duration: m.now().Sub(started), Payload: payload})
	if m.videos {
		m.sendVideos(ctx, e, inv, t, payload["refused"] != nil)
	}
	return nil
}

// parent looks up the post t replies to, for the card to show above it,
// unless t's source gave it already. One that cannot be looked up leaves
// the card saying only whom it replies to, and payload saying why.
func (m *Module) parent(ctx context.Context, t *tweet, payload map[string]any) {
	if t.ParentID == "" {
		return
	}
	payload["parent"] = t.ParentID
	if t.Parent != nil {
		return
	}
	p, _, err := m.api.status(ctx, t.ParentID)
	var gone *errGone
	switch {
	case errors.As(err, &gone):
		payload["parent_gone"] = gone.reason
	case err != nil:
		m.host.Logger().Warn("twitter: the post replied to was not looked up", "post", t.ID, "parent", t.ParentID, "error", err)
		payload["parent_error"] = err.Error()
	default:
		t.Parent = &p
	}
}

// sendVideos sends the post's videos and GIFs after its card, each as a
// reply to the message that linked the post. Unless Content safety says to
// send them, a post X marks sensitive, or whose card the platform refused,
// keeps them: the card censors its pictures, and the videos would not be.
// A video that cannot be sent is recorded and does not fail the card.
func (m *Module) sendVideos(ctx context.Context, e otogi.Event, inv otogi.Invocation, t tweet, refused bool) {
	for i, md := range t.Media {
		src := md.Video
		if len(md.Variants) > 0 {
			src = bestVariant(md.Variants, md.Seconds, m.maxVideo)
		}
		if src == "" {
			continue
		}
		started := m.now()
		payload := map[string]any{"post": t.ID, "video": src, "kind": md.Kind}
		skip := func(reason string) {
			payload["reason"] = reason
			m.host.Record(ctx, otogi.Record{Kind: "twitter.video", Outcome: "skipped", Payload: payload})
		}
		switch {
		case t.Sensitive && !m.censor.CensoredVideos:
			skip("X marks the post sensitive")
			continue
		case refused && !m.censor.CensoredVideos:
			skip("the platform refused the card")
			continue
		}
		data, err := m.api.video(ctx, src, m.maxVideo)
		switch {
		case errors.Is(err, errTooLarge):
			skip(fmt.Sprintf("larger than %d MB", m.maxVideo>>20))
			continue
		case err != nil:
			m.host.Logger().Warn("twitter: video not downloaded", "post", t.ID, "error", err)
			payload["step"], payload["error"] = "download", err.Error()
			m.host.Record(ctx, otogi.Record{Kind: "twitter.video", Outcome: "error", Duration: m.now().Sub(started), Payload: payload})
			continue
		}
		role := otogi.MediaVideo
		if md.Kind == "gif" {
			role = otogi.MediaAnimation
		}
		msg := otogi.Message{Roots: []otogi.PartID{"video"}, Parts: []otogi.Part{{ID: "video", Kind: otogi.PartMedia, Media: &otogi.Media{
			Role: role, MediaType: "video/mp4", Filename: fmt.Sprintf("%s-%d.mp4", t.ID, i+1), ByteSize: int64(len(data)),
			Width: md.Width, Height: md.Height, Duration: time.Duration(md.Seconds * float64(time.Second)),
			Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: data},
		}}}, Relations: []otogi.Relation{{Kind: otogi.RelationReply, Target: e.Ref()}}}
		payload["bytes"], payload["downloaded"] = len(data), m.now().Sub(started).String()
		if _, err := inv.Send(ctx, fmt.Sprintf("video:%s:%d", t.ID, i+1), msg); err != nil {
			m.host.Logger().Warn("twitter: video not sent", "post", t.ID, "error", err)
			payload["step"], payload["error"] = "send", err.Error()
			m.host.Record(ctx, otogi.Record{Kind: "twitter.video", Outcome: "error", Duration: m.now().Sub(started), Payload: payload})
			continue
		}
		m.host.Record(ctx, otogi.Record{Kind: "twitter.video", Outcome: "ok", Duration: m.now().Sub(started), Payload: payload})
	}
}

// message is the card drawn as a reply to e, or the post in words when it
// cannot be drawn, which payload notes.
func (m *Module) message(ctx context.Context, e otogi.Event, t tweet, c card, payload map[string]any) otogi.Message {
	png, err := m.render(ctx, c)
	var msg otogi.Message
	if err != nil {
		m.host.Logger().Warn("twitter: card not rendered; sending text", "post", t.ID, "error", err)
		payload["render_error"] = err.Error()
		msg = textCard(t, c.Translation, m.text.post)
	} else {
		payload["bytes"] = len(png)
		msg = otogi.Message{Roots: []otogi.PartID{"card"}, Parts: []otogi.Part{{ID: "card", Kind: otogi.PartMedia,
			Media: &otogi.Media{Role: otogi.MediaImage, MediaType: "image/png", ByteSize: int64(len(png)), Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: png}}}}}
	}
	msg.Relations = []otogi.Relation{{Kind: otogi.RelationReply, Target: e.Ref()}}
	return msg
}

// translate adds the post in Chinese to its card, under the original, and
// so its quote, the post it replies to, and that post's quote: each unless
// it is in Chinese already. A failed translation is recorded and returned,
// and the card goes out without it. A part the model gave back as it came
// gets no translation, and the record is degraded and names it in echoed.
func (m *Module) translate(ctx context.Context, t tweet, c *card) error {
	started := m.now()
	tr, needed, err := m.translator.translate(ctx, t)
	payload := map[string]any{"post": t.ID, "model": m.translator.cfg.Model, "took": m.now().Sub(started).String()}
	switch {
	case err != nil:
		m.host.Logger().Warn("twitter: translation failed", "post", t.ID, "error", err)
		payload["error"] = err.Error()
		m.host.Record(ctx, otogi.Record{Kind: "twitter.translation", Outcome: "error", Payload: payload})
		return err
	case !needed:
		m.host.Logger().Debug("twitter: post is in Chinese; no translation", "post", t.ID)
		return nil
	}
	if tr.Post != "" {
		payload["language"] = tr.Language
		text, more := excerpt(tr.Post, m.text.post)
		c.Translation = &translatedText{By: "由 " + providerName(m.translator.cfg.Provider) + " 翻译自" + tr.Language, Plain: text, Text: spans(text), More: more}
	}
	quote := func(q *quoted, text string) {
		if q != nil && text != "" {
			text, _ = excerpt(text, m.text.quote)
			q.Translation = spans(text)
		}
	}
	quote(c.Quote, tr.Quote)
	if p := c.Parent; p != nil {
		if tr.Parent != "" {
			text, _ := excerpt(tr.Parent, m.text.post)
			p.Translation = spans(text)
		}
		quote(p.Quote, tr.ParentQuote)
	}
	outcome := "ok"
	if tr.Echoed != "" {
		m.host.Logger().Warn("twitter: the model gave parts back untranslated", "post", t.ID, "parts", tr.Echoed)
		payload["echoed"], outcome = tr.Echoed, "degraded"
	}
	m.host.Record(ctx, otogi.Record{Kind: "twitter.translation", Outcome: outcome, Duration: m.now().Sub(started), Payload: payload})
	return nil
}

// providerName is a provider as a card names it: gemini is Gemini.
func providerName(p string) string {
	if p == "" {
		return p
	}
	return strings.ToUpper(p[:1]) + p[1:]
}

// pictures downloads what the card shows: avatars, and the media at the
// size its place on the card needs.
func (m *Module) pictures(ctx context.Context, t tweet) (pictures, error) {
	pics := pictures{}
	var errs []error
	get := func(src, size string) {
		if src == "" || pics[src] != "" {
			return
		}
		data, mime, err := m.api.picture(ctx, src, size)
		if err != nil {
			errs = append(errs, err)
			return
		}
		pics[src] = cardimage.DataURI(data, mime)
	}
	get(t.Author.Avatar, "")
	size := "small"
	if len(t.Media) == 1 {
		size = "medium"
	}
	for _, md := range t.Media[:min(len(t.Media), 4)] {
		get(md.Image, size)
	}
	quote := func(q *tweet) {
		if q == nil {
			return
		}
		get(q.Author.Avatar, "")
		for _, md := range q.Media[:min(len(q.Media), 4)] {
			get(md.Image, "small")
		}
	}
	quote(t.Quote)
	if p := t.Parent; p != nil {
		quote(p)
		quote(p.Quote)
	}
	return pics, errors.Join(errs...)
}

// textCard is the post in words, and its translation when it has one, for
// when the card cannot be drawn. It holds no link: the message it answers
// has one, and QQ groups refuse messages with links.
func textCard(t tweet, tr *translatedText, limit int) otogi.Message {
	var r otogi.RichText
	r.Mark(otogi.AnnotationBold, "", func() { r.Write(t.Author.Name) })
	text, _ := excerpt(t.Text, limit)
	r.Write(" @" + t.Author.Handle + "\n" + text)
	if tr != nil {
		r.Write("\n\n" + tr.By + "：\n" + tr.Plain)
	}
	return r.Message()
}
