package nbnhhsh

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ex-otogi/pkg/otogi"
)

func TestModuleHandleCommand(t *testing.T) {
	tests := []struct {
		name             string
		event            *otogi.Event
		replied          otogi.Memory
		repliedFound     bool
		repliedErr       error
		clientResults    []guessResult
		clientErr        error
		sendErr          error
		wantErrSubstring string
		wantReplyText    string
		wantTextContains []string
		wantClientText   string
		wantClientCalls  int
	}{
		{
			name:            "direct query expands abbreviations",
			event:           newCommandEvent("/nbnhhsh yyds", ""),
			clientResults:   []guessResult{{Name: "yyds", Trans: []string{"永远的神"}}},
			wantClientText:  "yyds",
			wantClientCalls: 1,
			wantTextContains: []string{
				"yyds",
				"永远的神",
			},
		},
		{
			name:  "alias command deduplicates abbreviations",
			event: newCommandEvent("/srh yyds yyds xswl", ""),
			clientResults: []guessResult{
				{Name: "yyds", Trans: []string{"永远的神"}},
				{Name: "xswl", Trans: []string{"笑死我了"}},
			},
			wantClientText:  "yyds,xswl",
			wantClientCalls: 1,
			wantTextContains: []string{
				"yyds",
				"xswl",
			},
		},
		{
			name:         "reply query uses memory text",
			event:        newCommandEvent("/nbnhhsh", "msg-0"),
			repliedFound: true,
			replied: otogi.Memory{
				Article: otogi.Article{Text: "xswl"},
			},
			clientResults:   []guessResult{{Name: "xswl", Trans: []string{"笑死我了"}}},
			wantClientText:  "xswl",
			wantClientCalls: 1,
			wantTextContains: []string{
				"笑死我了",
			},
		},
		{
			name:            "missing arguments without reply shows usage",
			event:           newCommandEvent("/nbnhhsh", ""),
			wantReplyText:   usageMessage().Text,
			wantClientCalls: 0,
		},
		{
			name:            "reply miss shows memory message",
			event:           newCommandEvent("/nbnhhsh", "msg-0"),
			wantReplyText:   replyNotFoundMessage,
			wantClientCalls: 0,
		},
		{
			name:         "reply without text shows unsupported message",
			event:        newCommandEvent("/nbnhhsh", "msg-0"),
			repliedFound: true,
			replied: otogi.Memory{
				Article: otogi.Article{Text: "   "},
			},
			wantReplyText:   replyNoTextMessage,
			wantClientCalls: 0,
		},
		{
			name:            "non abbreviation input is rejected",
			event:           newCommandEvent("/nbnhhsh 你好", ""),
			wantReplyText:   noAbbreviationMessage,
			wantClientCalls: 0,
		},
		{
			name:  "temporary client error replies with timeout",
			event: newCommandEvent("/nbnhhsh yyds", ""),
			clientErr: &clientError{
				kind: clientErrorKindTemporary,
				err:  errors.New("upstream timeout"),
			},
			wantErrSubstring: "upstream timeout",
			wantReplyText:    timeoutMessage,
			wantClientText:   "yyds",
			wantClientCalls:  1,
		},
		{
			name:  "invalid client error replies with invalid data",
			event: newCommandEvent("/nbnhhsh yyds", ""),
			clientErr: &clientError{
				kind: clientErrorKindInvalid,
				err:  errors.New("bad payload"),
			},
			wantErrSubstring: "bad payload",
			wantReplyText:    invalidDataMessage,
			wantClientText:   "yyds",
			wantClientCalls:  1,
		},
		{
			name:             "dispatcher error returns error",
			event:            newCommandEvent("/nbnhhsh yyds", ""),
			clientResults:    []guessResult{{Name: "yyds", Trans: []string{"永远的神"}}},
			sendErr:          errors.New("dispatcher down"),
			wantErrSubstring: "dispatcher down",
			wantClientText:   "yyds",
			wantClientCalls:  1,
			wantTextContains: []string{
				"yyds",
			},
		},
		{
			name:            "unsupported command ignored",
			event:           newCommandEvent("/ping", ""),
			wantClientCalls: 0,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			dispatcher := &captureDispatcher{
				messageID: "sent-1",
				sendErr:   testCase.sendErr,
			}
			client := &fakeGuessClient{
				results: testCase.clientResults,
				err:     testCase.clientErr,
			}
			module := &Module{
				dispatcher: dispatcher,
				memory: &memoryStub{
					replied:      testCase.replied,
					repliedFound: testCase.repliedFound,
					repliedErr:   testCase.repliedErr,
				},
				client: client,
			}

			err := module.handleCommand(context.Background(), testCase.event)
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

			if client.calls != testCase.wantClientCalls {
				t.Fatalf("client calls = %d, want %d", client.calls, testCase.wantClientCalls)
			}
			if client.lastText != testCase.wantClientText {
				t.Fatalf("client text = %q, want %q", client.lastText, testCase.wantClientText)
			}

			if testCase.wantReplyText == "" && len(testCase.wantTextContains) == 0 {
				if dispatcher.sendCalls != 0 {
					t.Fatalf("send calls = %d, want 0", dispatcher.sendCalls)
				}
				return
			}

			if dispatcher.sendCalls != 1 {
				t.Fatalf("send calls = %d, want 1", dispatcher.sendCalls)
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
			if testCase.wantReplyText != "" && dispatcher.lastRequest.Text != testCase.wantReplyText {
				t.Fatalf("reply text = %q, want %q", dispatcher.lastRequest.Text, testCase.wantReplyText)
			}
			for _, want := range testCase.wantTextContains {
				if !strings.Contains(dispatcher.lastRequest.Text, want) {
					t.Fatalf("reply text = %q, missing substring %q", dispatcher.lastRequest.Text, want)
				}
			}
			if err := otogi.ValidateTextEntities(
				dispatcher.lastRequest.Text,
				dispatcher.lastRequest.Entities,
			); err != nil {
				t.Fatalf("reply entities invalid: %v", err)
			}
		})
	}
}

