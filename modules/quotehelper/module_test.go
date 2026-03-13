package quotehelper

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

func TestModuleHandleReplyCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		event            *platform.Event
		replied          core.Memory
		repliedFound     bool
		repliedErr       error
		sendErr          error
		wantErrSubstring string
		wantReplyText    string
		wantSendCalls    int
	}{
		{
			name:          "you rewrites replied text",
			event:         newCommandEvent("/you", "msg-0"),
			repliedFound:  true,
			replied:       core.Memory{Article: platform.Article{Text: "我和你"}},
			wantReplyText: "您和我",
			wantSendCalls: 1,
		},
		{
			name:          "we rewrites replied text",
			event:         newCommandEvent("/we", "msg-0"),
			repliedFound:  true,
			replied:       core.Memory{Article: platform.Article{Text: "我和你谢谢您"}},
			wantReplyText: "大伙自己和大伙自己谢谢大伙自己",
			wantSendCalls: 1,
		},
		{
			name:          "command arguments are ignored for parity",
			event:         newCommandEvent("/you now", "msg-0"),
			repliedFound:  true,
			replied:       core.Memory{Article: platform.Article{Text: "我和你"}},
			wantSendCalls: 0,
		},
		{
			name:          "missing reply is ignored",
			event:         newCommandEvent("/we", ""),
			wantSendCalls: 0,
		},
		{
			name:          "reply miss is ignored",
			event:         newCommandEvent("/you", "msg-0"),
			wantSendCalls: 0,
		},
		{
			name:          "empty replied text is ignored",
			event:         newCommandEvent("/we", "msg-0"),
			repliedFound:  true,
			replied:       core.Memory{Article: platform.Article{Text: "   "}},
			wantSendCalls: 0,
		},
		{
			name:             "reply lookup failure returns error",
			event:            newCommandEvent("/you", "msg-0"),
			repliedErr:       errors.New("memory unavailable"),
			wantErrSubstring: "memory unavailable",
			wantSendCalls:    0,
		},
		{
			name:             "send failure returns error",
			event:            newCommandEvent("/we", "msg-0"),
			repliedFound:     true,
			replied:          core.Memory{Article: platform.Article{Text: "我"}},
			sendErr:          errors.New("dispatcher down"),
			wantErrSubstring: "dispatcher down",
			wantReplyText:    "大伙自己",
			wantSendCalls:    1,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			dispatcher := &captureDispatcher{messageID: "sent-1", sendErr: testCase.sendErr}
			module := &Module{
				dispatcher: dispatcher,
				memory: &memoryStub{
					replied:      testCase.replied,
					repliedFound: testCase.repliedFound,
					repliedErr:   testCase.repliedErr,
				},
			}

			var err error
			switch testCase.event.Command.Name {
			case youCommandName:
				err = module.handleYouCommand(context.Background(), testCase.event)
			case weCommandName:
				err = module.handleWeCommand(context.Background(), testCase.event)
			default:
				t.Fatalf("unexpected command name %q", testCase.event.Command.Name)
			}

			if testCase.wantErrSubstring == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if testCase.wantErrSubstring != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", testCase.wantErrSubstring)
				}
				if !strings.Contains(err.Error(), testCase.wantErrSubstring) {
					t.Fatalf("error = %v, want substring %q", err, testCase.wantErrSubstring)
				}
			}

			if dispatcher.sendCalls != testCase.wantSendCalls {
				t.Fatalf("send calls = %d, want %d", dispatcher.sendCalls, testCase.wantSendCalls)
			}
			if testCase.wantSendCalls == 0 {
				return
			}

			if dispatcher.lastRequest.Text != testCase.wantReplyText {
				t.Fatalf("reply text = %q, want %q", dispatcher.lastRequest.Text, testCase.wantReplyText)
			}
			if dispatcher.lastRequest.ReplyToMessageID != testCase.event.Article.ID {
				t.Fatalf("reply_to = %q, want %q", dispatcher.lastRequest.ReplyToMessageID, testCase.event.Article.ID)
			}
			if dispatcher.lastRequest.Target.Sink == nil {
				t.Fatal("target sink = nil, want source sink")
			}
			if dispatcher.lastRequest.Target.Sink.ID != "tg-main" {
				t.Fatalf("target sink id = %q, want tg-main", dispatcher.lastRequest.Target.Sink.ID)
			}
		})
	}
}

