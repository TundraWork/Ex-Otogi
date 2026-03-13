package llmchat

import (
	"context"
	"strings"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

func TestHandleArticleDirectReplyToTaggedArticleReusesAgent(t *testing.T) {
	module := newTestModule(Config{
		RequestTimeout: time.Second,
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Aliases:              []string{"Oto"},
				Description:          "assistant",
				Provider:             "openai",
				Model:                "gpt-test",
				SystemPromptTemplate: "You are {{.AgentName}}",
				RequestTimeout:       time.Second,
			},
			{
				Name:                 "Gemini",
				Description:          "assistant",
				Provider:             "gemini",
				Model:                "gemini-test",
				SystemPromptTemplate: "You are {{.AgentName}}",
				RequestTimeout:       time.Second,
			},
		},
	})

	sink := &sinkDispatcherStub{}
	module.dispatcher = sink
	module.memory = &memoryStub{
		repliedFound: true,
		repliedMemory: core.Memory{
			Article: platform.Article{
				ID:   "msg-1",
				Text: "openai",
				Tags: llmchatArticleTags(Agent{Name: "Otogi"}),
			},
		},
		replyChain: []core.ReplyChainEntry{
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "user-1", DisplayName: "Alice"},
				Article:      platform.Article{ID: "current", Text: "placeholder"},
				IsCurrent:    true,
			},
		},
	}

	var openAICalls, geminiCalls int
	openAIProvider := &regeneratingProviderStub{
		chunks: []ai.LLMGenerateChunk{{Delta: "openai"}},
		onGenerateStream: func() {
			openAICalls++
		},
	}
	geminiProvider := &regeneratingProviderStub{
		chunks: []ai.LLMGenerateChunk{{Delta: "gemini"}},
		onGenerateStream: func() {
			geminiCalls++
		},
	}
	module.providers = map[string]ai.LLMProvider{
		"openai": openAIProvider,
		"gemini": geminiProvider,
	}

	event := testLLMChatEvent("tell me more")
	event.Article.ID = "m-2"
	event.Article.ReplyToArticleID = "msg-1"
	if err := module.handleArticle(context.Background(), event); err != nil {
		t.Fatalf("handleArticle failed: %v", err)
	}

	if openAICalls != 1 {
		t.Fatalf("openai provider calls = %d, want 1", openAICalls)
	}
	if geminiCalls != 0 {
		t.Fatalf("gemini provider calls = %d, want 0", geminiCalls)
	}
	if len(sink.sendRequests) != 1 {
		t.Fatalf("send request count = %d, want 1", len(sink.sendRequests))
	}
	if sink.sendRequests[0].Tags[articleTagFrameworkModule] != "llmchat" {
		t.Fatalf("send tags = %+v, want platform.module=llmchat", sink.sendRequests[0].Tags)
	}
	if sink.sendRequests[0].Tags[articleTagAgent] != "Otogi" {
		t.Fatalf("send tags = %+v, want llmchat.agent=Otogi", sink.sendRequests[0].Tags)
	}

	messages := allMessageContents(openAIProvider.lastRequest.Messages)
	if len(messages) == 0 {
		t.Fatal("expected openai request messages")
	}
	lastMessage := messages[len(messages)-1]
	if !strings.Contains(lastMessage, "tell me more") {
		t.Fatalf("last request message = %q, want follow-up prompt", lastMessage)
	}
	if strings.Contains(lastMessage, "Oto") {
		t.Fatalf("last request message = %q, want raw follow-up without prefix stripping artifacts", lastMessage)
	}
}

type regeneratingProviderStub struct {
	chunks           []ai.LLMGenerateChunk
	streamErr        error
	onGenerateStream func()
	lastRequest      ai.LLMGenerateRequest
}

func (p *regeneratingProviderStub) GenerateStream(
	_ context.Context,
	req ai.LLMGenerateRequest,
) (ai.LLMStream, error) {
	if p.onGenerateStream != nil {
		p.onGenerateStream()
	}
	if p.streamErr != nil {
		return nil, p.streamErr
	}

	p.lastRequest = req
	return &streamStub{chunks: append([]ai.LLMGenerateChunk(nil), p.chunks...)}, nil
}

func TestHandleArticleDirectReplyToUntrackedArticleDoesNotTrigger(t *testing.T) {
	module := newTestModule(Config{
		RequestTimeout: time.Second,
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Description:          "assistant",
				Provider:             "openai",
				Model:                "gpt-test",
				SystemPromptTemplate: "You are {{.AgentName}}",
				RequestTimeout:       time.Second,
			},
		},
	})

	sink := &sinkDispatcherStub{}
	module.dispatcher = sink
	module.memory = &memoryStub{
		replyChain: []core.ReplyChainEntry{
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "user-1", DisplayName: "Alice"},
				Article:      platform.Article{ID: "current", Text: "placeholder"},
				IsCurrent:    true,
			},
		},
	}

	providerCalls := 0
	module.providers = map[string]ai.LLMProvider{
		"openai": &providerStub{
			stream: &streamStub{chunks: []ai.LLMGenerateChunk{{Delta: "openai"}}},
			onGenerateStream: func() {
				providerCalls++
			},
		},
	}

	event := testLLMChatEvent("tell me more")
	event.Article.ReplyToArticleID = "msg-missing"
	if err := module.handleArticle(context.Background(), event); err != nil {
		t.Fatalf("handleArticle failed: %v", err)
	}

	if providerCalls != 0 {
		t.Fatalf("provider calls = %d, want 0", providerCalls)
	}
	if len(sink.sendRequests) != 0 {
		t.Fatalf("send request count = %d, want 0", len(sink.sendRequests))
	}
}
