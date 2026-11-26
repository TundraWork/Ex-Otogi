package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/TundraWork/Ex-Otogi/internal/settingstest"
	"log/slog"
	"slices"
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
	before    func(n int) // when set, runs before the nth request is answered, and may block it
}

func (p *provider) Generate(_ context.Context, req llm.Request, partial func(otogi.Message) error) (llm.Response, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	n := len(p.requests)
	p.mu.Unlock()
	if p.before != nil {
		p.before(n)
	}
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

// stream keeps what a reply showed, and the message it sent.
type stream struct {
	h       *harness
	updates []string
	final   otogi.Message
	ref     otogi.EventRef
}

func (s *stream) Update(_ context.Context, msg otogi.Message) error {
	s.updates = append(s.updates, msg.PlainText())
	return nil
}

func (s *stream) Close(_ context.Context, msg otogi.Message) ([]otogi.EventRef, error) {
	s.h.mu.Lock()
	defer s.h.mu.Unlock()
	s.final = msg
	s.h.sent++
	s.ref = otogi.EventRef{Platform: "telegram", Account: "main", Conversation: "chat:1", SourceEvent: otogi.SourceEventID(fmt.Sprintf("agent-%d", s.h.sent))}
	return []otogi.EventRef{s.ref}, nil
}

// echo hands the message a reply sent back to the module as the platform
// does: the account's message, as it was sent, at mods' time or else a
// minute after the last message.
func (h *harness) echo(s *stream, mods ...func(*otogi.Event)) {
	h.t.Helper()
	h.mustHandle(h.message("Me", "", append([]func(*otogi.Event){func(e *otogi.Event) {
		e.SourceID, e.Direction, e.Message = s.ref.SourceEvent, otogi.DirectionOutgoing, s.final
	}}, mods...)...))
}

type invocation struct {
	otogi.Invocation
	h *harness
}

func (i invocation) Send(_ context.Context, slot string, msg otogi.Message) (otogi.EventRef, error) {
	i.h.mu.Lock()
	defer i.h.mu.Unlock()
	i.h.replies = append(i.h.replies, msg.PlainText())
	if i.h.sendErr != nil {
		return otogi.EventRef{}, i.h.sendErr
	}
	i.h.sent++
	i.h.sends = append(i.h.sends, sentEffect{slot: slot, msg: msg})
	return otogi.EventRef{Platform: "telegram", Account: "main", Conversation: "chat:1", SourceEvent: otogi.SourceEventID(fmt.Sprintf("agent-%d", i.h.sent))}, nil
}

// sentEffect is a message sent with Send, and its slot.
type sentEffect struct {
	slot string
	msg  otogi.Message
}

func (i invocation) Stream(context.Context, string, otogi.Destination) otogi.MessageStream {
	s := &stream{h: i.h}
	i.h.mu.Lock()
	i.h.streams = append(i.h.streams, s)
	i.h.mu.Unlock()
	return s
}

func (i invocation) FetchMedia(_ context.Context, _ otogi.Destination, src otogi.MediaSource) ([]byte, error) {
	if i.h.fetchErr != nil {
		return nil, i.h.fetchErr
	}
	return []byte("bytes of " + string(src.Locator.Data)), nil
}

type host struct {
	otogi.Host
	mu       sync.Mutex
	storage  *modulestore.Storage
	records  []otogi.Record
	services map[string]any
	lists    map[[2]string]string // list, chat → who added it
	recorded func(otogi.Record)   // when set, sees each record as it is written
}

func (h *host) Allowed(list, chat string) bool {
	_, ok := h.lists[[2]string{list, chat}]
	return ok
}

func (h *host) Service(name string) (any, bool) {
	s, ok := h.services[name]
	return s, ok
}
func (h *host) Module() otogi.ModuleID { return "chat" }

func (h *host) Logger() *slog.Logger { return slog.New(slog.DiscardHandler) }
func (h *host) Database(ctx context.Context) (*sql.DB, error) {
	return h.storage.Database(ctx, "chat")
}
func (h *host) Record(_ context.Context, r otogi.Record) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	if h.recorded != nil {
		h.recorded(r)
	}
}
func (h *host) Schedule(string, string, otogi.Job) error { return nil }

