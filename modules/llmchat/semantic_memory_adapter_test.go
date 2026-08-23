package llmchat

import (
	"context"
	"errors"
	"testing"

	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/platform"
)

func TestRetrieveSemanticMemoriesViaServiceShortCircuits(t *testing.T) {
	t.Parallel()

	baseEvent := testLLMChatEvent("Otogi hi")
	enabledAgent := func() Agent {
		agent := validModuleConfig().Agents[0]
		agent.MemoryEnabled = true
		return agent
	}

	testCases := []struct {
		name        string
		retriever   *semanticRetrieverStub
		noRetriever bool
		agent       func() Agent
		event       *platform.Event
		prompt      string
	}{
		{
			name:        "nil retriever service",
			noRetriever: true,
			agent:       enabledAgent,
			event:       baseEvent,
			prompt:      "hi",
		},
		{
			name:      "agent memory disabled",
			retriever: &semanticRetrieverStub{available: true, content: "unexpected"},
			agent: func() Agent {
				agent := validModuleConfig().Agents[0]
				agent.MemoryEnabled = false
				return agent
			},
			event:  baseEvent,
			prompt: "hi",
		},
		{
			name:      "nil event",
			retriever: &semanticRetrieverStub{available: true, content: "unexpected"},
			agent:     enabledAgent,
			event:     nil,
			prompt:    "hi",
		},
		{
			name:      "blank prompt",
			retriever: &semanticRetrieverStub{available: true, content: "unexpected"},
			agent:     enabledAgent,
			event:     baseEvent,
			prompt:    "   ",
		},
		{
			name:      "retriever reports provider unavailable",
			retriever: &semanticRetrieverStub{available: false, content: "unexpected"},
			agent:     enabledAgent,
			event:     baseEvent,
			prompt:    "hi",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			module := newTestModule(validModuleConfig())
			if !testCase.noRetriever {
				module.semanticRetriever = testCase.retriever
			}

			content, err := module.retrieveSemanticMemoriesViaService(
				context.Background(), testCase.event, testCase.agent(), testCase.prompt,
			)
			if err != nil {
				t.Fatalf("retrieveSemanticMemoriesViaService failed: %v", err)
			}
			if content != "" {
				t.Fatalf("content = %q, want empty for short-circuit case", content)
			}
			if testCase.retriever != nil && testCase.retriever.lastReq.Prompt != "" {
				t.Fatalf("retriever Retrieve was called with prompt=%q, expected no call",
					testCase.retriever.lastReq.Prompt)
			}
		})
	}
}

func TestRetrieveSemanticMemoriesViaServicePopulatesRequest(t *testing.T) {
	t.Parallel()

	retriever := &semanticRetrieverStub{available: true}
	module := newTestModule(validModuleConfig())
	module.semanticRetriever = retriever

	agent := validModuleConfig().Agents[0]
	agent.MemoryEnabled = true

	event := testLLMChatEvent("Otogi hi")
	event.TenantID = "tenant-42"

	if _, err := module.retrieveSemanticMemoriesViaService(
		context.Background(), event, agent, "hi",
	); err != nil {
		t.Fatalf("retrieveSemanticMemoriesViaService failed: %v", err)
	}

	req := retriever.lastReq
	if req.Prompt != "hi" {
		t.Fatalf("Prompt = %q, want hi", req.Prompt)
	}
	if req.Scope.TenantID != "tenant-42" ||
		req.Scope.Platform != string(platform.PlatformTelegram) ||
		req.Scope.ConversationID != event.Conversation.ID {
		t.Fatalf("Scope = %+v, want tenant-42/telegram/%s", req.Scope, event.Conversation.ID)
	}
	if req.CurrentActor.ID != event.Actor.ID || req.CurrentActor.Name != event.Actor.DisplayName {
		t.Fatalf("CurrentActor = %+v, want id=%s name=%s",
			req.CurrentActor, event.Actor.ID, event.Actor.DisplayName)
	}
	if req.Policy != (ai.SemanticRetrievalPolicy{}) {
		t.Fatalf("Policy = %+v, want implementation defaults", req.Policy)
	}
}

func TestRetrieveSemanticMemoriesViaServicePropagatesError(t *testing.T) {
	t.Parallel()

	retriever := &semanticRetrieverStub{
		available: true,
		err:       errors.New("search backend exploded"),
	}
	module := newTestModule(validModuleConfig())
	module.semanticRetriever = retriever

	agent := validModuleConfig().Agents[0]
	agent.MemoryEnabled = true

	_, err := module.retrieveSemanticMemoriesViaService(
		context.Background(), testLLMChatEvent("Otogi hi"), agent, "hi",
	)
	if err == nil {
		t.Fatal("retrieveSemanticMemoriesViaService succeeded, want error")
	}
	if !errors.Is(err, retriever.err) {
		t.Fatalf("error chain = %v, want to wrap %v", err, retriever.err)
	}
}

func TestToSemanticActorPrefersDisplayName(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		actor    platform.Actor
		wantID   string
		wantName string
		wantBot  bool
	}{
		{
			name:     "uses display name when set",
			actor:    platform.Actor{ID: "user-1", DisplayName: "Alice", Username: "alice_u"},
			wantID:   "user-1",
			wantName: "Alice",
		},
		{
			name:     "falls back to username when display name blank",
			actor:    platform.Actor{ID: "user-2", DisplayName: "  ", Username: "bob_u"},
			wantID:   "user-2",
			wantName: "bob_u",
		},
		{
			name:    "preserves bot flag",
			actor:   platform.Actor{ID: "bot-1", DisplayName: "Bot", IsBot: true},
			wantID:  "bot-1",
			wantBot: true,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ref := toSemanticActor(testCase.actor)
			if ref.ID != testCase.wantID {
				t.Fatalf("ID = %q, want %q", ref.ID, testCase.wantID)
			}
			if testCase.wantName != "" && ref.Name != testCase.wantName {
				t.Fatalf("Name = %q, want %q", ref.Name, testCase.wantName)
			}
			if ref.IsBot != testCase.wantBot {
				t.Fatalf("IsBot = %t, want %t", ref.IsBot, testCase.wantBot)
			}
		})
	}
}