func TestModuleHandleSubstituteArticle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		event            *platform.Event
		replied          core.Memory
		repliedFound     bool
		repliedErr       error
		runnerResult     string
		runnerErr        error
		sendErr          error
		wantErrSubstring string
		wantReplyText    string
		wantRunnerExpr   string
		wantRunnerInput  string
		wantRunnerCalls  int
		wantSendCalls    int
	}{
		{
			name:            "matching substitute expression runs runner",
			event:           newArticleEvent("s/我/你/g", "msg-0"),
			repliedFound:    true,
			replied:         core.Memory{Article: platform.Article{Text: "我爱我"}},
			runnerResult:    "你爱你",
			wantRunnerExpr:  "s/我/你/g",
			wantRunnerInput: "我爱我",
			wantRunnerCalls: 1,
			wantReplyText:   "你爱你",
			wantSendCalls:   1,
		},
		{
			name:            "non substitute text is ignored",
			event:           newArticleEvent("hello", "msg-0"),
			repliedFound:    true,
			replied:         core.Memory{Article: platform.Article{Text: "我爱我"}},
			wantRunnerCalls: 0,
			wantSendCalls:   0,
		},
		{
			name:            "missing reply is ignored",
			event:           newArticleEvent("s/a/b/", ""),
			wantRunnerCalls: 0,
			wantSendCalls:   0,
		},
		{
			name:            "reply miss is ignored",
			event:           newArticleEvent("s/a/b/", "msg-0"),
			wantRunnerCalls: 0,
			wantSendCalls:   0,
		},
		{
			name:             "reply lookup failure returns error",
			event:            newArticleEvent("s/a/b/", "msg-0"),
			repliedErr:       errors.New("memory unavailable"),
			wantErrSubstring: "memory unavailable",
			wantRunnerCalls:  0,
			wantSendCalls:    0,
		},
		{
			name:             "runner error sends failure reply",
			event:            newArticleEvent("s/a/b/", "msg-0"),
			repliedFound:     true,
			replied:          core.Memory{Article: platform.Article{Text: "abc"}},
			runnerErr:        errors.New("sed failed"),
			wantErrSubstring: "sed failed",
			wantRunnerExpr:   "s/a/b/",
			wantRunnerInput:  "abc",
			wantRunnerCalls:  1,
			wantReplyText:    substituteFailureMessage,
			wantSendCalls:    1,
		},
		{
			name:             "send failure on success returns error",
			event:            newArticleEvent("s/a/b/", "msg-0"),
			repliedFound:     true,
			replied:          core.Memory{Article: platform.Article{Text: "abc"}},
			runnerResult:     "bbc",
			sendErr:          errors.New("dispatcher down"),
			wantErrSubstring: "dispatcher down",
			wantReplyText:    "bbc",
			wantRunnerExpr:   "s/a/b/",
			wantRunnerInput:  "abc",
			wantRunnerCalls:  1,
			wantSendCalls:    1,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			dispatcher := &captureDispatcher{messageID: "sent-1", sendErr: testCase.sendErr}
			runner := &fakeRunner{result: testCase.runnerResult, err: testCase.runnerErr}
			module := &Module{
				dispatcher: dispatcher,
				memory: &memoryStub{
					replied:      testCase.replied,
					repliedFound: testCase.repliedFound,
					repliedErr:   testCase.repliedErr,
				},
				runner: runner,
			}

			err := module.handleSubstituteArticle(context.Background(), testCase.event)
			if testCase.wantErrSubstring == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if testCase.wantErrSubstring != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", testCase.wantErrSubstring)
				}
				if !strings.Contains(err.Error(), testCase.wantErrSubstring) {
					t.Fatalf("error = %v, want substring %q", err, testCase.wantErrSubstring)
				}
			}

			if runner.calls != testCase.wantRunnerCalls {
				t.Fatalf("runner calls = %d, want %d", runner.calls, testCase.wantRunnerCalls)
			}
			if runner.lastExpression != testCase.wantRunnerExpr {
				t.Fatalf("runner expression = %q, want %q", runner.lastExpression, testCase.wantRunnerExpr)
			}
			if runner.lastInput != testCase.wantRunnerInput {
				t.Fatalf("runner input = %q, want %q", runner.lastInput, testCase.wantRunnerInput)
			}
			if dispatcher.sendCalls != testCase.wantSendCalls {
				t.Fatalf("send calls = %d, want %d", dispatcher.sendCalls, testCase.wantSendCalls)
			}
			if testCase.wantSendCalls == 0 {
				return
			}

			if dispatcher.lastRequest.Text != testCase.wantReplyText {
				t.Fatalf("reply text = %q, want %q", dispatcher.lastRequest.Text, testCase.wantReplyText)
			}
			if dispatcher.lastRequest.ReplyToMessageID != testCase.event.Article.ID {
				t.Fatalf("reply_to = %q, want %q", dispatcher.lastRequest.ReplyToMessageID, testCase.event.Article.ID)
			}
		})
	}
}