type harness struct {
	t        *testing.T
	mu       sync.Mutex // guards what the invocation and streams record, for handlers running at once
	m        *Module
	host     *host
	llm      map[string]*provider
	streams  []*stream
	replies  []string // sent with Send: command replies
	sends    []sentEffect
	sendErr  error // when set, Send fails with it
	fetchErr error // when set, FetchMedia fails with it
	sent     int
	now      time.Time
	seq      int
}

const group = "telegram:chat:1"

func newHarness(t *testing.T, profiles ...ProfileConfig) *harness {
	t.Helper()
	return newHarnessWith(t, nil, profiles...)
}

// newHarnessWith starts the module on a host offering services.
func newHarnessWith(t *testing.T, services map[string]any, profiles ...ProfileConfig) *harness {
	t.Helper()
	return newHarnessConfig(t, services, Config{Profiles: profiles})
}

// newHarnessConfig starts the module with c on a host offering services.
func newHarnessConfig(t *testing.T, services map[string]any, c Config) *harness {
	t.Helper()
	storage, err := modulestore.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	h := &harness{t: t, host: &host{storage: storage, lists: map[[2]string]string{}, services: services}, llm: map[string]*provider{}, now: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)}
	if len(c.Profiles) == 0 {
		c.Profiles = []ProfileConfig{{Name: "otogi", Label: "Otogi", Keywords: []string{"otogi", "小鸟"}, Provider: "otogi", Model: "m", System: "Be kind."}}
	}
	for _, p := range c.Profiles {
		h.llm[p.Provider] = &provider{responses: []llm.Response{answer("hello")}}
		h.host.lists[[2]string{p.Name, group}] = "test"
	}
	h.m, err = New(c, func(name string) (llm.Provider, error) {
		if p, ok := h.llm[name]; ok {
			return p, nil
		}
		return nil, fmt.Errorf("provider %q: %w", name, otogi.ErrInvalid)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.m.now = h.clock
	if err := h.m.Start(context.Background(), h.host); err != nil {
		t.Fatal(err)
	}
	return h
}

// clock is the module's now: the harness's, which messages move on while
// handlers may be reading it.
func (h *harness) clock() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.now
}

// message is a new incoming group message from actor.
func (h *harness) message(actor, text string, mods ...func(*otogi.Event)) otogi.Event {
	h.mu.Lock()
	h.seq++
	h.now = h.now.Add(time.Minute)
	seq, now := h.seq, h.now
	h.mu.Unlock()
	e := otogi.Event{
		ID: otogi.EventID(fmt.Sprintf("e%d", seq)), Platform: "telegram", Account: "main",
		Conversation: otogi.Conversation{ID: "chat:1", Kind: otogi.ConversationGroup, Title: "Friends"},
		SourceID:     otogi.SourceEventID(fmt.Sprintf("m%d", seq)), Actor: otogi.Actor{ID: otogi.ActorID("user:" + actor), Name: actor},
		Action: otogi.ActionMessageCreated, Direction: otogi.DirectionIncoming, OccurredAt: now, Message: otogi.TextMessage(text),
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
		ProfileConfig{Name: "a", Keywords: []string{"alpha"}, Provider: "a", Model: "m"},
		ProfileConfig{Name: "b", Keywords: []string{"beta"}, Provider: "b", Model: "m"},
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
			_, _, _, found, _ := h.m.store.agentTurn(context.Background(), otogi.EventRef{Platform: "telegram", Account: "main", Conversation: "chat:1", SourceEvent: "agent-1"})
			if found != c.store {
				t.Fatalf("thread stored %v, want %v", found, c.store)
			}
		})
	}
}

