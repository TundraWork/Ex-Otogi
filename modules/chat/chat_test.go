package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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

func (i invocation) Send(_ context.Context, _ string, msg otogi.Message) (otogi.EventRef, error) {
	i.h.replies = append(i.h.replies, msg.PlainText())
	return otogi.EventRef{}, nil
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
	storage  *modulestore.Storage
	records  []otogi.Record
	services map[string]any
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
func (h *host) Record(_ context.Context, r otogi.Record) { h.records = append(h.records, r) }
func (h *host) Schedule(string, string, otogi.Job) error { return nil }

type harness struct {
	t       *testing.T
	m       *Module
	host    *host
	llm     map[string]*provider
	streams []*stream
	replies []string // sent with Send: command replies
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
	}, []string{"telegram:user:op"})
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
		"bad tool":        {[]ProfileConfig{{Name: "a", Keywords: []string{"a"}, Chats: []string{group}, Provider: "gemini", Model: "m", Tools: []string{"a b"}}}, `"a b": want a built-in`},
		"no provider":     {[]ProfileConfig{{Name: "a", Keywords: []string{"a"}, Chats: []string{group}, Provider: "openai", Model: "m"}}, "not configured"},
		"bad chat":        {[]ProfileConfig{{Name: "a", Keywords: []string{"a"}, Chats: []string{"123"}, Provider: "gemini", Model: "m"}}, "<platform>:<chat id>"},
		"bad thinking":    {[]ProfileConfig{{Name: "a", Keywords: []string{"a"}, Chats: []string{group}, Provider: "gemini", Model: "m", Thinking: "max"}}, "thinking"},
		"duplicate names": {[]ProfileConfig{base, {Name: "a", Keywords: []string{"b"}, Chats: []string{group}, Provider: "gemini", Model: "m"}}, "used twice"},
		"hot":             {[]ProfileConfig{{Name: "a", Keywords: []string{"a"}, Chats: []string{group}, Provider: "gemini", Model: "m", Temperature: ptr(2.5)}}, "temperature 2.5: want 0 to 2"},
		"two prompts":     {[]ProfileConfig{{Name: "a", Keywords: []string{"a"}, Chats: []string{group}, Provider: "gemini", Model: "m", System: "x", SystemFile: "card.md"}}, "use one"},
		"missing card":    {[]ProfileConfig{{Name: "a", Keywords: []string{"a"}, Chats: []string{group}, Provider: "gemini", Model: "m", SystemFile: "/nonexistent/card.md"}}, "system_file: open /nonexistent/card.md"},
	} {
		if _, err := New(Config{Profiles: c.profiles}, providers, nil); err == nil || !strings.Contains(err.Error(), c.want) {
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

func TestToolGroups(t *testing.T) {
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
	h := &harness{t: t, host: &host{storage: storage, services: map[string]any{"llm.tools/fun": []llm.Tool{echo}}}, llm: map[string]*provider{}, now: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)}
	p := &provider{responses: []llm.Response{
		{Turn: llm.Turn{Role: llm.RoleModel, Calls: []llm.Call{{ID: "c1", Name: "shout", Args: json.RawMessage(`"hi"`)}}}, Finish: llm.FinishToolCalls},
		answer("Shouted."),
	}}
	h.llm["p"] = p
	h.m, err = New(Config{Profiles: []ProfileConfig{{Name: "otogi", Keywords: []string{"otogi"}, Chats: []string{group}, Provider: "p", Model: "m", Tools: []string{"google_search", "fun"}}}},
		func(string) (llm.Provider, error) { return p, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := h.m.Descriptor().Requires; len(got) != 1 || got[0] != "llm.tools/fun" {
		t.Fatalf("requires %v", got)
	}
	h.m.now = func() time.Time { return h.now }
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
}

// A profile with mention: true answers a message that mentions the account,
// and the @ itself is left out of the prompt; without the flag, a mention
// does not trigger.
func TestMentionTrigger(t *testing.T) {
	h := newHarness(t,
		ProfileConfig{Name: "luna", Keywords: []string{"luna"}, Mention: true, Chats: []string{group}, Provider: "luna", Model: "m"},
		ProfileConfig{Name: "otogi", Keywords: []string{"otogi"}, Chats: []string{group}, Provider: "otogi", Model: "m"})
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

func ptr[T any](v T) *T { return &v }

// A character card comes from its file, and the temperature reaches the
// provider.
func TestCharacterCardAndTemperature(t *testing.T) {
	card := filepath.Join(t.TempDir(), "mizuki.md")
	if err := os.WriteFile(card, []byte("# Role: 水月\n\n称呼你为「博士」。\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, ProfileConfig{Name: "Mizuki", Keywords: []string{"水月"}, Chats: []string{group}, Provider: "mizuki", Model: "m", SystemFile: card, Temperature: ptr(1.2)})
	p := h.llm["mizuki"]
	p.responses = []llm.Response{answer("博士！")}
	h.mustHandle(h.message("Alice", "水月，你好"))
	req := p.requests[0]
	if !strings.HasPrefix(req.System, "# Role: 水月\n\n称呼你为「博士」。") || req.Temperature == nil || *req.Temperature != 1.2 {
		t.Fatalf("system %q, temperature %v", req.System, req.Temperature)
	}
}

// Operators change where agents answer with /chat; the change outlasts a
// restart and wins over the config, and anyone can see the list.
func TestChatAllowAndDeny(t *testing.T) {
	h := newHarness(t, ProfileConfig{Name: "Luna", Keywords: []string{"luna"}, Chats: []string{group}, Provider: "luna", Model: "m"},
		ProfileConfig{Name: "Mizuki", Keywords: []string{"水月"}, Provider: "mizuki", Model: "m"})
	command := func(actor, args string) string {
		e := h.message(actor, "/chat "+args)
		e.Command = &otogi.Command{Name: "chat", Args: args, Argv: strings.Fields(args)}
		h.mustHandle(e)
		return h.replies[len(h.replies)-1]
	}
	for _, c := range []struct{ actor, args, want string }{
		{"alice", "list", "In telegram:chat:1: Luna."},
		{"alice", "allow 水月", "Only operators can change where agents answer. Your ID is telegram:user:alice."},
		{"op", "allow 水月", "Mizuki now answers in telegram:chat:1."},
		{"op", "allow mizuki", "Mizuki already answers in telegram:chat:1."},
		{"op", "deny luna", "Luna no longer answers in telegram:chat:1."},
		{"op", "allow luna qq:group:G1", "Luna now answers in qq:group:G1."},
		{"op", "allow nobody", `No agent "nobody"; the agents are Luna, Mizuki.`},
		{"op", "allow luna G1", `"G1" is not a chat; name it as /whoami prints it, like telegram:chat:1.`},
		{"bob", "list", "In telegram:chat:1: Mizuki."},
		{"bob", "list qq:group:G1", "In qq:group:G1: Luna."},
	} {
		if got := command(c.actor, c.args); got != c.want {
			t.Errorf("/chat %s by %s: %q, want %q", c.args, c.actor, got, c.want)
		}
	}
	h.llm["mizuki"].responses = []llm.Response{answer("博士！")}
	h.mustHandle(h.message("alice", "水月 你好"))
	h.mustHandle(h.message("alice", "luna hi"))
	if len(h.llm["mizuki"].requests) != 1 || len(h.llm["luna"].requests) != 0 {
		t.Fatalf("mizuki %d, luna %d requests", len(h.llm["mizuki"].requests), len(h.llm["luna"].requests))
	}
	if r := h.host.records[0]; r.Kind != "chat.access" || r.Payload.(map[string]any)["by"] != "telegram:user:op" {
		t.Fatalf("record %+v", r)
	}

	// The changes are stored: a restarted module keeps them.
	again, err := New(h.m.cfg, func(string) (llm.Provider, error) { return &provider{}, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := again.Start(context.Background(), h.host); err != nil {
		t.Fatal(err)
	}
	if !again.answers(again.byName["Mizuki"], group) || again.answers(again.byName["Luna"], group) {
		t.Fatal("access lost on restart")
	}
}