func TestParseSubstitutionExpression(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		wantExpr string
		wantOK   bool
	}{
		{name: "plain substitute", input: "s/a/b/", wantExpr: "s/a/b/", wantOK: true},
		{name: "global substitute", input: "s/a/b/g", wantExpr: "s/a/b/g", wantOK: true},
		{name: "trimmed input", input: "  s/a/b/g  ", wantExpr: "s/a/b/g", wantOK: true},
		{name: "unsupported flag", input: "s/a/b/x", wantOK: false},
		{name: "too many separators", input: "s/a/b/c", wantOK: false},
		{name: "not substitute", input: "/you", wantOK: false},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got, ok := parseSubstitutionExpression(testCase.input)
			if ok != testCase.wantOK {
				t.Fatalf("ok = %v, want %v", ok, testCase.wantOK)
			}
			if got != testCase.wantExpr {
				t.Fatalf("expression = %q, want %q", got, testCase.wantExpr)
			}
		})
	}
}

func TestTransforms(t *testing.T) {
	t.Parallel()

	if got := transformForYou("我和你请咱提醒您"); got != "您和我请您提醒我" {
		t.Fatalf("transformForYou = %q, want %q", got, "您和我请您提醒我")
	}
	if got := transformForWe("我和你谢谢您"); got != "大伙自己和大伙自己谢谢大伙自己" {
		t.Fatalf("transformForWe = %q, want %q", got, "大伙自己和大伙自己谢谢大伙自己")
	}
}

func TestExecSubstitutionRunner(t *testing.T) {
	t.Parallel()

	path, err := exec.LookPath("sed")
	if err != nil {
		t.Skip("sed not available")
	}

	runner := execSubstitutionRunner{
		command: path,
		timeout: time.Second,
	}

	result, err := runner.Run(context.Background(), "s/foo/bar/g", "foo foo")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result != "bar bar" {
		t.Fatalf("result = %q, want %q", result, "bar bar")
	}

	if _, err := runner.Run(context.Background(), "s/[//", "foo"); err == nil {
		t.Fatal("expected invalid sed expression error")
	}
}

func TestModuleOnRegister(t *testing.T) {
	t.Parallel()

	dispatcher := &captureDispatcher{messageID: "sent-1"}
	memory := &memoryStub{}
	runtime := moduleRuntimeStub{
		registry: serviceRegistryStub{
			values: map[string]any{
				platform.ServiceSinkDispatcher: dispatcher,
				core.ServiceMemory:             memory,
			},
		},
		configs: noopConfigRegistry{},
	}

	module := New()
	if err := module.OnRegister(context.Background(), runtime); err != nil {
		t.Fatalf("OnRegister failed: %v", err)
	}
	if module.dispatcher == nil {
		t.Fatal("expected dispatcher to be configured")
	}
	if module.memory == nil {
		t.Fatal("expected memory service to be configured")
	}
	if module.runner == nil {
		t.Fatal("expected substitution runner to be configured")
	}
}

func TestModuleSpec(t *testing.T) {
	t.Parallel()

	spec := New().Spec()
	if len(spec.Handlers) != 3 {
		t.Fatalf("handler count = %d, want 3", len(spec.Handlers))
	}
	if len(spec.Commands) != 2 {
		t.Fatalf("command count = %d, want 2", len(spec.Commands))
	}
	if spec.Commands[0].Name != youCommandName || spec.Commands[1].Name != weCommandName {
		t.Fatalf("unexpected commands: %+v", spec.Commands)
	}
}