// An agent with sources off ends a grounded reply with what the model
// wrote, and its record still has the pages.
func TestSourcesOff(t *testing.T) {
	off := false
	h := newHarness(t, ProfileConfig{Name: "otogi", Label: "Otogi", Keywords: []string{"otogi"}, Provider: "otogi", Model: "m", Sources: &off})
	cited := []llm.Source{{Title: "uefa.com", URL: "https://u"}}
	h.llm["otogi"].responses = []llm.Response{{Turn: answer("Kane won.").Turn, Finish: llm.FinishStop, Sources: cited}}
	h.mustHandle(h.message("Alice", "otogi, who won?"))
	if got := h.streams[0].final.PlainText(); got != "Otogi\nKane won." {
		t.Fatalf("reply %q, want no sources", got)
	}
	r := h.host.records[len(h.host.records)-1]
	if got, _ := r.Payload.(map[string]any)["sources"].([]llm.Source); r.Kind != "chat.reply" || !slices.Equal(got, cited) {
		t.Fatalf("record %s %+v, want the sources", r.Kind, r.Payload)
	}
}

// An agent with failure_notes direct_chats says how a reply failed only in
// direct chats. In a group, it sends what the model wrote and nothing else;
// a batch it sends nothing for is not kept, and its record says why.
func TestFailureNotesInGroups(t *testing.T) {
	busy := fmt.Errorf("gemini: HTTP 503: %w", otogi.ErrUnavailable)
	lookUp := llm.Response{Turn: llm.Turn{Role: llm.RoleModel, Content: otogi.TextMessage("Let me look."), Calls: []llm.Call{{ID: "c1", Name: "look", Args: json.RawMessage(`{}`)}}}, Finish: llm.FinishToolCalls}
	for name, c := range map[string]struct {
		resps   []llm.Response
		errs    []error
		direct  bool
		shown   []string // what each stream showed, in order
		skipped string   // the record's, when it is skipped
	}{
		"error":        {errs: []error{busy}, shown: []string{""}},
		"cut short":    {resps: []llm.Response{lookUp}, errs: []error{nil, busy}, shown: []string{"Otogi\nLet me look."}},
		"blocked":      {resps: []llm.Response{{Turn: llm.Turn{Role: llm.RoleModel}, Finish: llm.FinishBlocked, Reason: "SAFETY"}}, shown: []string{""}, skipped: "no_answer"},
		"nothing":      {resps: []llm.Response{{Turn: llm.Turn{Role: llm.RoleModel}, Finish: llm.FinishStop}}, shown: []string{""}, skipped: "no_answer"},
		"direct error": {errs: []error{busy}, direct: true, shown: []string{"Otogi\nSorry, I couldn't reply: the model is busy or unreachable, try again in a minute."}},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, ProfileConfig{Name: "otogi", Label: "Otogi", Keywords: []string{"otogi"}, Provider: "otogi", Model: "m", FailureNotes: "direct_chats"})
			p := h.llm["otogi"]
			p.responses, p.errs = c.resps, c.errs
			e := h.message("Alice", "otogi?")
			if c.direct {
				e.Conversation = otogi.Conversation{ID: "user:7", Kind: otogi.ConversationDirect}
				h.host.lists[[2]string{"otogi", "telegram:user:7"}] = "test"
			}
			err := h.handle(e)
			if (len(c.errs) > 0) != (err != nil) {
				t.Fatalf("err %v", err)
			}
			var shown []string
			for _, s := range h.streams {
				shown = append(shown, s.final.PlainText())
			}
			if !slices.Equal(shown, c.shown) {
				t.Fatalf("shown %q, want %q", shown, c.shown)
			}
			r, payload := lastReplyRecord(t, h)
			if c.skipped != "" && (r.Outcome != "skipped" || payload["skipped"] != c.skipped) {
				t.Fatalf("record %s %+v, want skipped %s", r.Outcome, payload, c.skipped)
			}
			if c.skipped != "" {
				if _, _, _, found, _ := h.m.store.agentTurn(context.Background(), h.streams[0].ref); found {
					t.Fatal("a batch nothing was sent for was kept")
				}
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
	base := ProfileConfig{Name: "a", Keywords: []string{"a"}, Provider: "gemini", Model: "m"}
	for name, c := range map[string]struct {
		profiles []ProfileConfig
		want     string
	}{
		"no profiles":                   {nil, "at least one"},
		"shared keyword":                {[]ProfileConfig{base, {Name: "b", Keywords: []string{"A"}, Provider: "gemini", Model: "m"}}, `"A" also belongs to profile "a"`},
		"module tool on a profile":      {[]ProfileConfig{{Name: "a", Keywords: []string{"a"}, Provider: "gemini", Model: "m", Tools: []string{"todou"}}}, `"todou": want a built-in`},
		"profile named as a tools list": {[]ProfileConfig{{Name: "tools:todou", Keywords: []string{"a"}, Provider: "gemini", Model: "m"}}, "names starting with tools:"},
		"no provider":                   {[]ProfileConfig{{Name: "a", Keywords: []string{"a"}, Provider: "openai", Model: "m"}}, "not configured"},
		"bad thinking":                  {[]ProfileConfig{{Name: "a", Keywords: []string{"a"}, Provider: "gemini", Model: "m", Thinking: "max"}}, "thinking"},
		"duplicate names":               {[]ProfileConfig{base, {Name: "a", Keywords: []string{"b"}, Provider: "gemini", Model: "m"}}, "used twice"},
		"hot":                           {[]ProfileConfig{{Name: "a", Keywords: []string{"a"}, Provider: "gemini", Model: "m", Temperature: ptr(2.5)}}, "temperature: 2.5: want 0 to 2"},
		"sticker number 0":              {[]ProfileConfig{{Name: "a", Keywords: []string{"a"}, Provider: "gemini", Model: "m", Media: &MediaConfig{Stickers: []int{1, 0}}}}, "media.stickers[1]: 0: want a sticker's number"},
		"sticker pack ID":               {[]ProfileConfig{{Name: "a", Keywords: []string{"a"}, Provider: "gemini", Model: "m", Media: &MediaConfig{Packs: []string{"eku", "Eku Memes"}}}}, `media.packs[1]: "Eku Memes": want a sticker pack's ID`},
		"max_stickers 0":                {[]ProfileConfig{{Name: "a", Keywords: []string{"a"}, Provider: "gemini", Model: "m", Media: &MediaConfig{Packs: []string{"eku"}, MaxStickers: ptr(0)}}}, "media.max_stickers: must be at least 1"},
	} {
		if _, err := New(Config{Profiles: c.profiles}, providers, nil); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err %v, want %q", name, err, c.want)
		}
	}
	for tools, want := range map[string]string{"web_search": "a built-in tool: set it on the profiles", "a b": "want a module ID", "todou,todou": "listed twice"} {
		if _, err := New(Config{Profiles: []ProfileConfig{base}, Tools: strings.Split(tools, ",")}, providers, nil); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("tools %s: err %v, want %q", tools, err, want)
		}
	}
}

