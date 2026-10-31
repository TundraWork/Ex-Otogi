package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// provider answers each Generate with the next scripted response, showing
// its content as one partial, and keeps the requests.
type provider struct {
	mu        sync.Mutex
	responses []llm.Response
	errs      []error
	requests  []llm.Request
}

func (p *provider) Generate(_ context.Context, req llm.Request, partial func(otogi.Message) error) (llm.Response, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	n := len(p.requests)
	p.mu.Unlock()
	if n <= len(p.errs) && p.errs[n-1] != nil {
		return llm.Response{}, p.errs[n-1]
	}
	resp := p.responses[min(n, len(p.responses))-1]
	if partial != nil && len(resp.Turn.Content.Parts) > 0 {
		if err := partial(resp.Turn.Content); err != nil {
			return llm.Response{}, err
		}
	}
	return resp, nil
}

func answer(text string) llm.Response {
	return llm.Response{Turn: llm.Turn{Role: llm.RoleModel, Content: otogi.TextMessage(text), Native: &llm.Native{Provider: "fake", Data: []byte(`"` + text + `"`)}}, Markdown: text, Finish: llm.FinishStop}
}

// stream keeps what a reply showed.
type stream struct {
	h       *harness
	updates []string
	final   otogi.Message
}

func (s *stream) Update(_ context.Context, msg otogi.Message) error {
	s.updates = append(s.updates, msg.PlainText())
	return nil
}

func (s *stream) Close(_ context.Context, msg otogi.Message) ([]otogi.EventRef, error) {
	s.final = msg
	s.h.sent++
	return []otogi.EventRef{{Platform: "telegram", Account: "main", Conversation: "chat:1", SourceEvent: otogi.SourceEventID(fmt.Sprintf("agent-%d", s.h.sent))}}, nil
}

type invocation struct {
	otogi.Invocation
	h *harness
}

func (i invocation) Stream(context.Context, string, otogi.Destination) otogi.MessageStream {
	s := &stream{h: i.h}
	i.h.streams = append(i.h.streams, s)
	return s
}

func (i invocation) FetchMedia(_ context.Context, _ otogi.Destination, src otogi.MediaSource) ([]byte, error) {
	return []byte("bytes of " + string(src.Locator.Data)), nil
}

type host struct {
	otogi.Host
	storage *modulestore.Storage
	records []otogi.Record
}

func (h *host) Logger() *slog.Logger { return slog.New(slog.DiscardHandler) }
func (h *host) Database(ctx context.Context) (*sql.DB, error) {
	return h.storage.Database(ctx, "chat")
}
func (h *host) Record(_ context.Context, r otogi.Record) { h.records = append(h.records, r) }
func (h *host) Schedule(string, string, otogi.Job) error { return nil }

type harness struct {
	t       *testing.T
	m       *Module
	host    *host
	llm     map[string]*provider
	streams []*stream
	sent    int
	now     time.Time
	seq     int
}

const group = "telegram:chat:1"

func newHarness(t *testing.T, profiles ...ProfileConfig) *harness {
	t.Helper()
	storage, err := modulestore.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	h := &harness{t: t, host: &host{storage: storage}, llm: map[string]*provider{}, now: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)}
	if len(profiles) == 0 {
		profiles = []ProfileConfig{{Name: "otogi", Label: "Otogi", Keywords: []string{"otogi", "小鸟"}, Chats: []string{group}, Provider: "otogi", Model: "m", System: "Be kind."}}
	}
	for _, p := range profiles {
		h.llm[p.Provider] = &provider{responses: []llm.Response{answer("hello")}}
	}
	h.m, err = New(Config{Profiles: profiles}, func(name string) (llm.Provider, error) {
		if p, ok := h.llm[name]; ok {
			return p, nil
		}
		return nil, fmt.Errorf("provider %q: %w", name, otogi.ErrInvalid)
	})
	if err != nil {
		t.Fatal(err)
	}
	h.m.now = func() time.Time { return h.now }
	if err := h.m.Start(context.Background(), h.host); err != nil {
		t.Fatal(err)
	}
	return h
}