func TestModuleOnRegister(t *testing.T) {
	t.Parallel()

	dispatcher := &captureDispatcher{messageID: "sent-1"}
	memory := &memoryStub{}
	configs := newConfigRegistryStub()
	mustRegisterConfig(t, configs, map[string]string{
		"api_url":         "https://example.com/guess",
		"request_timeout": "4s",
	})

	runtime := &moduleRuntimeStub{
		registry: serviceRegistryStub{
			values: map[string]any{
				otogi.ServiceSinkDispatcher: dispatcher,
				otogi.ServiceMemory:         memory,
			},
		},
		configs: configs,
	}

	module := New()
	if err := module.OnRegister(context.Background(), runtime); err != nil {
		t.Fatalf("OnRegister() error: %v", err)
	}
	if runtime.subscribeCall != 1 {
		t.Fatalf("subscribe calls = %d, want 1", runtime.subscribeCall)
	}
	if runtime.lastSpec.Name != "nbnhhsh-commands" {
		t.Fatalf("subscription name = %q, want nbnhhsh-commands", runtime.lastSpec.Name)
	}
	if runtime.lastSpec.HandlerTimeout != 4500*time.Millisecond {
		t.Fatalf("handler timeout = %s, want 4.5s", runtime.lastSpec.HandlerTimeout)
	}
	if module.dispatcher == nil {
		t.Fatal("expected dispatcher to be configured")
	}
	if module.memory == nil {
		t.Fatal("expected memory service to be configured")
	}
	httpClient, ok := module.client.(*httpGuessClient)
	if !ok {
		t.Fatalf("client type = %T, want *httpGuessClient", module.client)
	}
	if httpClient.apiURL != "https://example.com/guess" {
		t.Fatalf("client api url = %q, want custom config url", httpClient.apiURL)
	}
}

func TestModuleOnRegisterErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		services         map[string]any
		configValue      any
		subscribeErr     error
		wantErrSubstring string
	}{
		{
			name: "missing dispatcher fails",
			services: map[string]any{
				otogi.ServiceMemory: &memoryStub{},
			},
			wantErrSubstring: "resolve sink dispatcher",
		},
		{
			name: "missing memory fails",
			services: map[string]any{
				otogi.ServiceSinkDispatcher: &captureDispatcher{},
			},
			wantErrSubstring: "resolve memory service",
		},
		{
			name: "invalid config fails",
			services: map[string]any{
				otogi.ServiceSinkDispatcher: &captureDispatcher{},
				otogi.ServiceMemory:         &memoryStub{},
			},
			configValue: map[string]string{
				"request_timeout": "bad",
			},
			wantErrSubstring: "load config",
		},
		{
			name: "subscribe failure is wrapped",
			services: map[string]any{
				otogi.ServiceSinkDispatcher: &captureDispatcher{},
				otogi.ServiceMemory:         &memoryStub{},
			},
			subscribeErr:     errors.New("subscribe failed"),
			wantErrSubstring: "nbnhhsh subscribe",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			var configs otogi.ConfigRegistry
			if testCase.configValue != nil {
				registry := newConfigRegistryStub()
				mustRegisterConfig(t, registry, testCase.configValue)
				configs = registry
			}

			runtime := &moduleRuntimeStub{
				registry:     serviceRegistryStub{values: testCase.services},
				configs:      configs,
				subscribeErr: testCase.subscribeErr,
			}

			err := New().OnRegister(context.Background(), runtime)
			if err == nil {
				t.Fatalf("expected error containing %q", testCase.wantErrSubstring)
			}
			if !strings.Contains(err.Error(), testCase.wantErrSubstring) {
				t.Fatalf("error = %v, want substring %q", err, testCase.wantErrSubstring)
			}
		})
	}
}

func TestModuleSpecUsesCommandCapability(t *testing.T) {
	t.Parallel()

	spec := New().Spec()
	if len(spec.Handlers) != 0 {
		t.Fatalf("handler count = %d, want 0 for imperative subscription", len(spec.Handlers))
	}
	if len(spec.AdditionalCapabilities) != 1 {
		t.Fatalf("additional capability count = %d, want 1", len(spec.AdditionalCapabilities))
	}
	if len(spec.Commands) != 2 {
		t.Fatalf("command count = %d, want 2", len(spec.Commands))
	}

	capability := spec.AdditionalCapabilities[0]
	if !capability.Interest.RequireCommand {
		t.Fatal("expected RequireCommand to be true")
	}
	if !capability.Interest.RequireArticle {
		t.Fatal("expected RequireArticle to be true")
	}
	if len(capability.Interest.CommandNames) != 2 {
		t.Fatalf("command names = %v, want 2 aliases", capability.Interest.CommandNames)
	}
	if capability.Interest.CommandNames[0] != primaryCommandName || capability.Interest.CommandNames[1] != aliasCommandName {
		t.Fatalf("command names = %v, want [%s %s]", capability.Interest.CommandNames, primaryCommandName, aliasCommandName)
	}
}