// A bot's message is kept for context, but does not trigger an agent, even
// starting with its keyword: two bots must not answer each other forever.
func TestBotsDoNotTrigger(t *testing.T) {
	h := newHarness(t)
	h.mustHandle(h.message("Sarasa", "otogi, x.com/a/status/1", func(e *otogi.Event) { e.Actor.Bot = true }))
	if n := len(h.llm["otogi"].requests); n != 0 {
		t.Fatalf("a bot triggered %d requests", n)
	}
	h.mustHandle(h.message("Alice", "otogi, what did Sarasa post?"))
	if text := lastUserText(h.llm["otogi"].requests[0]); !strings.Contains(text, "Sarasa: otogi, x.com/a/status/1") {
		t.Fatalf("the bot's message is not in the context:\n%s", text)
	}
}

func TestPrivateChatsAndDeletedMessages(t *testing.T) {
	h := newHarness(t, ProfileConfig{Name: "otogi", Keywords: []string{"otogi"}, Provider: "p", Model: "m"})
	h.host.lists = map[[2]string]string{{"otogi", "telegram:user:7"}: "test"}
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

// A module's tools are offered in the chats on its tools list, and only
// there; built-in tools go wherever the agent answers.
func TestToolsPerChat(t *testing.T) {
	storage, err := modulestore.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	var seen otogi.Event
	echo := llm.NewTool(llm.Function{Name: "shout", Description: "shouts"}, func(_ context.Context, e otogi.Event, args json.RawMessage) (any, error) {
		seen = e
		return strings.ToUpper(string(args)), nil
	})
	h := &harness{t: t, host: &host{storage: storage, services: map[string]any{"llm.tools/fun": []llm.Tool{echo}}, lists: map[[2]string]string{{"otogi", group}: "test", {"otogi", "telegram:user:7"}: "test", {"tools:fun", group}: "test"}}, llm: map[string]*provider{}, now: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)}
	p := &provider{responses: []llm.Response{
		{Turn: llm.Turn{Role: llm.RoleModel, Calls: []llm.Call{{ID: "c1", Name: "shout", Args: json.RawMessage(`"hi"`)}}}, Finish: llm.FinishToolCalls},
		answer("Shouted."),
	}}
	h.llm["p"] = p
	h.m, err = New(Config{Tools: []string{"fun"}, Profiles: []ProfileConfig{{Name: "otogi", Keywords: []string{"otogi"}, Provider: "p", Model: "m", Tools: []string{"web_search"}}}},
		func(string) (llm.Provider, error) { return p, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := h.m.Descriptor()
	if len(d.Requires) != 1 || d.Requires[0] != "llm.tools/fun" || len(d.Allowlists) != 2 ||
		d.Allowlists[1] != (otogi.AllowlistSpec{Name: "tools:fun", Title: "fun tools", Description: "Agents may use the fun module's tools here.", About: "fun"}) {
		t.Fatalf("requires %v, allowlists %+v", d.Requires, d.Allowlists)
	}
	h.m.now = h.clock
	if err := h.m.Start(context.Background(), h.host); err != nil {
		t.Fatal(err)
	}
	ask := h.message("Alice", "otogi shout hi")
	h.mustHandle(ask)
	if len(p.requests) != 2 || len(p.requests[0].Tools) != 2 || p.requests[0].Tools[1].Function.Name != "shout" || seen.ID != ask.ID {
		t.Fatalf("requests %d, tools %+v, tool saw %q", len(p.requests), p.requests[0].Tools, seen.ID)
	}
	var kinds []string
	for _, r := range h.host.records {
		kinds = append(kinds, r.Kind)
		if r.Kind == "chat.tool" && string(r.Payload.(map[string]any)["result"].(json.RawMessage)) != `"\"HI\""` {
			t.Errorf("tool record %+v", r.Payload)
		}
	}
	if strings.Join(kinds, ",") != "chat.tool,chat.reply" {
		t.Fatalf("records %v", kinds)
	}
	// The thread keeps the call and its result, so a follow-up resends them.
	h.mustHandle(h.message("Alice", "again", replyingTo("agent-1")))
	if turns := p.requests[2].Turns; len(turns) != 5 || turns[2].Role != llm.RoleTool {
		t.Fatalf("follow-up turns %+v", turns)
	}
	// In a chat not on the tools list, the agent has its built-ins alone.
	p.responses = []llm.Response{answer("Hi.")}
	h.mustHandle(h.message("Alice", "otogi shout hi", func(e *otogi.Event) {
		e.Conversation = otogi.Conversation{ID: "user:7", Kind: otogi.ConversationDirect}
	}))
	if tools := p.requests[len(p.requests)-1].Tools; len(tools) != 1 || tools[0].Builtin != llm.BuiltinWebSearch {
		t.Fatalf("tools in a chat without fun's %+v", tools)
	}
}

// What the model first says before calling tools goes out as its own message
// before the tools run; the rest follows in one more, and replying to either
// continues the thread. When it says nothing after the tools, there is no
// second message.
func TestWordsBeforeToolsGoOutFirst(t *testing.T) {
	var shownWhenCalled int
	var h *harness
	shout := llm.NewTool(llm.Function{Name: "shout", Description: "shouts"}, func(context.Context, otogi.Event, json.RawMessage) (any, error) {
		shownWhenCalled = h.sent
		return "HI", nil
	})
	storage, err := modulestore.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	h = &harness{t: t, host: &host{storage: storage, services: map[string]any{"llm.tools/fun": []llm.Tool{shout}}, lists: map[[2]string]string{{"otogi", group}: "test", {"tools:fun", group}: "test"}}, llm: map[string]*provider{}, now: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)}
	call := llm.Response{Turn: llm.Turn{Role: llm.RoleModel, Content: otogi.TextMessage("Let me shout."), Calls: []llm.Call{{ID: "c1", Name: "shout", Args: json.RawMessage(`"hi"`)}}}, Finish: llm.FinishToolCalls}
	p := &provider{responses: []llm.Response{call, answer("Shouted.")}}
	h.llm["otogi"] = p
	if h.m, err = New(Config{Tools: []string{"fun"}, Profiles: []ProfileConfig{{Name: "otogi", Keywords: []string{"otogi"}, Provider: "otogi", Model: "m"}}}, func(string) (llm.Provider, error) { return p, nil }, nil); err != nil {
		t.Fatal(err)
	}
	h.m.now = h.clock
	if err := h.m.Start(context.Background(), h.host); err != nil {
		t.Fatal(err)
	}

	h.mustHandle(h.message("Alice", "otogi shout hi"))
	if len(h.streams) != 2 || h.streams[0].final.PlainText() != "Let me shout." || h.streams[1].final.PlainText() != "Shouted." || shownWhenCalled != 1 {
		t.Fatalf("streams %d, sent before the tool ran %d", len(h.streams), shownWhenCalled)
	}
	if got := h.host.records[len(h.host.records)-1].Payload.(map[string]any)["messages"].([]otogi.EventRef); len(got) != 2 {
		t.Fatalf("recorded messages %v", got)
	}
	for _, source := range []string{"agent-1", "agent-2"} {
		before := len(p.requests)
		h.mustHandle(h.message("Alice", "and?", replyingTo(source)))
		if len(p.requests) != before+1 || len(p.requests[before].Turns) != 5 {
			t.Fatalf("a reply to %s did not continue the thread: %+v", source, p.requests[before:])
		}
	}

	// Words before a later call stay with the answer: two messages at most.
	again := call
	again.Turn.Content, again.Turn.Calls = otogi.TextMessage("Once more."), []llm.Call{{ID: "c2", Name: "shout", Args: json.RawMessage(`"hi"`)}}
	p.responses, p.requests = []llm.Response{call, again, answer("Shouted twice.")}, nil
	h.streams = nil
	h.mustHandle(h.message("Alice", "otogi shout twice"))
	if len(h.streams) != 2 || h.streams[0].final.PlainText() != "Let me shout." || h.streams[1].final.PlainText() != "Once more.\n\nShouted twice." {
		t.Fatalf("streams %d, last %q", len(h.streams), h.streams[len(h.streams)-1].final.PlainText())
	}

	// Nothing said after the tools: no second message, none recorded.
	p.responses, p.requests = []llm.Response{call, {Turn: llm.Turn{Role: llm.RoleModel}, Finish: llm.FinishStop}}, nil
	h.streams = nil
	h.mustHandle(h.message("Alice", "otogi shout again"))
	if len(h.streams) != 1 || h.streams[0].final.PlainText() != "Let me shout." {
		t.Fatalf("streams %d, last %q", len(h.streams), h.streams[len(h.streams)-1].final.PlainText())
	}
	if got := h.host.records[len(h.host.records)-1].Payload.(map[string]any)["messages"].([]otogi.EventRef); len(got) != 1 {
		t.Fatalf("recorded messages %v", got)
	}
}

// A profile with mention: true answers a message that mentions the account,
// and the @ itself is left out of the prompt; without the flag, a mention
// does not trigger.
func TestMentionTrigger(t *testing.T) {
	h := newHarness(t,
		ProfileConfig{Name: "luna", Keywords: []string{"luna"}, Mention: true, Provider: "luna", Model: "m"},
		ProfileConfig{Name: "otogi", Keywords: []string{"otogi"}, Provider: "otogi", Model: "m"})
	p := h.llm["luna"]
	p.responses = []llm.Response{answer("Meow.")}
	mentioned := func(e *otogi.Event) {
		e.Mentioned = true
		e.Message.Parts[0].Annotations = []otogi.Annotation{{Offset: 0, Length: 6, Kind: otogi.AnnotationMention, Target: "user:B1"}}
	}
	h.mustHandle(h.message("Alice", "@Luna  draw a cat", mentioned))
	if len(p.requests) != 1 || lastUserText(p.requests[0]) != "Alice: draw a cat" {
		t.Fatalf("requests %+v", p.requests)
	}
	if r := h.host.records[len(h.host.records)-1]; r.Payload.(map[string]any)["trigger"] != "mention" {
		t.Fatalf("record %+v", r)
	}
	h.mustHandle(h.message("Alice", "hello", func(e *otogi.Event) { e.Mentioned = false }))
	if len(p.requests) != 1 || len(h.llm["otogi"].requests) != 0 {
		t.Fatal("an unmentioned message triggered an agent")
	}
}

// A reply to a message the account sent for another module, such as a card,
// is no mention: platforms flag it as one, and it addresses no agent. A reply
// to another person's message that mentions the account still is one, and
// so is a keyword on a reply to the card.
func TestReplyToAnotherModulesMessageIsNoMention(t *testing.T) {
	t.Parallel()
	h := newHarness(t, ProfileConfig{Name: "luna", Keywords: []string{"luna"}, Mention: true, Provider: "luna", Model: "m"})
	p := h.llm["luna"]
	p.responses = []llm.Response{answer("One."), answer("Two.")}
	mentioned := func(e *otogi.Event) { e.Mentioned = true }
	h.mustHandle(h.message("Me", "A post by someone", func(e *otogi.Event) { e.Direction = otogi.DirectionOutgoing })) // m1, a card
	h.mustHandle(h.message("Bob", "look at this"))                                                                     // m2

	h.mustHandle(h.message("Alice", "what does it say?", replyingTo("m1"), mentioned))
	if len(p.requests) != 0 {
		t.Fatalf("a reply to a card triggered the agent: %+v", p.requests)
	}
	h.mustHandle(h.message("Alice", "what about this?", replyingTo("m2"), mentioned))
	if len(p.requests) != 1 {
		t.Fatalf("a mention on a reply to a person made %d requests", len(p.requests))
	}
	h.mustHandle(h.message("Alice", "luna, what does it say?", replyingTo("m1"), mentioned))
	if r := h.host.records[len(h.host.records)-1]; len(p.requests) != 2 || r.Payload.(map[string]any)["trigger"] != "keyword" {
		t.Fatalf("a keyword on a reply to a card: %d requests, record %+v", len(p.requests), r)
	}
}

func ptr[T any](v T) *T { return &v }

// A character card comes from its file, and the temperature reaches the
// provider.
func TestSystemPromptAndTemperature(t *testing.T) {
	h := newHarness(t, ProfileConfig{Name: "Mizuki", Keywords: []string{"水月"}, Provider: "mizuki", Model: "m", System: "# Role: 水月\n\n称呼你为「博士」。", Temperature: ptr(1.2)})
	p := h.llm["mizuki"]
	p.responses = []llm.Response{answer("博士！")}
	h.mustHandle(h.message("Alice", "水月，你好"))
	req := p.requests[0]
	if !strings.HasPrefix(req.System, "# Role: 水月\n\n称呼你为「博士」。") || req.Temperature == nil || *req.Temperature != 1.2 {
		t.Fatalf("system %q, temperature %v", req.System, req.Temperature)
	}
}

// describe_wait.reply is at most half of timeout. Unset, it is 30s, or less
// with a timeout under a minute, so that settings saved before it still hold.
func TestDescribeWaitWithinTimeout(t *testing.T) {
	t.Parallel()
	providers := func(string) (llm.Provider, error) { return &provider{}, nil }
	profiles := []ProfileConfig{{Name: "luna", Keywords: []string{"luna"}, Provider: "p", Model: "m"}}
	for name, c := range map[string]struct {
		timeout, judge, reply time.Duration
		wantJudge, wantReply  time.Duration
		refused               string
	}{
		"defaults":                 {0, 0, 0, 15 * time.Second, 30 * time.Second, ""},
		"short timeout, unset":     {40 * time.Second, 0, 0, 15 * time.Second, 20 * time.Second, ""},
		"set within":               {time.Minute, 5 * time.Second, 30 * time.Second, 5 * time.Second, 30 * time.Second, ""},
		"set past half of timeout": {40 * time.Second, 0, 30 * time.Second, 0, 0, "describe_wait.reply"},
		"negative judge":           {0, -time.Second, 0, 0, 0, "describe_wait.judge"},
	} {
		cfg := Config{Timeout: otogi.Duration(c.timeout), DescribeWait: DescribeWait{Judge: otogi.Duration(c.judge), Reply: otogi.Duration(c.reply)}, Profiles: profiles}
		m, err := New(cfg, providers, nil)
		var se *otogi.SettingError
		switch {
		case c.refused != "" && (!errors.As(err, &se) || se.Path != c.refused):
			t.Errorf("%s: err %v, want a refusal at %s", name, err, c.refused)
		case c.refused == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case c.refused == "" && (time.Duration(m.cfg.DescribeWait.Judge) != c.wantJudge || time.Duration(m.cfg.DescribeWait.Reply) != c.wantReply):
			t.Errorf("%s: waits %+v", name, m.cfg.DescribeWait)
		}
	}
}

// The defaults the settings form shows are the ones an unset field gets.
func TestSchemaDefaultsAreTheDefaults(t *testing.T) {
	t.Parallel()
	providers := func(string) (llm.Provider, error) { return &provider{}, nil }
	unset := Config{Profiles: []ProfileConfig{
		{Name: "luna", Keywords: []string{"luna"}, Provider: "p", Model: "m", Notes: &NotesConfig{}, Media: &MediaConfig{Examine: &ExamineConfig{}, Packs: []string{"eku"}}},
		{Name: "mizuki", Keywords: []string{"mizuki"}, Provider: "p", Model: "m", Judge: &JudgeConfig{Wake: &WakeConfig{}, Stale: &StaleConfig{}, Wait: &WaitConfig{}, Chime: &ChimeConfig{}}},
	}, People: []PersonConfig{{User: "qq:user:x"}}}
	typed := settingstest.WithDefaults(t, unset)
	a, err := unset.profiles(providers)
	if err != nil {
		t.Fatal(err)
	}
	b, err := typed.profiles(providers)
	if err != nil {
		t.Fatal(err)
	}
	if unset.Retention != typed.Retention || unset.Timeout != typed.Timeout || *unset.ToolReserve != *typed.ToolReserve || unset.MaxMediaMB != typed.MaxMediaMB || unset.DescribeWait != typed.DescribeWait || unset.DescribedText != typed.DescribedText ||
		a[0].MaxTurns != b[0].MaxTurns || a[0].MaxToolRounds != b[0].MaxToolRounds || a[0].context != b[0].context || a[1].wake != b[1].wake ||
		a[1].stale != b[1].stale || a[1].chime != b[1].chime || a[1].still != b[1].still || a[1].waitMin != b[1].waitMin || a[1].waitMax != b[1].waitMax || a[0].note("follow") != b[0].note("follow") || a[0].note("chime") != b[0].note("chime") || a[0].examine != b[0].examine || a[0].maxStickers != b[0].maxStickers {
		t.Fatalf("schema defaults give %+v %+v wake %v, unset fields %+v %+v wake %v", typed, b[0], b[1].wake, unset, a[0], a[1].wake)
	}
	pa, err := unset.people()
	if err != nil {
		t.Fatal(err)
	}
	pb, err := typed.people()
	if err != nil {
		t.Fatal(err)
	}
	if typed.People[0].ThresholdFactor != 1 || pa["qq:user:x"] != pb["qq:user:x"] {
		t.Fatalf("schema default factor %v gives %+v, unset gives %+v", typed.People[0].ThresholdFactor, pb["qq:user:x"], pa["qq:user:x"])
	}
}