// message is a new incoming group message from actor.
func (h *harness) message(actor, text string, mods ...func(*otogi.Event)) otogi.Event {
	h.seq++
	h.now = h.now.Add(time.Minute)
	e := otogi.Event{
		ID: otogi.EventID(fmt.Sprintf("e%d", h.seq)), Platform: "telegram", Account: "main",
		Conversation: otogi.Conversation{ID: "chat:1", Kind: otogi.ConversationGroup, Title: "Friends"},
		SourceID:     otogi.SourceEventID(fmt.Sprintf("m%d", h.seq)), Actor: otogi.Actor{ID: otogi.ActorID("user:" + actor), Name: actor},
		Action: otogi.ActionMessageCreated, Direction: otogi.DirectionIncoming, OccurredAt: h.now, Message: otogi.TextMessage(text),
	}
	for _, mod := range mods {
		mod(&e)
	}
	return e
}

func replyingTo(source string) func(*otogi.Event) {
	return func(e *otogi.Event) {
		e.Message.Relations = []otogi.Relation{{Kind: otogi.RelationReply, Target: otogi.EventRef{Platform: "telegram", Account: "main", Conversation: "chat:1", SourceEvent: otogi.SourceEventID(source)}}}
	}
}

func (h *harness) handle(e otogi.Event) error {
	return h.m.Handle(context.Background(), e, invocation{h: h})
}

func (h *harness) mustHandle(e otogi.Event) {
	h.t.Helper()
	if err := h.handle(e); err != nil {
		h.t.Fatal(err)
	}
}

func lastUserText(req llm.Request) string {
	return req.Turns[len(req.Turns)-1].Content.PlainText()
}