func newCommandEvent(text string, replyToID string) *platform.Event {
	candidate, matched, err := platform.ParseCommandCandidate(text)
	if err != nil {
		panic(err)
	}
	if !matched {
		panic("newCommandEvent expects command text")
	}

	return &platform.Event{
		ID:         "event-1",
		Kind:       platform.EventKindCommandReceived,
		OccurredAt: time.Unix(1, 0).UTC(),
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{
			ID:   "chat-42",
			Type: platform.ConversationTypePrivate,
		},
		Article: &platform.Article{
			ID:               "msg-1",
			Text:             text,
			ReplyToArticleID: replyToID,
		},
		Command: &platform.CommandInvocation{
			Name:            candidate.Name,
			Mention:         candidate.Mention,
			Value:           strings.Join(candidate.Tokens, " "),
			SourceEventID:   "source-1",
			SourceEventKind: platform.EventKindArticleCreated,
			RawInput:        text,
		},
	}
}

func newArticleEvent(text string, replyToID string) *platform.Event {
	return &platform.Event{
		ID:         "event-1",
		Kind:       platform.EventKindArticleCreated,
		OccurredAt: time.Unix(1, 0).UTC(),
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{
			ID:   "chat-42",
			Type: platform.ConversationTypePrivate,
		},
		Article: &platform.Article{
			ID:               "msg-1",
			Text:             text,
			ReplyToArticleID: replyToID,
		},
	}
}

type captureDispatcher struct {
	sendErr     error
	messageID   string
	sendCalls   int
	lastRequest platform.SendMessageRequest
}

func (d *captureDispatcher) SendMessage(
	_ context.Context,
	request platform.SendMessageRequest,
) (*platform.OutboundMessage, error) {
	d.sendCalls++
	d.lastRequest = request
	if d.sendErr != nil {
		return nil, d.sendErr
	}

	return &platform.OutboundMessage{ID: d.messageID}, nil
}

func (*captureDispatcher) EditMessage(context.Context, platform.EditMessageRequest) error {
	return nil
}

func (*captureDispatcher) DeleteMessage(context.Context, platform.DeleteMessageRequest) error {
	return nil
}

func (*captureDispatcher) SetReaction(context.Context, platform.SetReactionRequest) error {
	return nil
}

func (*captureDispatcher) ListSinks(context.Context) ([]platform.EventSink, error) {
	return nil, nil
}

func (*captureDispatcher) ListSinksByPlatform(context.Context, platform.Platform) ([]platform.EventSink, error) {
	return nil, nil
}

type memoryStub struct {
	replied      core.Memory
	repliedFound bool
	repliedErr   error
}

func (*memoryStub) Get(context.Context, core.MemoryLookup) (core.Memory, bool, error) {
	return core.Memory{}, false, nil
}

func (*memoryStub) GetBatch(context.Context, []core.MemoryLookup) (map[core.MemoryLookup]core.Memory, error) {
	return nil, nil
}

func (m *memoryStub) GetReplied(context.Context, *platform.Event) (core.Memory, bool, error) {
	return m.replied, m.repliedFound, m.repliedErr
}

func (*memoryStub) GetReplyChain(context.Context, *platform.Event) ([]core.ReplyChainEntry, error) {
	return nil, nil
}

func (*memoryStub) ListConversationContextBefore(
	context.Context,
	core.ConversationContextBeforeQuery,
) ([]core.ConversationContextEntry, error) {
	return nil, nil
}

type fakeRunner struct {
	lastExpression string
	lastInput      string
	calls          int
	result         string
	err            error
}

func (r *fakeRunner) Run(_ context.Context, expression string, input string) (string, error) {
	r.calls++
	r.lastExpression = expression
	r.lastInput = input
	if r.err != nil {
		return "", r.err
	}

	return r.result, nil
}

type moduleRuntimeStub struct {
	registry core.ServiceRegistry
	configs  core.ConfigRegistry
}

func (s moduleRuntimeStub) Services() core.ServiceRegistry {
	return s.registry
}

func (s moduleRuntimeStub) Config() core.ConfigRegistry {
	return s.configs
}

func (moduleRuntimeStub) Subscribe(
	context.Context,
	core.InterestSet,
	core.SubscriptionSpec,
	core.EventHandler,
) (core.Subscription, error) {
	return nil, nil
}

type serviceRegistryStub struct {
	values map[string]any
}

func (serviceRegistryStub) Register(string, any) error {
	return nil
}

func (s serviceRegistryStub) Resolve(name string) (any, error) {
	value, ok := s.values[name]
	if !ok {
		return nil, core.ErrServiceNotFound
	}

	return value, nil
}

type noopConfigRegistry struct{}

func (noopConfigRegistry) Register(string, json.RawMessage) error {
	return nil
}

func (noopConfigRegistry) Resolve(string) (json.RawMessage, error) {
	return nil, core.ErrConfigNotFound
}