func TestKeywords(t *testing.T) {
	t.Parallel()
	p := &profile{keywords: []string{"Otogi", "小鸟"}}
	for text, want := range map[string]bool{
		"otogi, hi": true, "  OTOGI": true, "Otogi 你好": true, "otogi？": true, "小鸟今天天气": true, "小鸟": true,
		"otogiri": false, "hi otogi": false, "小": false, "": false,
	} {
		if got := p.keyword(text); got != want {
			t.Errorf("keyword(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestKeywordStartsAThreadAndRepliesContinueIt(t *testing.T) {
	h := newHarness(t)
	p := h.llm["otogi"]
	p.responses = []llm.Response{answer("Hi Alice!"), answer("Sure.")}

	h.mustHandle(h.message("Alice", "otogi, hi"))
	if len(p.requests) != 1 {
		t.Fatalf("%d requests", len(p.requests))
	}
	first := p.requests[0]
	if !strings.HasPrefix(first.System, "Be kind.") || !strings.Contains(first.System, "You are otogi, talking in a group chat called Friends on telegram") {
		t.Errorf("system %q", first.System)
	}
	if len(first.Turns) != 1 || lastUserText(first) != "Alice: otogi, hi" {
		t.Fatalf("turns %+v", first.Turns)
	}
	s := h.streams[0]
	if s.final.PlainText() != "Otogi\nHi Alice!" || s.final.Parts[0].Annotations[0].Kind != otogi.AnnotationBold || s.final.Relations[0].Target.SourceEvent != "m1" {
		t.Fatalf("reply %q %+v", s.final.PlainText(), s.final)
	}

	h.mustHandle(h.message("Bob", "and you?", replyingTo("agent-1")))
	second := p.requests[1]
	if len(second.Turns) != 3 || second.Turns[0].Content.PlainText() != "Alice: otogi, hi" || second.Turns[1].Role != llm.RoleModel || lastUserText(second) != "Bob: and you?" {
		t.Fatalf("second turns %+v", second.Turns)
	}
	if r := h.host.records[len(h.host.records)-1]; r.Kind != "chat.reply" || r.Payload.(map[string]any)["trigger"] != "reply" {
		t.Fatalf("record %+v", r)
	}
}

func TestBranchingAndAnotherAgentsKeyword(t *testing.T) {
	h := newHarness(t,
		ProfileConfig{Name: "a", Keywords: []string{"alpha"}, Chats: []string{group}, Provider: "a", Model: "m"},
		ProfileConfig{Name: "b", Keywords: []string{"beta"}, Chats: []string{group}, Provider: "b", Model: "m"},
	)
	h.mustHandle(h.message("Alice", "alpha one"))                  // agent-1
	h.mustHandle(h.message("Alice", "two", replyingTo("agent-1"))) // agent-2
	h.mustHandle(h.message("Alice", "again", replyingTo("agent-1")))
	if got := h.llm["a"].requests[2]; len(got.Turns) != 3 || !strings.HasSuffix(lastUserText(got), "Alice: again") || got.Turns[0].Content.PlainText() != "Alice: alpha one" {
		t.Fatalf("branch from agent-1: %+v", got.Turns)
	}
	h.mustHandle(h.message("Alice", "beta what did alpha say?", replyingTo("agent-2")))
	if b := h.llm["b"].requests; len(b) != 1 || len(b[0].Turns) != 1 || !strings.Contains(lastUserText(b[0]), "beta what did alpha say?") {
		t.Fatalf("beta: %+v", b)
	}
}

func TestWhatDoesNotTrigger(t *testing.T) {
	h := newHarness(t)
	for name, e := range map[string]otogi.Event{
		"no keyword":      h.message("Alice", "hello all"),
		"outgoing":        h.message("Me", "otogi hi", func(e *otogi.Event) { e.Direction = otogi.DirectionOutgoing }),
		"edited":          h.message("Alice", "otogi hi", func(e *otogi.Event) { e.Action = otogi.ActionMessageEdited }),
		"command":         h.message("Alice", "otogi hi", func(e *otogi.Event) { e.Command = &otogi.Command{Name: "note"} }),
		"other chat":      h.message("Alice", "otogi hi", func(e *otogi.Event) { e.Conversation.ID = "chat:2" }),
		"reply to person": h.message("Alice", "nice", replyingTo("m1")),
	} {
		if err := h.handle(e); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if n := len(h.llm["otogi"].requests); n != 0 {
		t.Fatalf("%d requests", n)
	}
	lines, err := h.m.store.recent(context.Background(), otogi.Destination{Platform: "telegram", Account: "main", Conversation: "chat:1"}, h.now.Add(time.Hour), 10)
	if err != nil || len(lines) != 5 { // all but the other chat's
		t.Fatalf("transcript %d lines, %v", len(lines), err)
	}
}

func TestContextIsTheRecentChat(t *testing.T) {
	h := newHarness(t)
	h.mustHandle(h.message("Bob", "the **meeting** moved to 3pm"))
	h.mustHandle(h.message("Me", "thanks", func(e *otogi.Event) { e.Direction = otogi.DirectionOutgoing }))
	photo := otogi.Part{ID: "p", Kind: otogi.PartMedia, Media: &otogi.Media{Role: otogi.MediaImage, Source: otogi.MediaSource{Kind: otogi.MediaSourceOpaque, Locator: otogi.ProviderLocator{Data: []byte("photo")}}}}
	h.mustHandle(h.message("Bob", "", func(e *otogi.Event) {
		e.Message = otogi.Message{Roots: []otogi.PartID{"p"}, Parts: []otogi.Part{photo}}
	}))
	h.mustHandle(h.message("Alice", "otogi, when is the meeting? and what is this?", replyingTo("m3")))

	req := h.llm["otogi"].requests[0]
	text := lastUserText(req)
	at := func(minute int) string {
		return time.Date(2026, 10, 31, 12, minute, 0, 0, time.UTC).In(time.Local).Format("15:04")
	}
	for _, want := range []string{
		"Recent messages in this chat, oldest first, for context:\n[" + at(1) + "] Bob: the **meeting** moved to 3pm\n[" + at(2) + "] Me (this account): thanks\n[" + at(3) + "] Bob: [photo]",
		"The message to answer:\n\nReplying to Bob:\n",
		"Alice: otogi, when is the meeting?",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("request lacks %q:\n%s", want, text)
		}
	}
	var media []string
	for _, p := range req.Turns[0].Content.Parts {
		if p.Media != nil {
			media = append(media, string(p.Media.Source.Inline))
		}
	}
	if len(media) != 1 || media[0] != "bytes of photo" {
		t.Fatalf("media %q", media)
	}
}

func TestEndings(t *testing.T) {
	for name, c := range map[string]struct {
		resp  llm.Response
		err   error
		want  string
		store bool
	}{
		"error":      {err: fmt.Errorf("gemini: HTTP 503: %w", otogi.ErrUnavailable), want: "Otogi\nSorry, I couldn't reply: the model is busy or unreachable, try again in a minute."},
		"blocked":    {resp: llm.Response{Turn: llm.Turn{Role: llm.RoleModel}, Finish: llm.FinishBlocked, Reason: "SAFETY"}, want: "Otogi\nI can't answer that (SAFETY).", store: true},
		"max tokens": {resp: llm.Response{Turn: answer("long story").Turn, Finish: llm.FinishMaxTokens}, want: "Otogi\nlong story…", store: true},
		"sources":    {resp: llm.Response{Turn: answer("Kane won.").Turn, Finish: llm.FinishStop, Sources: []llm.Source{{Title: "uefa.com", URL: "https://u"}}}, want: "Otogi\nKane won.\n\nSources: uefa.com", store: true},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			p := h.llm["otogi"]
			p.responses, p.errs = []llm.Response{c.resp}, []error{c.err}
			err := h.handle(h.message("Alice", "otogi?"))
			if (c.err != nil) != (err != nil) {
				t.Fatalf("err %v", err)
			}
			if got := h.streams[0].final.PlainText(); got != c.want {
				t.Fatalf("reply %q, want %q", got, c.want)
			}
			_, _, found, _ := h.m.store.agentTurn(context.Background(), otogi.EventRef{Platform: "telegram", Account: "main", Conversation: "chat:1", SourceEvent: "agent-1"})
			if found != c.store {
				t.Fatalf("thread stored %v, want %v", found, c.store)
			}
		})
	}
}

func TestRetention(t *testing.T) {
	h := newHarness(t)
	h.mustHandle(h.message("Alice", "otogi hi"))
	h.now = h.now.Add(31 * 24 * time.Hour)
	if err := h.m.prune(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	h.mustHandle(h.message("Alice", "still there?", replyingTo("agent-1")))
	if n := len(h.llm["otogi"].requests); n != 1 {
		t.Fatalf("a reply to a pruned thread triggered: %d requests", n)
	}
}

func TestConfigErrors(t *testing.T) {
	t.Parallel()
	providers := func(name string) (llm.Provider, error) {
		if name == "gemini" {
			return &provider{}, nil
		}
		return nil, errors.New("not configured")
	}
	base := ProfileConfig{Name: "a", Keywords: []string{"a"}, Chats: []string{group}, Provider: "gemini", Model: "m"}
	for name, c := range map[string]struct {
		profiles []ProfileConfig
		want     string
	}{
		"no profiles":     {nil, "at least one"},
		"shared keyword":  {[]ProfileConfig{base, {Name: "b", Keywords: []string{"A"}, Chats: []string{group}, Provider: "gemini", Model: "m"}}, `"A" also belongs to profile "a"`},
		"unknown tool":    {[]ProfileConfig{{Name: "a", Keywords: []string{"a"}, Chats: []string{group}, Provider: "gemini", Model: "m", Tools: []string{"maps"}}}, `unknown tool "maps"`},
		"no provider":     {[]ProfileConfig{{Name: "a", Keywords: []string{"a"}, Chats: []string{group}, Provider: "openai", Model: "m"}}, "not configured"},
		"bad chat":        {[]ProfileConfig{{Name: "a", Keywords: []string{"a"}, Chats: []string{"123"}, Provider: "gemini", Model: "m"}}, "<platform>:<chat id>"},
		"bad thinking":    {[]ProfileConfig{{Name: "a", Keywords: []string{"a"}, Chats: []string{group}, Provider: "gemini", Model: "m", Thinking: "max"}}, "thinking"},
		"duplicate names": {[]ProfileConfig{base, {Name: "a", Keywords: []string{"b"}, Chats: []string{group}, Provider: "gemini", Model: "m"}}, "used twice"},
	} {
		if _, err := New(Config{Profiles: c.profiles}, providers); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err %v, want %q", name, err, c.want)
		}
	}
}

func TestPrivateChatsAndDeletedMessages(t *testing.T) {
	h := newHarness(t, ProfileConfig{Name: "otogi", Keywords: []string{"otogi"}, Chats: []string{"telegram:user:7"}, Provider: "p", Model: "m"})
	private := func(e *otogi.Event) {
		e.Conversation = otogi.Conversation{ID: "user:7", Kind: otogi.ConversationDirect}
	}
	secret := h.message("Alice", "my password is hunter2", private)
	h.mustHandle(secret)
	h.mustHandle(otogi.Event{Platform: "telegram", Account: "main", Action: otogi.ActionMessageDeleted, Direction: otogi.DirectionSystem, SourceID: secret.SourceID})
	h.mustHandle(h.message("Alice", "otogi, what did I say?", private))
	text := lastUserText(h.llm["p"].requests[0])
	if text != "otogi, what did I say?" {
		t.Fatalf("request %q", text)
	}
}
