package llmchat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

func TestSpecDeclaresAdditionalCapabilities(t *testing.T) {
	t.Parallel()

	module := New()
	spec := module.Spec()

	if len(spec.AdditionalCapabilities) != 1 {
		t.Fatalf("additional capabilities len = %d, want 1", len(spec.AdditionalCapabilities))
	}
	capability := spec.AdditionalCapabilities[0]
	if capability.Name != "llm-chat-trigger" {
		t.Fatalf("capability name = %q, want llm-chat-trigger", capability.Name)
	}
	if !capability.Interest.RequireArticle {
		t.Fatal("expected RequireArticle to be true")
	}
	if len(capability.Interest.Kinds) != 1 || capability.Interest.Kinds[0] != platform.EventKindArticleCreated {
		t.Fatalf("kinds = %v, want [%s]", capability.Interest.Kinds, platform.EventKindArticleCreated)
	}
}

func TestMatchTriggeredAgent(t *testing.T) {
	module := newTestModule(Config{
		RequestTimeout: time.Second,
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Aliases:              []string{"Oto", "Otogi Sensei"},
				Description:          "primary",
				Provider:             "p",
				Model:                "m",
				SystemPromptTemplate: "You are {{.AgentName}}",
				RequestTimeout:       time.Second,
			},
			{
				Name:                 "Ai",
				Description:          "short",
				Provider:             "p",
				Model:                "m",
				SystemPromptTemplate: "You are {{.AgentName}}",
				RequestTimeout:       time.Second,
			},
		},
	})

	tests := []struct {
		name      string
		text      string
		wantMatch bool
		wantAgent string
		wantBody  string
	}{
		{name: "exact prefix", text: "Otogi hello", wantMatch: true, wantAgent: "Otogi", wantBody: "hello"},
		{name: "alias prefix", text: "Oto hello", wantMatch: true, wantAgent: "Otogi", wantBody: "hello"},
		{name: "colon separator", text: "Otogi: hello", wantMatch: true, wantAgent: "Otogi", wantBody: "hello"},
		{name: "punct separator", text: "Otogi, hello", wantMatch: true, wantAgent: "Otogi", wantBody: "hello"},
		{name: "no body", text: "Otogi", wantMatch: true, wantAgent: "Otogi", wantBody: ""},
		{name: "not prefix", text: "hello Otogi", wantMatch: false},
		{name: "word continuation rejects", text: "Otogix hi", wantMatch: false},
		{name: "case-insensitive", text: "otogi hi", wantMatch: true, wantAgent: "Otogi", wantBody: "hi"},
		{name: "case-insensitive alias", text: "oto hi", wantMatch: true, wantAgent: "Otogi", wantBody: "hi"},
		{
			name:      "longest alias wins",
			text:      "Otogi Sensei hi",
			wantMatch: true,
			wantAgent: "Otogi",
			wantBody:  "hi",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			agent, body, matched := module.matchTriggeredAgent(testCase.text)
			if matched != testCase.wantMatch {
				t.Fatalf("matched = %v, want %v", matched, testCase.wantMatch)
			}
			if !matched {
				return
			}
			if agent.Name != testCase.wantAgent {
				t.Fatalf("agent = %q, want %q", agent.Name, testCase.wantAgent)
			}
			if body != testCase.wantBody {
				t.Fatalf("body = %q, want %q", body, testCase.wantBody)
			}
		})
	}
}

func TestBuildGenerateRequestStructuresContextAndPreservesRoles(t *testing.T) {
	module := newTestModule(Config{
		RequestTimeout: time.Second,
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Description:          "assistant",
				Provider:             "p",
				Model:                "gpt-test",
				SystemPromptTemplate: "You are {{.AgentName}} at {{.DateTimeUTC}}",
				RequestTimeout:       time.Second,
				RequestMetadata: map[string]string{
					"gemini.google_search":      "true",
					"gemini.url_context":        "false",
					"gemini.thinking_budget":    "32",
					"gemini.include_thoughts":   "true",
					"gemini.thinking_level":     "medium",
					"gemini.response_mime_type": "application/json",
				},
				ContextPolicy: ContextPolicy{
					ReplyChainMaxMessages:  6,
					LeadingContextMessages: 2,
					LeadingContextMaxAge:   15 * time.Minute,
					MaxContextRunes:        4000,
					MaxMessageRunes:        120,
				},
			},
		},
	})

	module.memory = &memoryStub{
		leadingContext: []core.ConversationContextEntry{
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u0", Username: "eve"},
				Article:      platform.Article{ID: "ctx-1", Text: "background before the thread"},
				CreatedAt:    time.Unix(80, 0).UTC(),
			},
		},
		replyChain: []core.ReplyChainEntry{
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u1", Username: "alice"},
				Article:      platform.Article{ID: "m1", Text: "hello"},
				CreatedAt:    time.Unix(90, 0).UTC(),
			},
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "bot-1", Username: "otogi", IsBot: true},
				Article:      platform.Article{ID: "m2", ReplyToArticleID: "m1", Text: "previous bot reply"},
				CreatedAt:    time.Unix(95, 0).UTC(),
			},
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u2", DisplayName: "Bob"},
				Article:      platform.Article{ID: "m3", ReplyToArticleID: "m2", Text: "trigger text"},
				CreatedAt:    time.Unix(100, 0).UTC(),
				IsCurrent:    true,
			},
		},
	}
	module.clock = func() time.Time { return time.Unix(100, 0).UTC() }

	req, err := module.buildGenerateRequest(context.Background(), &platform.Event{
		ID:         "evt-1",
		Kind:       platform.EventKindArticleCreated,
		OccurredAt: time.Unix(100, 0).UTC(),
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
		Actor:        platform.Actor{ID: "u2", DisplayName: "Bob"},
		Article: &platform.Article{
			ID:               "m3",
			ReplyToArticleID: "m2",
			Text:             "Otogi hi",
		},
	}, module.cfg.Agents[0], "how are you")
	if err != nil {
		t.Fatalf("buildGenerateRequest failed: %v", err)
	}
	if len(req.Messages) != 6 {
		t.Fatalf("messages len = %d, want 6", len(req.Messages))
	}
	if req.Messages[0].Role != ai.LLMMessageRoleSystem {
		t.Fatalf("system role = %q, want %q", req.Messages[0].Role, ai.LLMMessageRoleSystem)
	}
	if req.Messages[1].Role != ai.LLMMessageRoleSystem {
		t.Fatalf("message[1] role = %q, want %q", req.Messages[1].Role, ai.LLMMessageRoleSystem)
	}
	if !strings.Contains(req.Messages[1].Content, "structured conversation context") {
		t.Fatalf("message[1] = %q, want context handling instructions", req.Messages[1].Content)
	}
	if req.Messages[2].Role != ai.LLMMessageRoleUser || !strings.Contains(req.Messages[2].Content, "<leading_context") {
		t.Fatalf("message[2] = %+v, want leading_context user message", req.Messages[2])
	}
	if !strings.Contains(req.Messages[2].Content, "background before the thread") {
		t.Fatalf("message[2] = %q, want leading context body", req.Messages[2].Content)
	}
	if req.Messages[3].Role != ai.LLMMessageRoleUser || !strings.Contains(req.Messages[3].Content, `<reply_thread_message`) {
		t.Fatalf("message[3] = %+v, want user reply-thread message", req.Messages[3])
	}
	if !strings.Contains(req.Messages[3].Content, `article_id="m1"`) {
		t.Fatalf("message[3] = %q, want root article id", req.Messages[3].Content)
	}
	if req.Messages[4].Role != ai.LLMMessageRoleAssistant {
		t.Fatalf("message[4] role = %q, want assistant", req.Messages[4].Role)
	}
	if !strings.Contains(req.Messages[4].Content, "previous bot reply") {
		t.Fatalf("message[4] = %q, want previous assistant content", req.Messages[4].Content)
	}
	if req.Messages[5].Role != ai.LLMMessageRoleUser {
		t.Fatalf("message[5] role = %q, want user", req.Messages[5].Role)
	}
	if !strings.Contains(req.Messages[5].Content, "<current_message") {
		t.Fatalf("message[5] = %q, want current_message envelope", req.Messages[5].Content)
	}
	if !strings.Contains(req.Messages[5].Content, "how are you") {
		t.Fatalf("message[5] = %q, want stripped current prompt", req.Messages[5].Content)
	}
	if !strings.Contains(req.Messages[5].Content, `reply_thread_included="2"`) {
		t.Fatalf("message[5] = %q, want reply_thread_included context status", req.Messages[5].Content)
	}
	if !strings.Contains(req.Messages[5].Content, `leading_context_included="1"`) {
		t.Fatalf("message[5] = %q, want leading_context_included context status", req.Messages[5].Content)
	}
	if req.Metadata[metadataKeyAgent] != "Otogi" {
		t.Fatalf("metadata[%s] = %q, want Otogi", metadataKeyAgent, req.Metadata[metadataKeyAgent])
	}
	if req.Metadata[metadataKeyProvider] != "p" {
		t.Fatalf("metadata[%s] = %q, want p", metadataKeyProvider, req.Metadata[metadataKeyProvider])
	}
	if req.Metadata[metadataKeyConversationID] != "chat-1" {
		t.Fatalf(
			"metadata[%s] = %q, want chat-1",
			metadataKeyConversationID,
			req.Metadata[metadataKeyConversationID],
		)
	}
	if req.Metadata["gemini.google_search"] != "true" {
		t.Fatalf("metadata[gemini.google_search] = %q, want true", req.Metadata["gemini.google_search"])
	}
	if req.Metadata["gemini.url_context"] != "false" {
		t.Fatalf("metadata[gemini.url_context] = %q, want false", req.Metadata["gemini.url_context"])
	}
	if req.Metadata["gemini.thinking_budget"] != "32" {
		t.Fatalf("metadata[gemini.thinking_budget] = %q, want 32", req.Metadata["gemini.thinking_budget"])
	}
	if req.Metadata["gemini.include_thoughts"] != "true" {
		t.Fatalf("metadata[gemini.include_thoughts] = %q, want true", req.Metadata["gemini.include_thoughts"])
	}
	if req.Metadata["gemini.thinking_level"] != "medium" {
		t.Fatalf("metadata[gemini.thinking_level] = %q, want medium", req.Metadata["gemini.thinking_level"])
	}
	if req.Metadata["gemini.response_mime_type"] != "application/json" {
		t.Fatalf(
			"metadata[gemini.response_mime_type] = %q, want application/json",
			req.Metadata["gemini.response_mime_type"],
		)
	}
}

func TestBuildGenerateRequestIncludesCurrentEventImages(t *testing.T) {
	module := newTestModule(Config{
		RequestTimeout: time.Second,
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Description:          "assistant",
				Provider:             "p",
				Model:                "gpt-test",
				SystemPromptTemplate: "You are {{.AgentName}}",
				RequestTimeout:       time.Second,
				ImageInputs: ImageInputPolicy{
					Enabled:       true,
					MaxImages:     2,
					MaxImageBytes: 1 << 20,
					MaxTotalBytes: 2 << 20,
					Detail:        ai.LLMInputImageDetailHigh,
				},
			},
		},
	})

	module.memory = &memoryStub{
		replyChain: []core.ReplyChainEntry{
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u1", Username: "alice"},
				Article: platform.Article{
					ID:    "m1",
					Text:  "trigger text",
					Media: []platform.MediaAttachment{{ID: "photo-1", Type: platform.MediaTypePhoto, Caption: "diagram"}},
				},
				CreatedAt: time.Unix(100, 0).UTC(),
				IsCurrent: true,
			},
		},
	}
	module.mediaDownloader = &mediaDownloaderStub{
		data:       []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43, 0x00},
		attachment: platform.MediaAttachment{ID: "photo-1", MIMEType: "image/jpeg", FileName: "diagram.jpg"},
	}
	module.clock = func() time.Time { return time.Unix(100, 0).UTC() }

	req, err := module.buildGenerateRequest(context.Background(), &platform.Event{
		ID:         "evt-1",
		Kind:       platform.EventKindArticleCreated,
		OccurredAt: time.Unix(100, 0).UTC(),
		Source:     platform.EventSource{Platform: platform.PlatformTelegram, ID: "tg-main"},
		Conversation: platform.Conversation{
			ID:   "chat-1",
			Type: platform.ConversationTypeGroup,
		},
		Actor: platform.Actor{ID: "u1", Username: "alice"},
		Article: &platform.Article{
			ID:    "m1",
			Text:  "Otogi inspect this",
			Media: []platform.MediaAttachment{{ID: "photo-1", Type: platform.MediaTypePhoto, Caption: "diagram"}},
		},
	}, module.cfg.Agents[0], "inspect this")
	if err != nil {
		t.Fatalf("buildGenerateRequest failed: %v", err)
	}

	last := req.Messages[len(req.Messages)-1]
	if last.Content != "" {
		t.Fatalf("last.Content = %q, want multimodal parts", last.Content)
	}
	if len(last.Parts) != 2 {
		t.Fatalf("last.Parts len = %d, want 2", len(last.Parts))
	}
	if last.Parts[0].Type != ai.LLMMessagePartTypeText {
		t.Fatalf("last.Parts[0].Type = %q, want text", last.Parts[0].Type)
	}
	if !strings.Contains(last.Parts[0].Text, "<current_message") {
		t.Fatalf("last text part = %q, want current_message envelope", last.Parts[0].Text)
	}
	if !strings.Contains(last.Parts[0].Text, "<image_inputs") {
		t.Fatalf("last text part = %q, want image_inputs summary", last.Parts[0].Text)
	}
	if last.Parts[1].Type != ai.LLMMessagePartTypeImage {
		t.Fatalf("last.Parts[1].Type = %q, want image", last.Parts[1].Type)
	}
	if last.Parts[1].Image == nil {
		t.Fatal("last image part is nil")
	}
	if last.Parts[1].Image.MIMEType != "image/jpeg" {
		t.Fatalf("image MIMEType = %q, want image/jpeg", last.Parts[1].Image.MIMEType)
	}
	if last.Parts[1].Image.Detail != ai.LLMInputImageDetailHigh {
		t.Fatalf("image detail = %q, want high", last.Parts[1].Image.Detail)
	}
	downloader, ok := module.mediaDownloader.(*mediaDownloaderStub)
	if !ok {
		t.Fatalf("module.mediaDownloader type = %T, want *mediaDownloaderStub", module.mediaDownloader)
	}
	if downloader.lastRequest.Source.ID != "tg-main" {
		t.Fatalf("download source id = %q, want tg-main", downloader.lastRequest.Source.ID)
	}
	if downloader.lastRequest.ArticleID != "m1" {
		t.Fatalf("download article id = %q, want m1", downloader.lastRequest.ArticleID)
	}
	if downloader.lastRequest.AttachmentID != "photo-1" {
		t.Fatalf("download attachment id = %q, want photo-1", downloader.lastRequest.AttachmentID)
	}
}

func TestBuildGenerateRequestIncludesLeadingContextImageOnlyMessage(t *testing.T) {
	module := newTestModule(Config{
		RequestTimeout: time.Second,
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Description:          "assistant",
				Provider:             "p",
				Model:                "gpt-test",
				SystemPromptTemplate: "You are {{.AgentName}}",
				RequestTimeout:       time.Second,
				ContextPolicy: ContextPolicy{
					ReplyChainMaxMessages:  4,
					LeadingContextMessages: 2,
					LeadingContextMaxAge:   15 * time.Minute,
					MaxContextRunes:        4000,
					MaxMessageRunes:        200,
				},
				ImageInputs: ImageInputPolicy{
					Enabled:       true,
					MaxImages:     2,
					MaxImageBytes: 1 << 20,
					MaxTotalBytes: 2 << 20,
				},
			},
		},
	})

	downloader := &mediaDownloaderStub{
		data:       []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43, 0x00},
		attachment: platform.MediaAttachment{ID: "photo-1", MIMEType: "image/jpeg"},
	}
	module.memory = &memoryStub{
		replyChain: []core.ReplyChainEntry{
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u2", Username: "alice"},
				Article:      platform.Article{ID: "m2", Text: "trigger text"},
				CreatedAt:    time.Unix(100, 0).UTC(),
				IsCurrent:    true,
			},
		},
		leadingContext: []core.ConversationContextEntry{
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u1", Username: "alice"},
				Article: platform.Article{
					ID:    "m1",
					Text:  "",
					Media: []platform.MediaAttachment{{ID: "photo-1", Type: platform.MediaTypePhoto}},
				},
				CreatedAt: time.Unix(95, 0).UTC(),
			},
		},
	}
	module.mediaDownloader = downloader
	module.clock = func() time.Time { return time.Unix(100, 0).UTC() }

	req, err := module.buildGenerateRequest(context.Background(), &platform.Event{
		ID:         "evt-1",
		Kind:       platform.EventKindArticleCreated,
		OccurredAt: time.Unix(100, 0).UTC(),
		Source:     platform.EventSource{Platform: platform.PlatformTelegram, ID: "tg-main"},
		Conversation: platform.Conversation{
			ID:   "chat-1",
			Type: platform.ConversationTypeGroup,
		},
		Actor: platform.Actor{ID: "u2", Username: "alice"},
		Article: &platform.Article{
			ID:   "m2",
			Text: "Otogi，描述一下上面的图片",
		},
	}, module.cfg.Agents[0], "描述一下上面的图片")
	if err != nil {
		t.Fatalf("buildGenerateRequest failed: %v", err)
	}

	if len(req.Messages) != 4 {
		t.Fatalf("messages len = %d, want 4", len(req.Messages))
	}
	// Leading context message should be text-only (no Parts).
	leading := req.Messages[2]
	if len(leading.Parts) != 0 {
		t.Fatalf("leading.Parts len = %d, want 0 (text-only)", len(leading.Parts))
	}
	if !strings.Contains(leading.Content, `media_only="true"`) {
		t.Fatalf("leading.Content = %q, want media_only flag", leading.Content)
	}
	// All images are attached to the current (last) message.
	last := req.Messages[3]
	if len(last.Parts) != 2 {
		t.Fatalf("last.Parts len = %d, want 2", len(last.Parts))
	}
	if !strings.Contains(last.Parts[0].Text, "<current_message") {
		t.Fatalf("last text part = %q, want current_message envelope", last.Parts[0].Text)
	}
	if last.Parts[1].Image == nil || last.Parts[1].Image.MIMEType != "image/jpeg" {
		t.Fatalf("last image part = %+v, want image/jpeg", last.Parts[1].Image)
	}
	if len(downloader.requests) != 1 {
		t.Fatalf("download requests len = %d, want 1", len(downloader.requests))
	}
	if downloader.requests[0].ArticleID != "m1" {
		t.Fatalf("download article id = %q, want m1", downloader.requests[0].ArticleID)
	}
}

func TestBuildGenerateRequestIncludesReplyThreadImageOnlyMessage(t *testing.T) {
	module := newTestModule(Config{
		RequestTimeout: time.Second,
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Description:          "assistant",
				Provider:             "p",
				Model:                "gpt-test",
				SystemPromptTemplate: "You are {{.AgentName}}",
				RequestTimeout:       time.Second,
				ContextPolicy: ContextPolicy{
					ReplyChainMaxMessages:  4,
					LeadingContextMessages: 0,
					LeadingContextMaxAge:   15 * time.Minute,
					MaxContextRunes:        4000,
					MaxMessageRunes:        200,
				},
				ImageInputs: ImageInputPolicy{
					Enabled:       true,
					MaxImages:     2,
					MaxImageBytes: 1 << 20,
					MaxTotalBytes: 2 << 20,
				},
			},
		},
	})

	downloader := &mediaDownloaderStub{
		data:       []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43, 0x00},
		attachment: platform.MediaAttachment{ID: "photo-1", MIMEType: "image/jpeg"},
	}
	module.memory = &memoryStub{
		replyChain: []core.ReplyChainEntry{
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u1", Username: "alice"},
				Article: platform.Article{
					ID:    "m1",
					Text:  "",
					Media: []platform.MediaAttachment{{ID: "photo-1", Type: platform.MediaTypePhoto}},
				},
				CreatedAt: time.Unix(95, 0).UTC(),
			},
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u2", Username: "alice"},
				Article:      platform.Article{ID: "m2", ReplyToArticleID: "m1", Text: "trigger text"},
				CreatedAt:    time.Unix(100, 0).UTC(),
				IsCurrent:    true,
			},
		},
	}
	module.mediaDownloader = downloader
	module.clock = func() time.Time { return time.Unix(100, 0).UTC() }

	req, err := module.buildGenerateRequest(context.Background(), &platform.Event{
		ID:         "evt-1",
		Kind:       platform.EventKindArticleCreated,
		OccurredAt: time.Unix(100, 0).UTC(),
		Source:     platform.EventSource{Platform: platform.PlatformTelegram, ID: "tg-main"},
		Conversation: platform.Conversation{
			ID:   "chat-1",
			Type: platform.ConversationTypeGroup,
		},
		Actor: platform.Actor{ID: "u2", Username: "alice"},
		Article: &platform.Article{
			ID:               "m2",
			ReplyToArticleID: "m1",
			Text:             "Otogi，描述一下上面的图片",
		},
	}, module.cfg.Agents[0], "描述一下上面的图片")
	if err != nil {
		t.Fatalf("buildGenerateRequest failed: %v", err)
	}

	if len(req.Messages) != 4 {
		t.Fatalf("messages len = %d, want 4", len(req.Messages))
	}
	// Reply thread message should be text-only (no Parts).
	reply := req.Messages[2]
	if len(reply.Parts) != 0 {
		t.Fatalf("reply.Parts len = %d, want 0 (text-only)", len(reply.Parts))
	}
	if !strings.Contains(reply.Content, `media_only="true"`) {
		t.Fatalf("reply.Content = %q, want media_only flag", reply.Content)
	}
	// All images are attached to the current (last) message.
	last := req.Messages[3]
	if len(last.Parts) != 2 {
		t.Fatalf("last.Parts len = %d, want 2", len(last.Parts))
	}
	if !strings.Contains(last.Parts[0].Text, "<current_message") {
		t.Fatalf("last text part = %q, want current_message envelope", last.Parts[0].Text)
	}
	if last.Parts[1].Image == nil || last.Parts[1].Image.MIMEType != "image/jpeg" {
		t.Fatalf("last image part = %+v, want image/jpeg", last.Parts[1].Image)
	}
	if len(downloader.requests) != 1 {
		t.Fatalf("download requests len = %d, want 1", len(downloader.requests))
	}
	if downloader.requests[0].ArticleID != "m1" {
		t.Fatalf("download article id = %q, want m1", downloader.requests[0].ArticleID)
	}
}

func TestBuildGenerateRequestDownloadsImagesConcurrentlyAndPreservesPriorityOrder(t *testing.T) {
	module := newTestModule(Config{
		RequestTimeout: time.Second,
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Description:          "assistant",
				Provider:             "p",
				Model:                "gpt-test",
				SystemPromptTemplate: "You are {{.AgentName}}",
				RequestTimeout:       time.Second,
				ContextPolicy: ContextPolicy{
					ReplyChainMaxMessages:  4,
					LeadingContextMessages: 2,
					LeadingContextMaxAge:   15 * time.Minute,
					MaxContextRunes:        4000,
					MaxMessageRunes:        200,
				},
				ImageInputs: ImageInputPolicy{
					Enabled:       true,
					MaxImages:     3,
					MaxImageBytes: 1 << 20,
					MaxTotalBytes: 3 << 20,
				},
			},
		},
	})

	downloader := &mediaDownloaderStub{
		data:          []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43, 0x00},
		attachment:    platform.MediaAttachment{MIMEType: "image/jpeg"},
		downloadDelay: 20 * time.Millisecond,
	}
	module.memory = &memoryStub{
		replyChain: []core.ReplyChainEntry{
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u1", Username: "alice"},
				Article: platform.Article{
					ID:    "m1",
					Text:  "root message",
					Media: []platform.MediaAttachment{{ID: "photo-reply", Type: platform.MediaTypePhoto}},
				},
				CreatedAt: time.Unix(95, 0).UTC(),
			},
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u2", Username: "bob"},
				Article: platform.Article{
					ID:               "m2",
					ReplyToArticleID: "m1",
					Text:             "trigger text",
					Media:            []platform.MediaAttachment{{ID: "photo-current", Type: platform.MediaTypePhoto}},
				},
				CreatedAt: time.Unix(100, 0).UTC(),
				IsCurrent: true,
			},
		},
		leadingContext: []core.ConversationContextEntry{
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u0", Username: "eve"},
				Article: platform.Article{
					ID:    "ctx-1",
					Text:  "earlier context",
					Media: []platform.MediaAttachment{{ID: "photo-leading", Type: platform.MediaTypePhoto}},
				},
				CreatedAt: time.Unix(90, 0).UTC(),
			},
		},
	}
	module.mediaDownloader = downloader
	module.clock = func() time.Time { return time.Unix(100, 0).UTC() }

	req, err := module.buildGenerateRequest(context.Background(), &platform.Event{
		ID:         "evt-1",
		Kind:       platform.EventKindArticleCreated,
		OccurredAt: time.Unix(100, 0).UTC(),
		Source:     platform.EventSource{Platform: platform.PlatformTelegram, ID: "tg-main"},
		Conversation: platform.Conversation{
			ID:   "chat-1",
			Type: platform.ConversationTypeGroup,
		},
		Actor: platform.Actor{ID: "u2", Username: "bob"},
		Article: &platform.Article{
			ID:               "m2",
			ReplyToArticleID: "m1",
			Text:             "Otogi inspect all three",
			Media: []platform.MediaAttachment{
				{ID: "photo-current", Type: platform.MediaTypePhoto},
			},
		},
	}, module.cfg.Agents[0], "inspect all three")
	if err != nil {
		t.Fatalf("buildGenerateRequest failed: %v", err)
	}

	if got := downloader.maxConcurrent.Load(); got < 2 {
		t.Fatalf("max concurrent downloads = %d, want at least 2", got)
	}
	if len(downloader.requests) != 3 {
		t.Fatalf("download requests len = %d, want 3", len(downloader.requests))
	}

	last := req.Messages[len(req.Messages)-1]
	if len(last.Parts) != 4 {
		t.Fatalf("last.Parts len = %d, want 4 (text + 3 images)", len(last.Parts))
	}
	textPart := last.Parts[0].Text
	currentPos := strings.Index(textPart, `article_id="m2"`)
	replyPos := strings.Index(textPart, `article_id="m1"`)
	leadingPos := strings.Index(textPart, `article_id="ctx-1"`)
	if currentPos < 0 || replyPos < 0 || leadingPos < 0 {
		t.Fatalf("text part = %q, want all article ids in image summary", textPart)
	}
	if !(currentPos < replyPos && replyPos < leadingPos) {
		t.Fatalf("text part order = %q, want current then reply then leading", textPart)
	}
}

func TestBuildGenerateRequestDeduplicatesImagesAcrossContextSources(t *testing.T) {
	module := newTestModule(Config{
		RequestTimeout: time.Second,
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Description:          "assistant",
				Provider:             "p",
				Model:                "gpt-test",
				SystemPromptTemplate: "You are {{.AgentName}}",
				RequestTimeout:       time.Second,
				ContextPolicy: ContextPolicy{
					ReplyChainMaxMessages:  4,
					LeadingContextMessages: 2,
					LeadingContextMaxAge:   15 * time.Minute,
					MaxContextRunes:        4000,
					MaxMessageRunes:        200,
				},
				ImageInputs: ImageInputPolicy{
					Enabled:       true,
					MaxImages:     3,
					MaxImageBytes: 1 << 20,
					MaxTotalBytes: 3 << 20,
				},
			},
		},
	})

	downloader := &mediaDownloaderStub{
		data:       []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43, 0x00},
		attachment: platform.MediaAttachment{ID: "photo-shared", MIMEType: "image/jpeg"},
	}
	module.memory = &memoryStub{
		replyChain: []core.ReplyChainEntry{
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u1", Username: "alice"},
				Article: platform.Article{
					ID:    "m1",
					Text:  "reply image",
					Media: []platform.MediaAttachment{{ID: "photo-shared", Type: platform.MediaTypePhoto}},
				},
				CreatedAt: time.Unix(95, 0).UTC(),
			},
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u2", Username: "bob"},
				Article: platform.Article{
					ID:               "m2",
					ReplyToArticleID: "m1",
					Text:             "trigger text",
					Media:            []platform.MediaAttachment{{ID: "photo-shared", Type: platform.MediaTypePhoto}},
				},
				CreatedAt: time.Unix(100, 0).UTC(),
				IsCurrent: true,
			},
		},
		leadingContext: []core.ConversationContextEntry{
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u0", Username: "eve"},
				Article: platform.Article{
					ID:    "ctx-1",
					Text:  "leading image",
					Media: []platform.MediaAttachment{{ID: "photo-shared", Type: platform.MediaTypePhoto}},
				},
				CreatedAt: time.Unix(90, 0).UTC(),
			},
		},
	}
	module.mediaDownloader = downloader
	module.clock = func() time.Time { return time.Unix(100, 0).UTC() }

	req, err := module.buildGenerateRequest(context.Background(), &platform.Event{
		ID:         "evt-1",
		Kind:       platform.EventKindArticleCreated,
		OccurredAt: time.Unix(100, 0).UTC(),
		Source:     platform.EventSource{Platform: platform.PlatformTelegram, ID: "tg-main"},
		Conversation: platform.Conversation{
			ID:   "chat-1",
			Type: platform.ConversationTypeGroup,
		},
		Actor: platform.Actor{ID: "u2", Username: "bob"},
		Article: &platform.Article{
			ID:               "m2",
			ReplyToArticleID: "m1",
			Text:             "Otogi inspect shared image",
			Media:            []platform.MediaAttachment{{ID: "photo-shared", Type: platform.MediaTypePhoto}},
		},
	}, module.cfg.Agents[0], "inspect shared image")
	if err != nil {
		t.Fatalf("buildGenerateRequest failed: %v", err)
	}

	if len(downloader.requests) != 1 {
		t.Fatalf("download requests len = %d, want 1", len(downloader.requests))
	}
	if downloader.requests[0].ArticleID != "m2" {
		t.Fatalf("download article id = %q, want m2", downloader.requests[0].ArticleID)
	}

	last := req.Messages[len(req.Messages)-1]
	if len(last.Parts) != 2 {
		t.Fatalf("last.Parts len = %d, want 2 (text + 1 deduplicated image)", len(last.Parts))
	}
	textPart := last.Parts[0].Text
	if !strings.Contains(textPart, `article_id="m2"`) {
		t.Fatalf("text part = %q, want current article id in image summary", textPart)
	}
	if strings.Contains(textPart, `article_id="m1"`) {
		t.Fatalf("text part = %q, should not include duplicate reply image", textPart)
	}
	if strings.Contains(textPart, `article_id="ctx-1"`) {
		t.Fatalf("text part = %q, should not include duplicate leading image", textPart)
	}
}

func TestBuildGenerateRequestIncludesBotSentImageOnCurrentMessage(t *testing.T) {
	module := newTestModule(Config{
		RequestTimeout: time.Second,
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Description:          "assistant",
				Provider:             "p",
				Model:                "gpt-test",
				SystemPromptTemplate: "You are {{.AgentName}}",
				RequestTimeout:       time.Second,
				ContextPolicy: ContextPolicy{
					ReplyChainMaxMessages:  4,
					LeadingContextMessages: 0,
					LeadingContextMaxAge:   15 * time.Minute,
					MaxContextRunes:        4000,
					MaxMessageRunes:        200,
				},
				ImageInputs: ImageInputPolicy{
					Enabled:       true,
					MaxImages:     2,
					MaxImageBytes: 1 << 20,
					MaxTotalBytes: 2 << 20,
				},
			},
		},
	})

	downloader := &mediaDownloaderStub{
		data:       []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43, 0x00},
		attachment: platform.MediaAttachment{ID: "photo-1", MIMEType: "image/jpeg"},
	}
	module.memory = &memoryStub{
		replyChain: []core.ReplyChainEntry{
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "bot-1", Username: "thebot", IsBot: true},
				Article: platform.Article{
					ID:    "m1",
					Text:  "here is the result",
					Media: []platform.MediaAttachment{{ID: "photo-1", Type: platform.MediaTypePhoto}},
				},
				CreatedAt: time.Unix(95, 0).UTC(),
			},
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u2", Username: "alice"},
				Article:      platform.Article{ID: "m2", ReplyToArticleID: "m1", Text: "trigger text"},
				CreatedAt:    time.Unix(100, 0).UTC(),
				IsCurrent:    true,
			},
		},
	}
	module.mediaDownloader = downloader
	module.clock = func() time.Time { return time.Unix(100, 0).UTC() }

	req, err := module.buildGenerateRequest(context.Background(), &platform.Event{
		ID:         "evt-1",
		Kind:       platform.EventKindArticleCreated,
		OccurredAt: time.Unix(100, 0).UTC(),
		Source:     platform.EventSource{Platform: platform.PlatformTelegram, ID: "tg-main"},
		Conversation: platform.Conversation{
			ID:   "chat-1",
			Type: platform.ConversationTypeGroup,
		},
		Actor: platform.Actor{ID: "u2", Username: "alice"},
		Article: &platform.Article{
			ID:               "m2",
			ReplyToArticleID: "m1",
			Text:             "Otogi describe what you sent",
		},
	}, module.cfg.Agents[0], "describe what you sent")
	if err != nil {
		t.Fatalf("buildGenerateRequest failed: %v", err)
	}

	if len(req.Messages) != 4 {
		t.Fatalf("messages len = %d, want 4", len(req.Messages))
	}
	// Bot reply thread message should be assistant role, text-only.
	botMsg := req.Messages[2]
	if botMsg.Role != ai.LLMMessageRoleAssistant {
		t.Fatalf("bot message role = %q, want assistant", botMsg.Role)
	}
	if len(botMsg.Parts) != 0 {
		t.Fatalf("bot message Parts len = %d, want 0 (text-only)", len(botMsg.Parts))
	}
	// Bot-sent image should appear on the current (last) user message.
	last := req.Messages[3]
	if len(last.Parts) != 2 {
		t.Fatalf("last.Parts len = %d, want 2 (text + image)", len(last.Parts))
	}
	if last.Parts[1].Image == nil || last.Parts[1].Image.MIMEType != "image/jpeg" {
		t.Fatalf("last image = %+v, want image/jpeg from bot article", last.Parts[1].Image)
	}
	if len(downloader.requests) != 1 {
		t.Fatalf("download requests len = %d, want 1", len(downloader.requests))
	}
	if downloader.requests[0].ArticleID != "m1" {
		t.Fatalf("download article id = %q, want m1 (bot article)", downloader.requests[0].ArticleID)
	}
}

func TestBuildGenerateRequestSkipsImagesWithoutMediaDownloader(t *testing.T) {
	module := newTestModule(Config{
		RequestTimeout: time.Second,
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Description:          "assistant",
				Provider:             "p",
				Model:                "gpt-test",
				SystemPromptTemplate: "You are {{.AgentName}}",
				RequestTimeout:       time.Second,
				ImageInputs: ImageInputPolicy{
					Enabled: true,
				},
			},
		},
	})

	module.memory = &memoryStub{
		replyChain: []core.ReplyChainEntry{
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u1", Username: "alice"},
				Article: platform.Article{
					ID:    "m1",
					Text:  "trigger text",
					Media: []platform.MediaAttachment{{ID: "photo-1", Type: platform.MediaTypePhoto}},
				},
				CreatedAt: time.Unix(100, 0).UTC(),
				IsCurrent: true,
			},
		},
	}
	module.clock = func() time.Time { return time.Unix(100, 0).UTC() }

	req, err := module.buildGenerateRequest(context.Background(), &platform.Event{
		ID:         "evt-1",
		Kind:       platform.EventKindArticleCreated,
		OccurredAt: time.Unix(100, 0).UTC(),
		Source:     platform.EventSource{Platform: platform.PlatformTelegram, ID: "tg-main"},
		Conversation: platform.Conversation{
			ID:   "chat-1",
			Type: platform.ConversationTypeGroup,
		},
		Actor: platform.Actor{ID: "u1", Username: "alice"},
		Article: &platform.Article{
			ID:    "m1",
			Text:  "Otogi inspect this",
			Media: []platform.MediaAttachment{{ID: "photo-1", Type: platform.MediaTypePhoto}},
		},
	}, module.cfg.Agents[0], "inspect this")
	if err != nil {
		t.Fatalf("buildGenerateRequest failed: %v", err)
	}

	last := req.Messages[len(req.Messages)-1]
	if len(last.Parts) != 0 {
		t.Fatalf("last.Parts len = %d, want 0 when media downloader is absent", len(last.Parts))
	}
	if !strings.Contains(last.Content, "<current_message") {
		t.Fatalf("last.Content = %q, want current_message envelope", last.Content)
	}
}

func TestBuildGenerateRequestSkipsOversizedImages(t *testing.T) {
	module := newTestModule(Config{
		RequestTimeout: time.Second,
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Description:          "assistant",
				Provider:             "p",
				Model:                "gpt-test",
				SystemPromptTemplate: "You are {{.AgentName}}",
				RequestTimeout:       time.Second,
				ImageInputs: ImageInputPolicy{
					Enabled:       true,
					MaxImages:     1,
					MaxImageBytes: 4,
					MaxTotalBytes: 4,
				},
			},
		},
	})

	downloader := &mediaDownloaderStub{
		data:       []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10},
		attachment: platform.MediaAttachment{ID: "photo-1", MIMEType: "image/jpeg"},
	}
	module.memory = &memoryStub{
		replyChain: []core.ReplyChainEntry{
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u1", Username: "alice"},
				Article: platform.Article{
					ID:    "m1",
					Text:  "trigger text",
					Media: []platform.MediaAttachment{{ID: "photo-1", Type: platform.MediaTypePhoto, SizeBytes: 8}},
				},
				CreatedAt: time.Unix(100, 0).UTC(),
				IsCurrent: true,
			},
		},
	}
	module.mediaDownloader = downloader
	module.clock = func() time.Time { return time.Unix(100, 0).UTC() }

	req, err := module.buildGenerateRequest(context.Background(), &platform.Event{
		ID:         "evt-1",
		Kind:       platform.EventKindArticleCreated,
		OccurredAt: time.Unix(100, 0).UTC(),
		Source:     platform.EventSource{Platform: platform.PlatformTelegram, ID: "tg-main"},
		Conversation: platform.Conversation{
			ID:   "chat-1",
			Type: platform.ConversationTypeGroup,
		},
		Actor: platform.Actor{ID: "u1", Username: "alice"},
		Article: &platform.Article{
			ID:    "m1",
			Text:  "Otogi inspect this",
			Media: []platform.MediaAttachment{{ID: "photo-1", Type: platform.MediaTypePhoto, SizeBytes: 8}},
		},
	}, module.cfg.Agents[0], "inspect this")
	if err != nil {
		t.Fatalf("buildGenerateRequest failed: %v", err)
	}

	last := req.Messages[len(req.Messages)-1]
	if len(last.Parts) != 0 {
		t.Fatalf("last.Parts len = %d, want 0 for oversized image", len(last.Parts))
	}
	if downloader.lastRequest.AttachmentID != "" {
		t.Fatalf("download attachment id = %q, want no download attempt", downloader.lastRequest.AttachmentID)
	}
}

func TestBuildGenerateRequestIncludesImageDocumentAttachments(t *testing.T) {
	module := newTestModule(Config{
		RequestTimeout: time.Second,
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Description:          "assistant",
				Provider:             "p",
				Model:                "gpt-test",
				SystemPromptTemplate: "You are {{.AgentName}}",
				RequestTimeout:       time.Second,
				ImageInputs: ImageInputPolicy{
					Enabled:       true,
					MaxImages:     1,
					MaxImageBytes: 1 << 20,
					MaxTotalBytes: 1 << 20,
				},
			},
		},
	})

	module.memory = &memoryStub{
		replyChain: []core.ReplyChainEntry{
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u1", Username: "alice"},
				Article: platform.Article{
					ID:   "m1",
					Text: "trigger text",
					Media: []platform.MediaAttachment{{
						ID:       "doc-1",
						Type:     platform.MediaTypeDocument,
						MIMEType: "image/png",
						FileName: "chart.png",
					}},
				},
				CreatedAt: time.Unix(100, 0).UTC(),
				IsCurrent: true,
			},
		},
	}
	module.mediaDownloader = &mediaDownloaderStub{
		data: []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a},
	}
	module.clock = func() time.Time { return time.Unix(100, 0).UTC() }

	req, err := module.buildGenerateRequest(context.Background(), &platform.Event{
		ID:         "evt-1",
		Kind:       platform.EventKindArticleCreated,
		OccurredAt: time.Unix(100, 0).UTC(),
		Source:     platform.EventSource{Platform: platform.PlatformTelegram, ID: "tg-main"},
		Conversation: platform.Conversation{
			ID:   "chat-1",
			Type: platform.ConversationTypeGroup,
		},
		Actor: platform.Actor{ID: "u1", Username: "alice"},
		Article: &platform.Article{
			ID:   "m1",
			Text: "Otogi inspect this",
			Media: []platform.MediaAttachment{{
				ID:       "doc-1",
				Type:     platform.MediaTypeDocument,
				MIMEType: "image/png",
				FileName: "chart.png",
			}},
		},
	}, module.cfg.Agents[0], "inspect this")
	if err != nil {
		t.Fatalf("buildGenerateRequest failed: %v", err)
	}

	last := req.Messages[len(req.Messages)-1]
	if len(last.Parts) != 2 {
		t.Fatalf("last.Parts len = %d, want 2", len(last.Parts))
	}
	if last.Parts[1].Image == nil {
		t.Fatal("last image part is nil")
	}
	if last.Parts[1].Image.MIMEType != "image/png" {
		t.Fatalf("image MIMEType = %q, want image/png", last.Parts[1].Image.MIMEType)
	}
}

func TestBuildGenerateRequestAppliesContextBudgets(t *testing.T) {
	module := newTestModule(Config{
		RequestTimeout: time.Second,
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Description:          "assistant",
				Provider:             "p",
				Model:                "gpt-test",
				SystemPromptTemplate: "You are {{.AgentName}}",
				RequestTimeout:       time.Second,
				ContextPolicy: ContextPolicy{
					ReplyChainMaxMessages:  3,
					LeadingContextMessages: 2,
					LeadingContextMaxAge:   15 * time.Minute,
					MaxContextRunes:        420,
					MaxMessageRunes:        48,
				},
			},
		},
	})

	module.memory = &memoryStub{
		leadingContext: []core.ConversationContextEntry{
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u0", Username: "eve"},
				Article:      platform.Article{ID: "ctx-1", Text: strings.Repeat("leading ", 20)},
				CreatedAt:    time.Unix(70, 0).UTC(),
			},
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u9", Username: "mallory"},
				Article:      platform.Article{ID: "ctx-2", Text: strings.Repeat("more-leading ", 20)},
				CreatedAt:    time.Unix(80, 0).UTC(),
			},
		},
		replyChain: []core.ReplyChainEntry{
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u1", Username: "alice"},
				Article:      platform.Article{ID: "m1", Text: strings.Repeat("root ", 20)},
				CreatedAt:    time.Unix(90, 0).UTC(),
			},
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u2", Username: "bob"},
				Article:      platform.Article{ID: "m2", ReplyToArticleID: "m1", Text: strings.Repeat("middle-one ", 20)},
				CreatedAt:    time.Unix(95, 0).UTC(),
			},
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u3", Username: "carol"},
				Article:      platform.Article{ID: "m3", ReplyToArticleID: "m2", Text: strings.Repeat("middle-two ", 20)},
				CreatedAt:    time.Unix(100, 0).UTC(),
			},
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u4", DisplayName: "Dave"},
				Article:      platform.Article{ID: "m4", ReplyToArticleID: "m3", Text: "trigger text"},
				CreatedAt:    time.Unix(105, 0).UTC(),
				IsCurrent:    true,
			},
		},
	}
	module.clock = func() time.Time { return time.Unix(105, 0).UTC() }

	req, err := module.buildGenerateRequest(context.Background(), &platform.Event{
		ID:         "evt-2",
		Kind:       platform.EventKindArticleCreated,
		OccurredAt: time.Unix(105, 0).UTC(),
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
		Actor:        platform.Actor{ID: "u4", DisplayName: "Dave"},
		Article: &platform.Article{
			ID:               "m4",
			ReplyToArticleID: "m3",
			Text:             "Otogi summarize",
		},
	}, module.cfg.Agents[0], strings.Repeat("current request ", 12))
	if err != nil {
		t.Fatalf("buildGenerateRequest failed: %v", err)
	}

	if len(req.Messages) < 3 {
		t.Fatalf("messages len = %d, want at least 3", len(req.Messages))
	}
	last := req.Messages[len(req.Messages)-1].Content
	if strings.Contains(last, "<leading_context") {
		t.Fatalf("current message should not embed leading_context, got %q", last)
	}
	if !strings.Contains(last, `reply_thread_omitted="`) {
		t.Fatalf("current message = %q, want omitted reply-thread count", last)
	}
	if !strings.Contains(last, `leading_context_omitted="`) {
		t.Fatalf("current message = %q, want omitted leading-context count", last)
	}
	if strings.Contains(strings.Join(allMessageContents(req.Messages), "\n"), "middle-one middle-one") {
		t.Fatalf("messages should drop over-budget oldest middle content: %+v", req.Messages)
	}
	if strings.Contains(strings.Join(allMessageContents(req.Messages), "\n"), "<leading_context") {
		t.Fatalf("messages should omit leading_context when budget is tight: %+v", req.Messages)
	}
	if !strings.Contains(last, "...") {
		t.Fatalf("current message = %q, want per-message truncation ellipsis", last)
	}
}

func TestBuildGenerateRequestNonReplyUsesRecentConversationContext(t *testing.T) {
	module := newTestModule(Config{
		RequestTimeout: time.Second,
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Description:          "assistant",
				Provider:             "p",
				Model:                "gpt-test",
				SystemPromptTemplate: "You are {{.AgentName}}",
				RequestTimeout:       time.Second,
				ContextPolicy: ContextPolicy{
					ReplyChainMaxMessages:  8,
					LeadingContextMessages: 3,
					LeadingContextMaxAge:   15 * time.Minute,
					MaxContextRunes:        4000,
					MaxMessageRunes:        200,
				},
			},
		},
	})

	module.memory = &memoryStub{
		leadingContext: []core.ConversationContextEntry{
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u1", Username: "alice"},
				Article:      platform.Article{ID: "m1", Text: "earlier discussion"},
				CreatedAt:    time.Unix(90, 0).UTC(),
			},
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u2", Username: "bob"},
				Article:      platform.Article{ID: "m2", Text: "latest local context"},
				CreatedAt:    time.Unix(95, 0).UTC(),
			},
		},
		replyChain: []core.ReplyChainEntry{
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u3", DisplayName: "Carol"},
				Article:      platform.Article{ID: "m3", Text: "trigger text"},
				CreatedAt:    time.Unix(100, 0).UTC(),
				IsCurrent:    true,
			},
		},
	}
	module.clock = func() time.Time { return time.Unix(100, 0).UTC() }

	req, err := module.buildGenerateRequest(context.Background(), &platform.Event{
		ID:         "evt-3",
		Kind:       platform.EventKindArticleCreated,
		OccurredAt: time.Unix(100, 0).UTC(),
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
		Actor:        platform.Actor{ID: "u3", DisplayName: "Carol"},
		Article: &platform.Article{
			ID:   "m3",
			Text: "Otogi summarize",
		},
	}, module.cfg.Agents[0], "what did we agree on?")
	if err != nil {
		t.Fatalf("buildGenerateRequest failed: %v", err)
	}

	if len(req.Messages) != 4 {
		t.Fatalf("messages len = %d, want 4", len(req.Messages))
	}
	if req.Messages[2].Role != ai.LLMMessageRoleUser {
		t.Fatalf("message[2] role = %q, want user", req.Messages[2].Role)
	}
	if !strings.Contains(req.Messages[2].Content, `<leading_context reason="messages_before_current_message">`) {
		t.Fatalf("message[2] = %q, want current-message leading_context", req.Messages[2].Content)
	}
	if !strings.Contains(req.Messages[2].Content, "latest local context") {
		t.Fatalf("message[2] = %q, want recent context body", req.Messages[2].Content)
	}
	if strings.Contains(req.Messages[2].Content, "<reply_thread_message") {
		t.Fatalf("message[2] = %q, should not include reply thread on non-reply trigger", req.Messages[2].Content)
	}
	if !strings.Contains(req.Messages[3].Content, `reply_thread_included="0"`) {
		t.Fatalf("message[3] = %q, want zero reply-thread count", req.Messages[3].Content)
	}
	if !strings.Contains(req.Messages[3].Content, `leading_context_included="2"`) {
		t.Fatalf("message[3] = %q, want leading context count", req.Messages[3].Content)
	}
	if !strings.Contains(req.Messages[3].Content, "what did we agree on?") {
		t.Fatalf("message[3] = %q, want stripped current prompt", req.Messages[3].Content)
	}
}

func TestBuildGenerateRequestInlinesQuotedReplies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		replyChain       []core.ReplyChainEntry
		leadingContext   []core.ConversationContextEntry
		memories         map[string]core.Memory
		quoteReplyDepth  int
		event            *platform.Event
		wantQuotedReply  []string
		wantNoQuote      []string
		wantMessageCount int
	}{
		{
			name: "reply_chain_message_quotes_unknown_parent",
			replyChain: []core.ReplyChainEntry{
				{
					Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
					Actor:        platform.Actor{ID: "u1", Username: "alice"},
					Article:      platform.Article{ID: "m2", ReplyToArticleID: "m1", Text: "reply to m1"},
					CreatedAt:    time.Unix(90, 0).UTC(),
				},
				{
					Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
					Actor:        platform.Actor{ID: "u2", Username: "bob"},
					Article:      platform.Article{ID: "m3", ReplyToArticleID: "m2", Text: "trigger text"},
					CreatedAt:    time.Unix(100, 0).UTC(),
					IsCurrent:    true,
				},
			},
			memories: map[string]core.Memory{
				"m1": {
					Actor:     platform.Actor{ID: "u0", Username: "eve"},
					Article:   platform.Article{ID: "m1", Text: "original message from eve"},
					CreatedAt: time.Unix(80, 0).UTC(),
				},
			},
			quoteReplyDepth: 2,
			event: &platform.Event{
				ID:         "evt-1",
				Kind:       platform.EventKindArticleCreated,
				OccurredAt: time.Unix(100, 0).UTC(),
				Source:     platform.EventSource{Platform: platform.PlatformTelegram, ID: "tg"},
				Conversation: platform.Conversation{
					ID:   "chat-1",
					Type: platform.ConversationTypeGroup,
				},
				Actor:   platform.Actor{ID: "u2", Username: "bob"},
				Article: &platform.Article{ID: "m3", ReplyToArticleID: "m2", Text: "Otogi hello"},
			},
			wantQuotedReply:  []string{"<quoted_reply", "original message from eve", `article_id="m1"`},
			wantMessageCount: 4,
		},
		{
			name: "nested_quote_depth_2",
			replyChain: []core.ReplyChainEntry{
				{
					Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
					Actor:        platform.Actor{ID: "u3", Username: "carol"},
					Article:      platform.Article{ID: "m4", ReplyToArticleID: "m3", Text: "trigger"},
					CreatedAt:    time.Unix(100, 0).UTC(),
					IsCurrent:    true,
				},
			},
			leadingContext: []core.ConversationContextEntry{
				{
					Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
					Actor:        platform.Actor{ID: "u2", Username: "bob"},
					Article:      platform.Article{ID: "m3", ReplyToArticleID: "m2", Text: "bob replies to m2"},
					CreatedAt:    time.Unix(95, 0).UTC(),
				},
			},
			memories: map[string]core.Memory{
				"m2": {
					Actor:     platform.Actor{ID: "u1", Username: "alice"},
					Article:   platform.Article{ID: "m2", ReplyToArticleID: "m1", Text: "alice replies to m1"},
					CreatedAt: time.Unix(85, 0).UTC(),
				},
				"m1": {
					Actor:     platform.Actor{ID: "u0", Username: "eve"},
					Article:   platform.Article{ID: "m1", Text: "root from eve"},
					CreatedAt: time.Unix(80, 0).UTC(),
				},
			},
			quoteReplyDepth: 2,
			event: &platform.Event{
				ID:         "evt-2",
				Kind:       platform.EventKindArticleCreated,
				OccurredAt: time.Unix(100, 0).UTC(),
				Source:     platform.EventSource{Platform: platform.PlatformTelegram, ID: "tg"},
				Conversation: platform.Conversation{
					ID:   "chat-1",
					Type: platform.ConversationTypeGroup,
				},
				Actor:   platform.Actor{ID: "u3", Username: "carol"},
				Article: &platform.Article{ID: "m4", ReplyToArticleID: "m3", Text: "Otogi hi"},
			},
			wantQuotedReply: []string{
				"alice replies to m1",
				"root from eve",
				`article_id="m2"`,
				`article_id="m1"`,
			},
			wantMessageCount: 4,
		},
		{
			name: "depth_0_disables_quoting",
			replyChain: []core.ReplyChainEntry{
				{
					Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
					Actor:        platform.Actor{ID: "u1", Username: "alice"},
					Article:      platform.Article{ID: "m2", ReplyToArticleID: "m1", Text: "reply to m1"},
					CreatedAt:    time.Unix(90, 0).UTC(),
				},
				{
					Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
					Actor:        platform.Actor{ID: "u2", Username: "bob"},
					Article:      platform.Article{ID: "m3", ReplyToArticleID: "m2", Text: "trigger"},
					CreatedAt:    time.Unix(100, 0).UTC(),
					IsCurrent:    true,
				},
			},
			memories: map[string]core.Memory{
				"m1": {
					Actor:     platform.Actor{ID: "u0", Username: "eve"},
					Article:   platform.Article{ID: "m1", Text: "should not appear"},
					CreatedAt: time.Unix(80, 0).UTC(),
				},
			},
			quoteReplyDepth: 0,
			event: &platform.Event{
				ID:         "evt-3",
				Kind:       platform.EventKindArticleCreated,
				OccurredAt: time.Unix(100, 0).UTC(),
				Source:     platform.EventSource{Platform: platform.PlatformTelegram, ID: "tg"},
				Conversation: platform.Conversation{
					ID:   "chat-1",
					Type: platform.ConversationTypeGroup,
				},
				Actor:   platform.Actor{ID: "u2", Username: "bob"},
				Article: &platform.Article{ID: "m3", ReplyToArticleID: "m2", Text: "Otogi hi"},
			},
			wantNoQuote:      []string{"<quoted_reply", "should not appear"},
			wantMessageCount: 4,
		},
		{
			name: "depth_1_limits_nesting",
			replyChain: []core.ReplyChainEntry{
				{
					Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
					Actor:        platform.Actor{ID: "u3", Username: "carol"},
					Article:      platform.Article{ID: "m4", ReplyToArticleID: "m3", Text: "trigger"},
					CreatedAt:    time.Unix(100, 0).UTC(),
					IsCurrent:    true,
				},
			},
			leadingContext: []core.ConversationContextEntry{
				{
					Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
					Actor:        platform.Actor{ID: "u2", Username: "bob"},
					Article:      platform.Article{ID: "m3", ReplyToArticleID: "m2", Text: "bob msg"},
					CreatedAt:    time.Unix(95, 0).UTC(),
				},
			},
			memories: map[string]core.Memory{
				"m2": {
					Actor:     platform.Actor{ID: "u1", Username: "alice"},
					Article:   platform.Article{ID: "m2", ReplyToArticleID: "m1", Text: "alice msg"},
					CreatedAt: time.Unix(85, 0).UTC(),
				},
				"m1": {
					Actor:     platform.Actor{ID: "u0", Username: "eve"},
					Article:   platform.Article{ID: "m1", Text: "root from eve depth limited"},
					CreatedAt: time.Unix(80, 0).UTC(),
				},
			},
			quoteReplyDepth: 1,
			event: &platform.Event{
				ID:         "evt-4",
				Kind:       platform.EventKindArticleCreated,
				OccurredAt: time.Unix(100, 0).UTC(),
				Source:     platform.EventSource{Platform: platform.PlatformTelegram, ID: "tg"},
				Conversation: platform.Conversation{
					ID:   "chat-1",
					Type: platform.ConversationTypeGroup,
				},
				Actor:   platform.Actor{ID: "u3", Username: "carol"},
				Article: &platform.Article{ID: "m4", ReplyToArticleID: "m3", Text: "Otogi hi"},
			},
			wantQuotedReply:  []string{"alice msg", `article_id="m2"`},
			wantNoQuote:      []string{"root from eve depth limited"},
			wantMessageCount: 4,
		},
		{
			name: "known_article_not_quoted",
			replyChain: []core.ReplyChainEntry{
				{
					Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
					Actor:        platform.Actor{ID: "u1", Username: "alice"},
					Article:      platform.Article{ID: "m1", Text: "thread root"},
					CreatedAt:    time.Unix(90, 0).UTC(),
				},
				{
					Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
					Actor:        platform.Actor{ID: "u2", Username: "bob"},
					Article:      platform.Article{ID: "m2", ReplyToArticleID: "m1", Text: "trigger"},
					CreatedAt:    time.Unix(100, 0).UTC(),
					IsCurrent:    true,
				},
			},
			memories: map[string]core.Memory{
				"m1": {
					Actor:     platform.Actor{ID: "u1", Username: "alice"},
					Article:   platform.Article{ID: "m1", Text: "should not be quoted"},
					CreatedAt: time.Unix(90, 0).UTC(),
				},
			},
			quoteReplyDepth: 2,
			event: &platform.Event{
				ID:         "evt-5",
				Kind:       platform.EventKindArticleCreated,
				OccurredAt: time.Unix(100, 0).UTC(),
				Source:     platform.EventSource{Platform: platform.PlatformTelegram, ID: "tg"},
				Conversation: platform.Conversation{
					ID:   "chat-1",
					Type: platform.ConversationTypeGroup,
				},
				Actor:   platform.Actor{ID: "u2", Username: "bob"},
				Article: &platform.Article{ID: "m2", ReplyToArticleID: "m1", Text: "Otogi hello"},
			},
			wantNoQuote:      []string{"<quoted_reply"},
			wantMessageCount: 4,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			module := newTestModule(Config{
				RequestTimeout: time.Second,
				Agents: []Agent{
					{
						Name:                 "Otogi",
						Description:          "assistant",
						Provider:             "p",
						Model:                "gpt-test",
						SystemPromptTemplate: "You are {{.AgentName}}",
						RequestTimeout:       time.Second,
						ContextPolicy: ContextPolicy{
							ReplyChainMaxMessages:  12,
							LeadingContextMessages: 4,
							LeadingContextMaxAge:   15 * time.Minute,
							MaxContextRunes:        12000,
							MaxMessageRunes:        1600,
							QuoteReplyDepth:        testCase.quoteReplyDepth,
						},
					},
				},
			})
			module.memory = &memoryStub{
				replyChain:     testCase.replyChain,
				leadingContext: testCase.leadingContext,
				memories:       testCase.memories,
			}
			module.clock = func() time.Time { return time.Unix(100, 0).UTC() }

			req, err := module.buildGenerateRequest(
				context.Background(),
				testCase.event,
				module.cfg.Agents[0],
				"how are you",
			)
			if err != nil {
				t.Fatalf("buildGenerateRequest failed: %v", err)
			}

			if testCase.wantMessageCount > 0 && len(req.Messages) != testCase.wantMessageCount {
				t.Fatalf("messages len = %d, want %d", len(req.Messages), testCase.wantMessageCount)
			}

			allContent := strings.Join(allMessageContents(req.Messages), "\n")
			for _, want := range testCase.wantQuotedReply {
				if !strings.Contains(allContent, want) {
					t.Fatalf("messages should contain %q, got:\n%s", want, allContent)
				}
			}
			for _, absent := range testCase.wantNoQuote {
				if strings.Contains(allContent, absent) {
					t.Fatalf("messages should NOT contain %q, got:\n%s", absent, allContent)
				}
			}
		})
	}
}

func TestBuildGenerateRequestBatchLoadsDanglingQuotedReplies(t *testing.T) {
	t.Parallel()

	module := newTestModule(Config{
		RequestTimeout: time.Second,
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Description:          "assistant",
				Provider:             "p",
				Model:                "gpt-test",
				SystemPromptTemplate: "You are {{.AgentName}}",
				RequestTimeout:       time.Second,
				ContextPolicy: ContextPolicy{
					ReplyChainMaxMessages:  12,
					LeadingContextMessages: 4,
					LeadingContextMaxAge:   15 * time.Minute,
					MaxContextRunes:        12000,
					MaxMessageRunes:        1600,
					QuoteReplyDepth:        2,
				},
			},
		},
	})

	var (
		batchCallCount int
		batchLookups   []core.MemoryLookup
		getLookups     []core.MemoryLookup
	)

	module.memory = &memoryStub{
		replyChain: []core.ReplyChainEntry{
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u1", Username: "alice"},
				Article:      platform.Article{ID: "m2", ReplyToArticleID: "m1", Text: "alice replies"},
				CreatedAt:    time.Unix(90, 0).UTC(),
			},
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u2", Username: "bob"},
				Article:      platform.Article{ID: "m4", ReplyToArticleID: "m2", Text: "trigger"},
				CreatedAt:    time.Unix(100, 0).UTC(),
				IsCurrent:    true,
			},
		},
		leadingContext: []core.ConversationContextEntry{
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "u3", Username: "carol"},
				Article:      platform.Article{ID: "m5", ReplyToArticleID: "m6", Text: "carol replies"},
				CreatedAt:    time.Unix(95, 0).UTC(),
			},
		},
		memories: map[string]core.Memory{
			"m1": {
				Actor:     platform.Actor{ID: "u0", Username: "eve"},
				Article:   platform.Article{ID: "m1", Text: "root one"},
				CreatedAt: time.Unix(80, 0).UTC(),
			},
			"m6": {
				Actor:     platform.Actor{ID: "u4", Username: "dan"},
				Article:   platform.Article{ID: "m6", Text: "root two"},
				CreatedAt: time.Unix(85, 0).UTC(),
			},
		},
		onGetBatch: func(lookups []core.MemoryLookup) {
			batchCallCount++
			batchLookups = append(batchLookups, lookups...)
		},
		onGet: func(lookup core.MemoryLookup) {
			getLookups = append(getLookups, lookup)
		},
	}
	module.clock = func() time.Time { return time.Unix(100, 0).UTC() }

	req, err := module.buildGenerateRequest(
		context.Background(),
		&platform.Event{
			ID:         "evt-6",
			Kind:       platform.EventKindArticleCreated,
			OccurredAt: time.Unix(100, 0).UTC(),
			Source:     platform.EventSource{Platform: platform.PlatformTelegram, ID: "tg"},
			Conversation: platform.Conversation{
				ID:   "chat-1",
				Type: platform.ConversationTypeGroup,
			},
			Actor:   platform.Actor{ID: "u2", Username: "bob"},
			Article: &platform.Article{ID: "m4", ReplyToArticleID: "m2", Text: "Otogi hello"},
		},
		module.cfg.Agents[0],
		"how are you",
	)
	if err != nil {
		t.Fatalf("buildGenerateRequest failed: %v", err)
	}

	if batchCallCount != 1 {
		t.Fatalf("GetBatch call count = %d, want 1", batchCallCount)
	}
	if len(batchLookups) != 2 {
		t.Fatalf("GetBatch lookup len = %d, want 2", len(batchLookups))
	}
	wantLookups := map[string]struct{}{
		"m1": {},
		"m6": {},
	}
	for _, lookup := range batchLookups {
		if _, ok := wantLookups[lookup.ArticleID]; !ok {
			t.Fatalf("unexpected batch lookup article id %q", lookup.ArticleID)
		}
		delete(wantLookups, lookup.ArticleID)
	}
	if len(wantLookups) != 0 {
		t.Fatalf("missing batch lookup article ids: %v", wantLookups)
	}
	if len(getLookups) != 0 {
		t.Fatalf("Get call count = %d, want 0", len(getLookups))
	}

	allContent := strings.Join(allMessageContents(req.Messages), "\n")
	if !strings.Contains(allContent, "root one") {
		t.Fatalf("messages should contain quoted batch reply root one, got:\n%s", allContent)
	}
	if !strings.Contains(allContent, "root two") {
		t.Fatalf("messages should contain quoted batch reply root two, got:\n%s", allContent)
	}
}

func TestSerializeQuotedReplyRendersNestedChain(t *testing.T) {
	t.Parallel()

	quote := quotedReply{
		Actor:     platform.Actor{ID: "u1", Username: "alice"},
		Article:   platform.Article{ID: "m2", ReplyToArticleID: "m1", Text: "alice reply"},
		CreatedAt: time.Unix(90, 0).UTC(),
		Nested: &quotedReply{
			Actor:     platform.Actor{ID: "u0", Username: "eve"},
			Article:   platform.Article{ID: "m1", Text: "eve root"},
			CreatedAt: time.Unix(80, 0).UTC(),
		},
	}

	result := serializeQuotedReply(quote, 1600)
	if !strings.Contains(result, "<quoted_reply") {
		t.Fatalf("result = %q, want <quoted_reply envelope", result)
	}
	if !strings.Contains(result, `article_id="m2"`) {
		t.Fatalf("result = %q, want outer article_id", result)
	}
	if !strings.Contains(result, `article_id="m1"`) {
		t.Fatalf("result = %q, want nested article_id", result)
	}
	if !strings.Contains(result, "alice reply") {
		t.Fatalf("result = %q, want outer content", result)
	}
	if !strings.Contains(result, "eve root") {
		t.Fatalf("result = %q, want nested content", result)
	}

	outerStart := strings.Index(result, `article_id="m2"`)
	innerStart := strings.Index(result, `article_id="m1"`)
	if outerStart > innerStart {
		t.Fatalf("outer quote should come before nested quote in result: %q", result)
	}
}

func TestSerializeQuotedReplyEmptyTextReturnsEmpty(t *testing.T) {
	t.Parallel()

	quote := quotedReply{
		Actor:   platform.Actor{ID: "u1", Username: "alice"},
		Article: platform.Article{ID: "m1", Text: ""},
	}

	result := serializeQuotedReply(quote, 1600)
	if result != "" {
		t.Fatalf("result = %q, want empty for empty text", result)
	}
}

func TestCollectKnownArticleIDs(t *testing.T) {
	t.Parallel()

	event := &platform.Event{
		Article: &platform.Article{ID: "current"},
	}
	replyChain := []core.ReplyChainEntry{
		{Article: platform.Article{ID: "r1"}},
		{Article: platform.Article{ID: "r2"}},
	}
	leading := []core.ConversationContextEntry{
		{Article: platform.Article{ID: "l1"}},
	}

	known := collectKnownArticleIDs(event, replyChain, leading)
	for _, id := range []string{"current", "r1", "r2", "l1"} {
		if _, found := known[id]; !found {
			t.Fatalf("known set missing %q", id)
		}
	}
	if len(known) != 4 {
		t.Fatalf("known set size = %d, want 4", len(known))
	}
}

func TestRenderSystemPromptTemplateVariablesDoNotOverrideBuiltIns(t *testing.T) {
	t.Parallel()

	agent := Agent{
		Name:                 "Otogi",
		Description:          "assistant",
		SystemPromptTemplate: "built_in={{.AgentName}} user={{index .TemplateVariables \"AgentName\"}}",
		TemplateVariables: map[string]string{
			"AgentName": "custom-name",
		},
	}
	event := &platform.Event{
		Kind:       platform.EventKindArticleCreated,
		OccurredAt: time.Unix(100, 0).UTC(),
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{
			ID:   "chat-1",
			Type: platform.ConversationTypeGroup,
		},
		Actor:   platform.Actor{ID: "u2", DisplayName: "Bob"},
		Article: &platform.Article{ID: "m2", Text: "Otogi hi"},
	}

	rendered, err := renderSystemPrompt(agent, event, time.Unix(100, 0).UTC())
	if err != nil {
		t.Fatalf("renderSystemPrompt failed: %v", err)
	}
	if rendered != "built_in=Otogi user=custom-name" {
		t.Fatalf("rendered prompt = %q, want built-in precedence", rendered)
	}
}

func TestRenderSystemPromptIncludesAgentAliasFields(t *testing.T) {
	t.Parallel()

	agent := Agent{
		Name:                 "Otogi",
		Aliases:              []string{"Oto", "Otogi Sensei"},
		Description:          "assistant",
		SystemPromptTemplate: `name={{.AgentName}} aliases={{printf "%v" .AgentAliases}} triggers={{printf "%v" .AgentTriggerNames}}`,
	}
	event := &platform.Event{
		Kind:       platform.EventKindArticleCreated,
		OccurredAt: time.Unix(100, 0).UTC(),
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{
			ID:   "chat-1",
			Type: platform.ConversationTypeGroup,
		},
		Actor:   platform.Actor{ID: "u2", DisplayName: "Bob"},
		Article: &platform.Article{ID: "m2", Text: "Otogi hi"},
	}

	rendered, err := renderSystemPrompt(agent, event, time.Unix(100, 0).UTC())
	if err != nil {
		t.Fatalf("renderSystemPrompt failed: %v", err)
	}
	want := "name=Otogi aliases=[Oto Otogi Sensei] triggers=[Otogi Oto Otogi Sensei]"
	if rendered != want {
		t.Fatalf("rendered prompt = %q, want %q", rendered, want)
	}
}

func TestOnRegisterFailsWithoutConfig(t *testing.T) {
	t.Parallel()

	module := New()
	runtime := moduleRuntimeStub{
		registry: serviceRegistryStub{values: map[string]any{}},
	}

	err := module.OnRegister(context.Background(), runtime)
	if err == nil {
		t.Fatal("OnRegister error = nil, want config load failure")
	}
	if !strings.Contains(err.Error(), "llmchat load config") {
		t.Fatalf("OnRegister error = %q, want llmchat load config context", err.Error())
	}
}

func TestEditPacerBaseIntervals(t *testing.T) {
	t.Parallel()

	start := time.Unix(0, 0).UTC()
	pacer := newEditPacer(start)

	cases := []struct {
		at   time.Duration
		want time.Duration
	}{
		{at: 500 * time.Millisecond, want: 2 * time.Second},
		{at: 8 * time.Second, want: 3500 * time.Millisecond},
		{at: 30 * time.Second, want: 6 * time.Second},
		{at: 80 * time.Second, want: 10 * time.Second},
	}
	for _, testCase := range cases {
		got := pacer.baseInterval(start.Add(testCase.at))
		if got != testCase.want {
			t.Fatalf("base interval at %s = %s, want %s", testCase.at, got, testCase.want)
		}
	}
}

func TestStreamProviderReplyUsesMarkdownParserPayload(t *testing.T) {
	module := newTestModule(validModuleConfig())

	module.dispatcher = &sinkDispatcherStub{}
	module.parser = markdownParserResultStub{
		result: platform.ParsedText{
			Text: "converted",
			Entities: []platform.TextEntity{
				{Type: platform.TextEntityTypeBold, Offset: 0, Length: 9},
			},
		},
	}
	module.clock = sequenceClock([]time.Time{
		time.Unix(0, 0).UTC(),
		time.Unix(0, 0).UTC(),
	})

	provider := &providerStub{stream: &streamStub{chunks: []ai.LLMGenerateChunk{
		{Delta: "hello"},
	}}}
	req := ai.LLMGenerateRequest{
		Model: "gpt-test",
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleSystem, Content: "sys"},
			{Role: ai.LLMMessageRoleUser, Content: "u"},
		},
	}
	if err := module.streamProviderReply(
		context.Background(),
		context.Background(),
		platform.OutboundTarget{Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup}},
		"placeholder-1",
		provider,
		req,
	); err != nil {
		t.Fatalf("streamProviderReply failed: %v", err)
	}

	sink, ok := module.dispatcher.(*sinkDispatcherStub)
	if !ok {
		t.Fatalf("dispatcher type = %T, want *sinkDispatcherStub", module.dispatcher)
	}
	if len(sink.editRequests) != 1 {
		t.Fatalf("edit request count = %d, want 1", len(sink.editRequests))
	}
	if sink.editRequests[0].Text != "converted" {
		t.Fatalf("edit text = %q, want converted", sink.editRequests[0].Text)
	}
	if len(sink.editRequests[0].Entities) != 1 {
		t.Fatalf("edit entities len = %d, want 1", len(sink.editRequests[0].Entities))
	}
	if sink.editRequests[0].Entities[0].Type != platform.TextEntityTypeBold {
		t.Fatalf("entity type = %q, want %q", sink.editRequests[0].Entities[0].Type, platform.TextEntityTypeBold)
	}
}

func TestStreamProviderReplyMarkdownParseErrorFallsBackToPlainText(t *testing.T) {
	module := newTestModule(validModuleConfig())

	module.dispatcher = &sinkDispatcherStub{}
	module.parser = markdownParserResultStub{
		err: errors.New("parse failed"),
	}
	module.clock = sequenceClock([]time.Time{
		time.Unix(0, 0).UTC(),
		time.Unix(0, 0).UTC(),
	})

	provider := &providerStub{stream: &streamStub{chunks: []ai.LLMGenerateChunk{
		{Delta: "hello"},
	}}}
	req := ai.LLMGenerateRequest{
		Model: "gpt-test",
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleSystem, Content: "sys"},
			{Role: ai.LLMMessageRoleUser, Content: "u"},
		},
	}
	if err := module.streamProviderReply(
		context.Background(),
		context.Background(),
		platform.OutboundTarget{Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup}},
		"placeholder-1",
		provider,
		req,
	); err != nil {
		t.Fatalf("streamProviderReply failed: %v", err)
	}

	sink, ok := module.dispatcher.(*sinkDispatcherStub)
	if !ok {
		t.Fatalf("dispatcher type = %T, want *sinkDispatcherStub", module.dispatcher)
	}
	if len(sink.editRequests) != 1 {
		t.Fatalf("edit request count = %d, want 1", len(sink.editRequests))
	}
	if sink.editRequests[0].Text != "hello" {
		t.Fatalf("edit text = %q, want hello", sink.editRequests[0].Text)
	}
	if len(sink.editRequests[0].Entities) != 0 {
		t.Fatalf("edit entities len = %d, want 0", len(sink.editRequests[0].Entities))
	}
}

func TestStreamProviderReplyDedupeConsidersEntities(t *testing.T) {
	module := newTestModule(validModuleConfig())

	module.dispatcher = &sinkDispatcherStub{}
	module.parser = &markdownParserSequenceStub{
		results: []platform.ParsedText{
			{
				Text: "normalized",
				Entities: []platform.TextEntity{
					{Type: platform.TextEntityTypeBold, Offset: 0, Length: 10},
				},
			},
			{
				Text: "normalized",
				Entities: []platform.TextEntity{
					{Type: platform.TextEntityTypeItalic, Offset: 0, Length: 10},
				},
			},
		},
	}
	module.clock = sequenceClock([]time.Time{
		time.Unix(0, 0).UTC(),
		time.Unix(3, 0).UTC(),
	})

	provider := &providerStub{stream: &streamStub{chunks: []ai.LLMGenerateChunk{
		{Delta: "a"},
		{Delta: "b"},
	}}}
	req := ai.LLMGenerateRequest{
		Model: "gpt-test",
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleSystem, Content: "sys"},
			{Role: ai.LLMMessageRoleUser, Content: "u"},
		},
	}
	if err := module.streamProviderReply(
		context.Background(),
		context.Background(),
		platform.OutboundTarget{Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup}},
		"placeholder-1",
		provider,
		req,
	); err != nil {
		t.Fatalf("streamProviderReply failed: %v", err)
	}

	sink, ok := module.dispatcher.(*sinkDispatcherStub)
	if !ok {
		t.Fatalf("dispatcher type = %T, want *sinkDispatcherStub", module.dispatcher)
	}
	if len(sink.editRequests) != 2 {
		t.Fatalf("edit request count = %d, want 2", len(sink.editRequests))
	}
	if sink.editRequests[0].Text != "normalized" || sink.editRequests[1].Text != "normalized" {
		t.Fatalf("edit texts = [%q, %q], want both normalized", sink.editRequests[0].Text, sink.editRequests[1].Text)
	}
	if sink.editRequests[0].Entities[0].Type != platform.TextEntityTypeBold {
		t.Fatalf("first entity type = %q, want %q", sink.editRequests[0].Entities[0].Type, platform.TextEntityTypeBold)
	}
	if sink.editRequests[1].Entities[0].Type != platform.TextEntityTypeItalic {
		t.Fatalf("second entity type = %q, want %q", sink.editRequests[1].Entities[0].Type, platform.TextEntityTypeItalic)
	}
}

func TestStreamProviderReplySkipsMarkdownParsingWhenPacerDefersEdit(t *testing.T) {
	module := newTestModule(validModuleConfig())

	module.dispatcher = &sinkDispatcherStub{}
	parser := &markdownParserSequenceStub{
		results: []platform.ParsedText{
			{Text: "a"},
			{Text: "ab"},
		},
	}
	module.parser = parser
	module.clock = sequenceClock([]time.Time{
		time.Unix(0, 0).UTC(),
		time.Unix(0, 0).UTC(),
		time.Unix(1, 0).UTC(),
	})

	provider := &providerStub{stream: &streamStub{chunks: []ai.LLMGenerateChunk{
		{Delta: "a"},
		{Delta: "b"},
	}}}
	req := ai.LLMGenerateRequest{
		Model: "gpt-test",
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleSystem, Content: "sys"},
			{Role: ai.LLMMessageRoleUser, Content: "u"},
		},
	}
	if err := module.streamProviderReply(
		context.Background(),
		context.Background(),
		platform.OutboundTarget{Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup}},
		"placeholder-1",
		provider,
		req,
	); err != nil {
		t.Fatalf("streamProviderReply failed: %v", err)
	}

	if parser.calls != 2 {
		t.Fatalf("parser calls = %d, want 2 (first paced edit + final edit)", parser.calls)
	}
	sink, ok := module.dispatcher.(*sinkDispatcherStub)
	if !ok {
		t.Fatalf("dispatcher type = %T, want *sinkDispatcherStub", module.dispatcher)
	}
	if len(sink.editRequests) != 2 {
		t.Fatalf("edit request count = %d, want 2", len(sink.editRequests))
	}
	if sink.editRequests[0].Text != "a" {
		t.Fatalf("first edit text = %q, want a", sink.editRequests[0].Text)
	}
	if sink.editRequests[1].Text != "ab" {
		t.Fatalf("final edit text = %q, want ab", sink.editRequests[1].Text)
	}
}

func TestEditPacerBackoffAndRateLimit(t *testing.T) {
	t.Parallel()

	start := time.Unix(0, 0).UTC()
	pacer := newEditPacer(start)

	pacer.RecordEditFailure(start.Add(time.Second), errors.New("temporary failure"))
	first := pacer.currentInterval
	if first <= 2*time.Second {
		t.Fatalf("first interval = %s, want > 2s", first)
	}

	pacer.RecordEditFailure(start.Add(2*time.Second), errors.New("temporary failure"))
	second := pacer.currentInterval
	if second <= first {
		t.Fatalf("second interval = %s, want > first interval %s", second, first)
	}

	pacer.RecordEditFailure(start.Add(3*time.Second), errors.New("429 too many requests"))
	if pacer.currentInterval != maxEditInterval {
		t.Fatalf("rate-limit interval = %s, want %s", pacer.currentInterval, maxEditInterval)
	}
}

func TestStreamProviderReplyIntermediateEditsContinueBeyondThreeFailures(t *testing.T) {
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

	sink := &sinkDispatcherStub{
		editErrors: []error{
			errors.New("edit fail 1"),
			errors.New("edit fail 2"),
			errors.New("edit fail 3"),
			errors.New("edit fail 4"),
			nil,
			nil,
		},
	}
	module.dispatcher = sink
	clock := sequenceClock([]time.Time{
		time.Unix(0, 0).UTC(),
		time.Unix(0, 0).UTC(),
		time.Unix(4, 0).UTC(),
		time.Unix(12, 0).UTC(),
		time.Unix(22, 0).UTC(),
		time.Unix(35, 0).UTC(),
		time.Unix(50, 0).UTC(),
	})
	module.clock = clock

	provider := &providerStub{stream: &streamStub{chunks: []ai.LLMGenerateChunk{
		{Delta: "a"},
		{Delta: "b"},
		{Delta: "c"},
		{Delta: "d"},
		{Delta: "e"},
		{Delta: "f"},
	}}}
	req := ai.LLMGenerateRequest{
		Model: "gpt-test",
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleSystem, Content: "sys"},
			{Role: ai.LLMMessageRoleUser, Content: "u"},
		},
	}
	if err := module.streamProviderReply(
		context.Background(),
		context.Background(),
		platform.OutboundTarget{Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup}},
		"placeholder-1",
		provider,
		req,
	); err != nil {
		t.Fatalf("streamProviderReply failed: %v", err)
	}

	if len(sink.editRequests) != 6 {
		t.Fatalf("edit request count = %d, want 6", len(sink.editRequests))
	}
	if sink.editRequests[4].Text != "abcde" {
		t.Fatalf("edit request[4].text = %q, want abcde", sink.editRequests[4].Text)
	}
	if sink.editRequests[len(sink.editRequests)-1].Text != "abcdef" {
		t.Fatalf("final edit text = %q, want abcdef", sink.editRequests[len(sink.editRequests)-1].Text)
	}
}

func TestStreamProviderReplyFinalEditRetriesUntilSuccess(t *testing.T) {
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

	sink := &sinkDispatcherStub{
		editErrors: []error{
			nil,
			errors.New("rpc error: FLOOD_WAIT_25"),
			errors.New("retry after 2"),
			nil,
		},
	}
	module.dispatcher = sink
	module.clock = sequenceClock([]time.Time{
		time.Unix(0, 0).UTC(),
		time.Unix(0, 0).UTC(),
		time.Unix(0, 0).UTC(),
	})
	sleeps := make([]time.Duration, 0, 2)
	module.sleep = func(_ context.Context, delay time.Duration) error {
		sleeps = append(sleeps, delay)
		return nil
	}

	provider := &providerStub{stream: &streamStub{chunks: []ai.LLMGenerateChunk{
		{Delta: "hello"},
		{Delta: " world"},
	}}}
	req := ai.LLMGenerateRequest{
		Model: "gpt-test",
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleSystem, Content: "sys"},
			{Role: ai.LLMMessageRoleUser, Content: "u"},
		},
	}
	if err := module.streamProviderReply(
		context.Background(),
		context.Background(),
		platform.OutboundTarget{Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup}},
		"placeholder-1",
		provider,
		req,
	); err != nil {
		t.Fatalf("streamProviderReply failed: %v", err)
	}

	if len(sink.sendRequests) != 0 {
		t.Fatalf("send request count = %d, want 0", len(sink.sendRequests))
	}
	if len(sink.editRequests) != 4 {
		t.Fatalf("edit request count = %d, want 4", len(sink.editRequests))
	}
	if got := sink.editRequests[len(sink.editRequests)-1].Text; got != "hello world" {
		t.Fatalf("final edit text = %q, want hello world", got)
	}
	if len(sleeps) != 2 {
		t.Fatalf("sleep count = %d, want 2", len(sleeps))
	}
	if sleeps[0] != maxEditInterval {
		t.Fatalf("first retry sleep = %s, want %s", sleeps[0], maxEditInterval)
	}
	if sleeps[1] != 2*time.Second {
		t.Fatalf("second retry sleep = %s, want %s", sleeps[1], 2*time.Second)
	}
}

func TestStreamProviderReplyFinalEditHonorsTypedRetryAfter(t *testing.T) {
	module := newTestModule(validModuleConfig())

	sink := &sinkDispatcherStub{
		editErrors: []error{
			nil,
			&platform.OutboundError{
				Operation:  platform.OutboundOperationEditMessage,
				Kind:       platform.OutboundErrorKindRateLimited,
				Platform:   platform.PlatformTelegram,
				SinkID:     "tg-main",
				RetryAfter: 4 * time.Second,
				Cause:      errors.New("flood wait"),
			},
			nil,
		},
	}
	module.dispatcher = sink
	module.clock = sequenceClock([]time.Time{
		time.Unix(0, 0).UTC(),
		time.Unix(0, 0).UTC(),
		time.Unix(0, 0).UTC(),
	})
	sleeps := make([]time.Duration, 0, 1)
	module.sleep = func(_ context.Context, delay time.Duration) error {
		sleeps = append(sleeps, delay)
		return nil
	}

	provider := &providerStub{stream: &streamStub{chunks: []ai.LLMGenerateChunk{
		{Delta: "hello"},
		{Delta: " world"},
	}}}
	req := ai.LLMGenerateRequest{
		Model: "gpt-test",
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleSystem, Content: "sys"},
			{Role: ai.LLMMessageRoleUser, Content: "u"},
		},
	}
	if err := module.streamProviderReply(
		context.Background(),
		context.Background(),
		platform.OutboundTarget{
			Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
		},
		"placeholder-1",
		provider,
		req,
	); err != nil {
		t.Fatalf("streamProviderReply failed: %v", err)
	}

	if len(sink.sendRequests) != 0 {
		t.Fatalf("send request count = %d, want 0", len(sink.sendRequests))
	}
	if len(sink.editRequests) != 3 {
		t.Fatalf("edit request count = %d, want 3", len(sink.editRequests))
	}
	if got := sink.editRequests[len(sink.editRequests)-1].Text; got != "hello world" {
		t.Fatalf("final edit text = %q, want hello world", got)
	}
	if len(sleeps) != 1 {
		t.Fatalf("sleep count = %d, want 1", len(sleeps))
	}
	if sleeps[0] != 4*time.Second {
		t.Fatalf("retry sleep = %s, want %s", sleeps[0], 4*time.Second)
	}
}

func TestStreamProviderReplyFinalEditExhaustsAtHandlerDeadline(t *testing.T) {
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

	sink := &sinkDispatcherStub{
		editErrors: []error{
			nil,
			errors.New("429 too many requests"),
		},
	}
	module.dispatcher = sink
	var logs bytes.Buffer
	module.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	module.clock = sequenceClock([]time.Time{
		time.Unix(0, 0).UTC(),
		time.Unix(0, 0).UTC(),
		time.Unix(0, 0).UTC(),
	})
	deliveryCtx, cancelDelivery := context.WithCancel(context.Background())
	module.sleep = func(_ context.Context, _ time.Duration) error {
		cancelDelivery()
		return deliveryCtx.Err()
	}

	provider := &providerStub{stream: &streamStub{chunks: []ai.LLMGenerateChunk{
		{Delta: "hello"},
		{Delta: " world"},
	}}}
	req := ai.LLMGenerateRequest{
		Model: "gpt-test",
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleSystem, Content: "sys"},
			{Role: ai.LLMMessageRoleUser, Content: "u"},
		},
	}
	err := module.streamProviderReply(
		context.Background(),
		deliveryCtx,
		platform.OutboundTarget{Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup}},
		"placeholder-1",
		provider,
		req,
	)
	if err == nil {
		t.Fatal("streamProviderReply error = nil, want exhausted retry error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
	if !strings.Contains(err.Error(), "exhausted before handler timeout") {
		t.Fatalf("error = %q, want exhausted before handler timeout", err)
	}
	if len(sink.sendRequests) != 0 {
		t.Fatalf("send request count = %d, want 0", len(sink.sendRequests))
	}
	logOutput := logs.String()
	if !strings.Contains(logOutput, "llmchat edit retry exhausted") {
		t.Fatalf("logs = %q, want exhaustion warning", logOutput)
	}
	if !strings.Contains(logOutput, "conversation_id=chat-1") {
		t.Fatalf("logs = %q, want conversation_id", logOutput)
	}
	if !strings.Contains(logOutput, "placeholder_message_id=placeholder-1") {
		t.Fatalf("logs = %q, want placeholder_message_id", logOutput)
	}
	if !strings.Contains(logOutput, "operation=edit_message") {
		t.Fatalf("logs = %q, want operation", logOutput)
	}
	if !strings.Contains(logOutput, "kind=rate_limited") {
		t.Fatalf("logs = %q, want kind", logOutput)
	}
	if !strings.Contains(logOutput, "attempts=1") {
		t.Fatalf("logs = %q, want attempts", logOutput)
	}
	if !strings.Contains(logOutput, "deadline_remaining=none") {
		t.Fatalf("logs = %q, want deadline_remaining", logOutput)
	}
}

func TestStreamProviderReplyWarnsOncePerFailureStreak(t *testing.T) {
	module := newTestModule(validModuleConfig())

	sink := &sinkDispatcherStub{
		editErrors: []error{
			errors.New("temporary edit failure"),
			errors.New("temporary edit failure"),
			nil,
			nil,
		},
	}
	module.dispatcher = sink
	var logs bytes.Buffer
	module.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	module.clock = sequenceClock([]time.Time{
		time.Unix(0, 0).UTC(),
		time.Unix(0, 0).UTC(),
		time.Unix(4, 0).UTC(),
		time.Unix(12, 0).UTC(),
		time.Unix(22, 0).UTC(),
	})

	provider := &providerStub{stream: &streamStub{chunks: []ai.LLMGenerateChunk{
		{Delta: "a"},
		{Delta: "b"},
		{Delta: "c"},
		{Delta: "d"},
	}}}
	req := ai.LLMGenerateRequest{
		Model: "gpt-test",
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleSystem, Content: "sys"},
			{Role: ai.LLMMessageRoleUser, Content: "u"},
		},
	}
	if err := module.streamProviderReply(
		context.Background(),
		context.Background(),
		platform.OutboundTarget{Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup}},
		"placeholder-1",
		provider,
		req,
	); err != nil {
		t.Fatalf("streamProviderReply failed: %v", err)
	}

	logOutput := logs.String()
	if got := strings.Count(logOutput, "llmchat intermediate edit failed"); got != 1 {
		t.Fatalf("warn count = %d, want 1 (logs=%q)", got, logOutput)
	}
	if !strings.Contains(logOutput, "conversation_id=chat-1") {
		t.Fatalf("logs = %q, want conversation_id", logOutput)
	}
	if !strings.Contains(logOutput, "placeholder_message_id=placeholder-1") {
		t.Fatalf("logs = %q, want placeholder_message_id", logOutput)
	}
	if !strings.Contains(logOutput, "operation=edit_message") {
		t.Fatalf("logs = %q, want operation", logOutput)
	}
	if !strings.Contains(logOutput, "kind=unknown") {
		t.Fatalf("logs = %q, want unknown kind for untyped error", logOutput)
	}
	if !strings.Contains(logOutput, "attempts=1") {
		t.Fatalf("logs = %q, want attempts", logOutput)
	}
	if !strings.Contains(logOutput, "deadline_remaining=none") {
		t.Fatalf("logs = %q, want deadline remaining", logOutput)
	}
}

func TestStreamProviderReplySkipsFinalEditWhenFinalTextAlreadyDelivered(t *testing.T) {
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

	sink := &sinkDispatcherStub{
		editErrors: []error{nil, errors.New("unexpected final edit")},
	}
	module.dispatcher = sink
	module.clock = sequenceClock([]time.Time{
		time.Unix(0, 0).UTC(),
		time.Unix(0, 0).UTC(),
	})

	provider := &providerStub{stream: &streamStub{chunks: []ai.LLMGenerateChunk{{Delta: "hello"}}}}
	req := ai.LLMGenerateRequest{
		Model: "gpt-test",
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleSystem, Content: "sys"},
			{Role: ai.LLMMessageRoleUser, Content: "u"},
		},
	}
	if err := module.streamProviderReply(
		context.Background(),
		context.Background(),
		platform.OutboundTarget{Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup}},
		"placeholder-1",
		provider,
		req,
	); err != nil {
		t.Fatalf("streamProviderReply failed: %v", err)
	}

	if len(sink.editRequests) != 1 {
		t.Fatalf("edit request count = %d, want 1", len(sink.editRequests))
	}
	if sink.editRequests[0].Text != "hello" {
		t.Fatalf("edit text = %q, want hello", sink.editRequests[0].Text)
	}
	if len(sink.sendRequests) != 0 {
		t.Fatalf("send request count = %d, want 0", len(sink.sendRequests))
	}
}

func TestStreamProviderReplyShowsThinkingThenFinalAnswer(t *testing.T) {
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
	module.clock = sequenceClock([]time.Time{
		time.Unix(0, 0).UTC(),
		time.Unix(0, 0).UTC(),
		time.Unix(3, 0).UTC(),
	})

	provider := &providerStub{stream: &streamStub{chunks: []ai.LLMGenerateChunk{
		{Kind: ai.LLMGenerateChunkKindThinkingSummary, Delta: "  Plan\nsteps\t"},
		{Kind: ai.LLMGenerateChunkKindOutputText, Delta: "final answer"},
	}}}
	req := ai.LLMGenerateRequest{
		Model: "gpt-test",
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleSystem, Content: "sys"},
			{Role: ai.LLMMessageRoleUser, Content: "u"},
		},
	}
	if err := module.streamProviderReply(
		context.Background(),
		context.Background(),
		platform.OutboundTarget{Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup}},
		"placeholder-1",
		provider,
		req,
	); err != nil {
		t.Fatalf("streamProviderReply failed: %v", err)
	}

	if len(sink.sendRequests) != 0 {
		t.Fatalf("send request count = %d, want 0", len(sink.sendRequests))
	}
	if len(sink.editRequests) < 2 {
		t.Fatalf("edit request count = %d, want at least 2", len(sink.editRequests))
	}
	if sink.editRequests[0].Text != "Thinking...\n\nPlan steps" {
		t.Fatalf("first edit text = %q, want thinking preview", sink.editRequests[0].Text)
	}

	finalText := sink.editRequests[len(sink.editRequests)-1].Text
	if finalText != "final answer" {
		t.Fatalf("final edit text = %q, want final answer", finalText)
	}
	if strings.Contains(strings.ToLower(finalText), "thinking") {
		t.Fatalf("final edit should not contain thinking text, got %q", finalText)
	}
}

func TestStreamProviderReplyThinkingOnlyReturnsError(t *testing.T) {
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
	module.clock = sequenceClock([]time.Time{
		time.Unix(0, 0).UTC(),
		time.Unix(0, 0).UTC(),
	})

	longSummary := strings.Repeat("summary ", 80)
	provider := &providerStub{stream: &streamStub{chunks: []ai.LLMGenerateChunk{
		{Kind: ai.LLMGenerateChunkKindThinkingSummary, Delta: longSummary},
	}}}
	req := ai.LLMGenerateRequest{
		Model: "gpt-test",
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleSystem, Content: "sys"},
			{Role: ai.LLMMessageRoleUser, Content: "u"},
		},
	}
	err := module.streamProviderReply(
		context.Background(),
		context.Background(),
		platform.OutboundTarget{Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup}},
		"placeholder-1",
		provider,
		req,
	)
	if err == nil {
		t.Fatal("streamProviderReply error = nil, want thinking-only error")
	}
	if !strings.Contains(err.Error(), "no output text received") {
		t.Fatalf("error = %q, want no output text received", err)
	}
	if !strings.Contains(err.Error(), "thinking_chunks=1") {
		t.Fatalf("error = %q, want thinking chunk diagnostics", err)
	}
	if !strings.Contains(err.Error(), "output_chunks=0") {
		t.Fatalf("error = %q, want output chunk diagnostics", err)
	}
	if !strings.Contains(err.Error(), "deadline_remaining=none") {
		t.Fatalf("error = %q, want deadline remaining diagnostics", err)
	}

	if len(sink.editRequests) == 0 {
		t.Fatal("expected edit requests")
	}
	if len(sink.sendRequests) != 0 {
		t.Fatalf("send request count = %d, want 0", len(sink.sendRequests))
	}

	thinkingText := sink.editRequests[len(sink.editRequests)-1].Text
	prefix := defaultThinkingPlaceholder + "\n\n"
	if !strings.HasPrefix(thinkingText, prefix) {
		t.Fatalf("thinking text prefix = %q, want %q", thinkingText, prefix)
	}
	preview := strings.TrimPrefix(thinkingText, prefix)
	if len([]rune(preview)) != maxThinkingPreviewRunes {
		t.Fatalf("preview rune length = %d, want %d", len([]rune(preview)), maxThinkingPreviewRunes)
	}
	if !strings.HasSuffix(preview, "...") {
		t.Fatalf("preview should be truncated with ellipsis, got %q", preview)
	}
}

func TestStreamProviderReplyNoOutputReturnsContextErrorWhenCanceled(t *testing.T) {
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

	req := ai.LLMGenerateRequest{
		Model: "gpt-test",
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleSystem, Content: "sys"},
			{Role: ai.LLMMessageRoleUser, Content: "u"},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := module.streamProviderReply(
		ctx,
		ctx,
		platform.OutboundTarget{Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup}},
		"placeholder-1",
		&providerStub{stream: &streamStub{}},
		req,
	)
	if err == nil {
		t.Fatal("streamProviderReply error = nil, want context cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
	if strings.Contains(err.Error(), "no output text received") {
		t.Fatalf("error = %q, should not map canceled stream to no output text", err)
	}
}

func TestShapeThinkingSummaryNormalizesWhitespace(t *testing.T) {
	t.Parallel()

	shaped := shapeThinkingSummary("  a\tb\nc  ")
	if shaped != "a b c" {
		t.Fatalf("shapeThinkingSummary = %q, want %q", shaped, "a b c")
	}
}

func TestStreamProviderReplyRequiresPlaceholderMessageID(t *testing.T) {
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

	req := ai.LLMGenerateRequest{
		Model: "gpt-test",
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleSystem, Content: "sys"},
			{Role: ai.LLMMessageRoleUser, Content: "u"},
		},
	}
	err := module.streamProviderReply(
		context.Background(),
		context.Background(),
		platform.OutboundTarget{Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup}},
		"",
		&providerStub{stream: &streamStub{chunks: []ai.LLMGenerateChunk{{Delta: "hello"}}}},
		req,
	)
	if err == nil {
		t.Fatal("streamProviderReply error = nil, want placeholder validation error")
	}
	if !strings.Contains(err.Error(), "empty placeholder message id") {
		t.Fatalf("error = %q, want empty placeholder message id", err)
	}
}

func TestFinalizePlaceholderFailureRetriesUntilSuccess(t *testing.T) {
	module := newTestModule(validModuleConfig())

	sink := &sinkDispatcherStub{
		editErrors: []error{
			errors.New("retry after 3"),
			nil,
		},
	}
	module.dispatcher = sink
	sleeps := make([]time.Duration, 0, 1)
	module.sleep = func(_ context.Context, delay time.Duration) error {
		sleeps = append(sleeps, delay)
		return nil
	}

	handlerErr := errors.New("handler failed")
	target := platform.OutboundTarget{
		Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
	}
	err := module.finalizePlaceholderFailure(context.Background(), target, "placeholder-1", handlerErr)
	if !errors.Is(err, handlerErr) {
		t.Fatalf("error = %v, want handler error", err)
	}
	if len(sink.editRequests) != 2 {
		t.Fatalf("edit request count = %d, want 2", len(sink.editRequests))
	}
	if sink.editRequests[1].Text != placeholderFailureMessage {
		t.Fatalf("retry edit text = %q, want %q", sink.editRequests[1].Text, placeholderFailureMessage)
	}
	if len(sleeps) != 1 {
		t.Fatalf("sleep count = %d, want 1", len(sleeps))
	}
	if sleeps[0] != 3*time.Second {
		t.Fatalf("sleep[0] = %s, want %s", sleeps[0], 3*time.Second)
	}
}

func TestFinalizePlaceholderFailureSkipsWhenContextDone(t *testing.T) {
	module := newTestModule(validModuleConfig())

	sink := &sinkDispatcherStub{}
	module.dispatcher = sink

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	handlerErr := errors.New("handler failed")
	target := platform.OutboundTarget{
		Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
	}
	err := module.finalizePlaceholderFailure(ctx, target, "placeholder-1", handlerErr)
	if !errors.Is(err, handlerErr) {
		t.Fatalf("error = %v, want handler error", err)
	}
	if len(sink.editRequests) != 0 {
		t.Fatalf("edit request count = %d, want 0", len(sink.editRequests))
	}
}

func TestHandleArticleIgnoresBotMessages(t *testing.T) {
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

	module.dispatcher = &sinkDispatcherStub{}
	module.memory = &memoryStub{}
	module.providers = map[string]ai.LLMProvider{
		"openai": &providerStub{stream: &streamStub{chunks: []ai.LLMGenerateChunk{{Delta: "hello"}}}},
	}

	err := module.handleArticle(context.Background(), &platform.Event{
		Kind:       platform.EventKindArticleCreated,
		OccurredAt: time.Unix(1, 0).UTC(),
		Source:     platform.EventSource{Platform: platform.PlatformTelegram, ID: "tg-main"},
		Conversation: platform.Conversation{
			ID:   "chat-1",
			Type: platform.ConversationTypeGroup,
		},
		Actor:   platform.Actor{ID: "bot-1", IsBot: true},
		Article: &platform.Article{ID: "m-1", Text: "Otogi hello"},
	})
	if err != nil {
		t.Fatalf("handleArticle failed: %v", err)
	}
}

func TestHandleArticleSendsPlaceholderBeforeBuildAndStream(t *testing.T) {
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

	callOrder := make([]string, 0, 3)
	sink := &sinkDispatcherStub{
		onSend: func(platform.SendMessageRequest) {
			callOrder = append(callOrder, "send")
		},
	}
	memory := &memoryStub{
		replyChain: []core.ReplyChainEntry{
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "user-1", DisplayName: "Alice"},
				Article:      platform.Article{ID: "m-1", Text: "Otogi hello"},
				IsCurrent:    true,
			},
		},
		onGetReplyChain: func() {
			callOrder = append(callOrder, "memory")
		},
	}
	provider := &providerStub{
		stream: &streamStub{chunks: []ai.LLMGenerateChunk{
			{Delta: "hello"},
		}},
		onGenerateStream: func() {
			callOrder = append(callOrder, "provider")
		},
	}
	module.dispatcher = sink
	module.memory = memory
	module.providers = map[string]ai.LLMProvider{"openai": provider}
	module.clock = sequenceClock([]time.Time{
		time.Unix(0, 0).UTC(),
		time.Unix(2, 0).UTC(),
	})

	event := testLLMChatEvent("Otogi hello")
	if err := module.handleArticle(context.Background(), event); err != nil {
		t.Fatalf("handleArticle failed: %v", err)
	}

	if len(sink.sendRequests) != 1 {
		t.Fatalf("send request count = %d, want 1", len(sink.sendRequests))
	}
	if sink.sendRequests[0].Text != defaultThinkingPlaceholder {
		t.Fatalf("placeholder text = %q, want %q", sink.sendRequests[0].Text, defaultThinkingPlaceholder)
	}
	if sink.sendRequests[0].ReplyToMessageID != event.Article.ID {
		t.Fatalf("placeholder reply_to_message_id = %q, want %q", sink.sendRequests[0].ReplyToMessageID, event.Article.ID)
	}

	if got, want := strings.Join(callOrder, ","), "send,memory,provider"; got != want {
		t.Fatalf("call order = %q, want %q", got, want)
	}
}

func TestHandleArticleDeadlinePreflightFailureEditsPlaceholder(t *testing.T) {
	module := newTestModule(Config{
		RequestTimeout: 90 * time.Second,
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Description:          "assistant",
				Provider:             "openai",
				Model:                "gpt-test",
				SystemPromptTemplate: "You are {{.AgentName}}",
				RequestTimeout:       90 * time.Second,
			},
		},
	})

	providerCalled := false
	sink := &sinkDispatcherStub{}
	module.dispatcher = sink
	module.memory = &memoryStub{
		replyChain: []core.ReplyChainEntry{
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "user-1", DisplayName: "Alice"},
				Article:      platform.Article{ID: "m-1", Text: "Otogi hello"},
				IsCurrent:    true,
			},
		},
	}
	module.providers = map[string]ai.LLMProvider{
		"openai": &providerStub{
			stream: &streamStub{chunks: []ai.LLMGenerateChunk{{Delta: "unused"}}},
			onGenerateStream: func() {
				providerCalled = true
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := module.handleArticle(ctx, testLLMChatEvent("Otogi hello"))
	if err == nil {
		t.Fatal("handleArticle error = nil, want deadline preflight failure")
	}
	if !strings.Contains(err.Error(), "llmchat preflight for agent Otogi") {
		t.Fatalf("error = %q, want llmchat preflight context", err)
	}
	if !strings.Contains(err.Error(), "insufficient handler deadline budget") {
		t.Fatalf("error = %q, want insufficient handler deadline budget", err)
	}
	if !strings.Contains(err.Error(), "remaining=") {
		t.Fatalf("error = %q, want remaining deadline detail", err)
	}
	if !strings.Contains(err.Error(), "request_timeout=1m30s") {
		t.Fatalf("error = %q, want request timeout detail", err)
	}
	if !strings.Contains(err.Error(), "kernel.module_handler_timeout") {
		t.Fatalf("error = %q, want handler timeout guidance", err)
	}
	if providerCalled {
		t.Fatal("provider generate stream should not be called when deadline preflight fails")
	}
	if len(sink.sendRequests) != 1 {
		t.Fatalf("send request count = %d, want 1", len(sink.sendRequests))
	}
	if sink.sendRequests[0].Text != defaultThinkingPlaceholder {
		t.Fatalf("placeholder text = %q, want %q", sink.sendRequests[0].Text, defaultThinkingPlaceholder)
	}
	if len(sink.editRequests) != 1 {
		t.Fatalf("edit request count = %d, want 1", len(sink.editRequests))
	}
	if sink.editRequests[0].Text != placeholderFailureMessage {
		t.Fatalf("failure edit text = %q, want %q", sink.editRequests[0].Text, placeholderFailureMessage)
	}
}

func TestHandleArticleBuildFailureEditsPlaceholder(t *testing.T) {
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

	providerCalled := false
	sink := &sinkDispatcherStub{}
	module.dispatcher = sink
	module.memory = &memoryStub{
		replyErr: errors.New("reply chain failed"),
	}
	module.providers = map[string]ai.LLMProvider{
		"openai": &providerStub{
			stream: &streamStub{chunks: []ai.LLMGenerateChunk{{Delta: "unused"}}},
			onGenerateStream: func() {
				providerCalled = true
			},
		},
	}

	err := module.handleArticle(context.Background(), testLLMChatEvent("Otogi hello"))
	if err == nil {
		t.Fatal("handleArticle error = nil, want build failure")
	}
	if !strings.Contains(err.Error(), "llmchat build request for agent Otogi") {
		t.Fatalf("error = %q, want build request context", err)
	}
	if providerCalled {
		t.Fatal("provider generate stream should not be called on build failure")
	}
	if len(sink.sendRequests) != 1 {
		t.Fatalf("send request count = %d, want 1", len(sink.sendRequests))
	}
	if len(sink.editRequests) != 1 {
		t.Fatalf("edit request count = %d, want 1", len(sink.editRequests))
	}
	if sink.editRequests[0].MessageID != "msg-1" {
		t.Fatalf("failure edit message id = %q, want msg-1", sink.editRequests[0].MessageID)
	}
	if sink.editRequests[0].Text != placeholderFailureMessage {
		t.Fatalf("failure edit text = %q, want %q", sink.editRequests[0].Text, placeholderFailureMessage)
	}
}

func TestHandleArticleProviderStartFailureEditsPlaceholder(t *testing.T) {
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
				Article:      platform.Article{ID: "m-1", Text: "Otogi hello"},
				IsCurrent:    true,
			},
		},
	}
	module.providers = map[string]ai.LLMProvider{
		"openai": &providerStub{
			streamErr: errors.New("provider start failed"),
		},
	}

	err := module.handleArticle(context.Background(), testLLMChatEvent("Otogi hello"))
	if err == nil {
		t.Fatal("handleArticle error = nil, want stream startup failure")
	}
	if !strings.Contains(err.Error(), "llmchat stream response for agent Otogi") {
		t.Fatalf("error = %q, want stream response context", err)
	}
	if len(sink.sendRequests) != 1 {
		t.Fatalf("send request count = %d, want 1", len(sink.sendRequests))
	}
	if len(sink.editRequests) != 1 {
		t.Fatalf("edit request count = %d, want 1", len(sink.editRequests))
	}
	if sink.editRequests[0].MessageID != "msg-1" {
		t.Fatalf("failure edit message id = %q, want msg-1", sink.editRequests[0].MessageID)
	}
	if sink.editRequests[0].Text != placeholderFailureMessage {
		t.Fatalf("failure edit text = %q, want %q", sink.editRequests[0].Text, placeholderFailureMessage)
	}
}

func TestHandleArticleThinkingOnlyStreamEditsPlaceholderFailure(t *testing.T) {
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
				Article:      platform.Article{ID: "m-1", Text: "Otogi hello"},
				IsCurrent:    true,
			},
		},
	}
	module.providers = map[string]ai.LLMProvider{
		"openai": &providerStub{
			stream: &streamStub{chunks: []ai.LLMGenerateChunk{
				{Kind: ai.LLMGenerateChunkKindThinkingSummary, Delta: "draft steps"},
			}},
		},
	}
	module.clock = sequenceClock([]time.Time{
		time.Unix(0, 0).UTC(),
		time.Unix(0, 0).UTC(),
	})

	err := module.handleArticle(context.Background(), testLLMChatEvent("Otogi hello"))
	if err == nil {
		t.Fatal("handleArticle error = nil, want thinking-only stream failure")
	}
	if !strings.Contains(err.Error(), "llmchat stream response for agent Otogi") {
		t.Fatalf("error = %q, want stream response context", err)
	}
	if !strings.Contains(err.Error(), "no output text received") {
		t.Fatalf("error = %q, want no output text received", err)
	}
	if len(sink.sendRequests) != 1 {
		t.Fatalf("send request count = %d, want 1", len(sink.sendRequests))
	}
	if sink.sendRequests[0].Text != defaultThinkingPlaceholder {
		t.Fatalf("placeholder text = %q, want %q", sink.sendRequests[0].Text, defaultThinkingPlaceholder)
	}
	if len(sink.editRequests) != 2 {
		t.Fatalf("edit request count = %d, want 2", len(sink.editRequests))
	}
	if sink.editRequests[0].Text != "Thinking...\n\ndraft steps" {
		t.Fatalf("thinking edit text = %q, want %q", sink.editRequests[0].Text, "Thinking...\n\ndraft steps")
	}
	if sink.editRequests[1].MessageID != "msg-1" {
		t.Fatalf("failure edit message id = %q, want msg-1", sink.editRequests[1].MessageID)
	}
	if sink.editRequests[1].Text != placeholderFailureMessage {
		t.Fatalf("failure edit text = %q, want %q", sink.editRequests[1].Text, placeholderFailureMessage)
	}
}

func testLLMChatEvent(text string) *platform.Event {
	return &platform.Event{
		Kind:       platform.EventKindArticleCreated,
		OccurredAt: time.Unix(1, 0).UTC(),
		Source:     platform.EventSource{Platform: platform.PlatformTelegram, ID: "tg-main"},
		Conversation: platform.Conversation{
			ID:   "chat-1",
			Type: platform.ConversationTypeGroup,
		},
		Actor:   platform.Actor{ID: "user-1", DisplayName: "Alice"},
		Article: &platform.Article{ID: "m-1", Text: text},
	}
}

type sinkDispatcherStub struct {
	sendRequests []platform.SendMessageRequest
	editRequests []platform.EditMessageRequest
	sendErrors   []error
	editErrors   []error
	onSend       func(platform.SendMessageRequest)
	onEdit       func(platform.EditMessageRequest)
}

func (s *sinkDispatcherStub) SendMessage(
	_ context.Context,
	req platform.SendMessageRequest,
) (*platform.OutboundMessage, error) {
	index := len(s.sendRequests)
	s.sendRequests = append(s.sendRequests, req)
	if s.onSend != nil {
		s.onSend(req)
	}
	if index < len(s.sendErrors) && s.sendErrors[index] != nil {
		return nil, s.sendErrors[index]
	}

	return &platform.OutboundMessage{ID: fmt.Sprintf("msg-%d", index+1), Target: req.Target}, nil
}

func (s *sinkDispatcherStub) EditMessage(_ context.Context, req platform.EditMessageRequest) error {
	index := len(s.editRequests)
	s.editRequests = append(s.editRequests, req)
	if s.onEdit != nil {
		s.onEdit(req)
	}
	if index < len(s.editErrors) {
		return s.editErrors[index]
	}

	return nil
}

func (*sinkDispatcherStub) DeleteMessage(context.Context, platform.DeleteMessageRequest) error {
	return nil
}

func (*sinkDispatcherStub) SetReaction(context.Context, platform.SetReactionRequest) error {
	return nil
}
func (*sinkDispatcherStub) ListSinks(context.Context) ([]platform.EventSink, error) { return nil, nil }
func (*sinkDispatcherStub) ListSinksByPlatform(context.Context, platform.Platform) ([]platform.EventSink, error) {
	return nil, nil
}

type memoryStub struct {
	replyChain                []core.ReplyChainEntry
	replyErr                  error
	repliedMemory             core.Memory
	repliedFound              bool
	repliedErr                error
	leadingContext            []core.ConversationContextEntry
	leadingContextErr         error
	memories                  map[string]core.Memory
	onGet                     func(core.MemoryLookup)
	onGetBatch                func([]core.MemoryLookup)
	onGetReplied              func()
	onGetReplyChain           func()
	onListConversationContext func()
}

func (m *memoryStub) Get(_ context.Context, lookup core.MemoryLookup) (core.Memory, bool, error) {
	if m.onGet != nil {
		m.onGet(lookup)
	}
	if m.memories == nil {
		return core.Memory{}, false, nil
	}
	mem, found := m.memories[lookup.ArticleID]
	return mem, found, nil
}

func (m *memoryStub) GetBatch(
	_ context.Context,
	lookups []core.MemoryLookup,
) (map[core.MemoryLookup]core.Memory, error) {
	if m.onGetBatch != nil {
		cloned := append([]core.MemoryLookup(nil), lookups...)
		m.onGetBatch(cloned)
	}
	if m.memories == nil {
		return nil, nil
	}

	results := make(map[core.MemoryLookup]core.Memory)
	for _, lookup := range lookups {
		mem, found := m.memories[lookup.ArticleID]
		if !found {
			continue
		}
		results[lookup] = mem
	}

	return results, nil
}

func (m *memoryStub) GetReplied(context.Context, *platform.Event) (core.Memory, bool, error) {
	if m.onGetReplied != nil {
		m.onGetReplied()
	}
	if m.repliedErr != nil {
		return core.Memory{}, false, m.repliedErr
	}
	if !m.repliedFound {
		return core.Memory{}, false, nil
	}

	return m.repliedMemory, true, nil
}

func (m *memoryStub) GetReplyChain(context.Context, *platform.Event) ([]core.ReplyChainEntry, error) {
	if m.onGetReplyChain != nil {
		m.onGetReplyChain()
	}
	if m.replyErr != nil {
		return nil, m.replyErr
	}

	cloned := make([]core.ReplyChainEntry, 0, len(m.replyChain))
	for _, entry := range m.replyChain {
		cloned = append(cloned, core.ReplyChainEntry{
			Conversation: entry.Conversation,
			Actor:        entry.Actor,
			Article:      entry.Article,
			IsCurrent:    entry.IsCurrent,
		})
	}

	return cloned, nil
}

func (m *memoryStub) ListConversationContextBefore(
	context.Context,
	core.ConversationContextBeforeQuery,
) ([]core.ConversationContextEntry, error) {
	if m.onListConversationContext != nil {
		m.onListConversationContext()
	}
	if m.leadingContextErr != nil {
		return nil, m.leadingContextErr
	}

	cloned := make([]core.ConversationContextEntry, 0, len(m.leadingContext))
	for _, entry := range m.leadingContext {
		cloned = append(cloned, core.ConversationContextEntry{
			Conversation: entry.Conversation,
			Actor:        entry.Actor,
			Article:      entry.Article,
			CreatedAt:    entry.CreatedAt,
			UpdatedAt:    entry.UpdatedAt,
		})
	}

	return cloned, nil
}

type providerStub struct {
	stream           ai.LLMStream
	streams          []ai.LLMStream
	streamErr        error
	onGenerateStream func()
	lastRequest      ai.LLMGenerateRequest
	requests         []ai.LLMGenerateRequest
}

type markdownParserStub struct{}

func (markdownParserStub) ParseMarkdown(
	_ context.Context,
	markdown string,
) (platform.ParsedText, error) {
	return platform.ParsedText{
		Text: markdown,
	}, nil
}

type markdownParserResultStub struct {
	result platform.ParsedText
	err    error
}

func (s markdownParserResultStub) ParseMarkdown(
	_ context.Context,
	markdown string,
) (platform.ParsedText, error) {
	if s.err != nil {
		return platform.ParsedText{}, s.err
	}
	if s.result.Text == "" {
		s.result.Text = markdown
	}

	return s.result, nil
}

type markdownParserSequenceStub struct {
	results []platform.ParsedText
	calls   int
}

func (s *markdownParserSequenceStub) ParseMarkdown(
	_ context.Context,
	markdown string,
) (platform.ParsedText, error) {
	if s == nil || len(s.results) == 0 {
		return platform.ParsedText{Text: markdown}, nil
	}

	index := s.calls
	s.calls++
	if index >= len(s.results) {
		index = len(s.results) - 1
	}
	result := s.results[index]
	if result.Text == "" {
		result.Text = markdown
	}

	return result, nil
}

func (p *providerStub) GenerateStream(
	_ context.Context,
	req ai.LLMGenerateRequest,
) (ai.LLMStream, error) {
	if p.onGenerateStream != nil {
		p.onGenerateStream()
	}
	if p.streamErr != nil {
		return nil, p.streamErr
	}
	if len(p.streams) > 0 {
		stream := p.streams[0]
		p.streams = p.streams[1:]
		if stream == nil {
			return nil, fmt.Errorf("nil stream")
		}

		p.lastRequest = cloneGenerateRequestForTest(req)
		p.requests = append(p.requests, cloneGenerateRequestForTest(req))
		return stream, nil
	}
	if p.stream == nil {
		return nil, fmt.Errorf("nil stream")
	}

	p.lastRequest = cloneGenerateRequestForTest(req)
	p.requests = append(p.requests, cloneGenerateRequestForTest(req))
	return p.stream, nil
}

type streamStub struct {
	chunks   []ai.LLMGenerateChunk
	index    int
	recvErr  error
	closeErr error
}

func (s *streamStub) Recv(context.Context) (ai.LLMGenerateChunk, error) {
	if s.recvErr != nil {
		return ai.LLMGenerateChunk{}, s.recvErr
	}
	if s.index >= len(s.chunks) {
		return ai.LLMGenerateChunk{}, io.EOF
	}
	chunk := s.chunks[s.index]
	s.index++

	return chunk, nil
}

func (s *streamStub) Close() error {
	return s.closeErr
}

type mediaDownloaderStub struct {
	mu              sync.Mutex
	lastRequest     platform.MediaDownloadRequest
	requests        []platform.MediaDownloadRequest
	attachment      platform.MediaAttachment
	err             error
	writeErr        error
	data            []byte
	downloadDelay   time.Duration
	activeDownloads atomic.Int32
	maxConcurrent   atomic.Int32
}

func (s *mediaDownloaderStub) Download(
	_ context.Context,
	request platform.MediaDownloadRequest,
	output io.Writer,
) (platform.MediaAttachment, error) {
	active := s.activeDownloads.Add(1)
	defer s.activeDownloads.Add(-1)
	for {
		currentMax := s.maxConcurrent.Load()
		if active <= currentMax {
			break
		}
		if s.maxConcurrent.CompareAndSwap(currentMax, active) {
			break
		}
	}

	s.mu.Lock()
	s.lastRequest = request
	s.requests = append(s.requests, request)
	attachment := s.attachment
	err := s.err
	writeErr := s.writeErr
	data := append([]byte(nil), s.data...)
	delay := s.downloadDelay
	s.mu.Unlock()

	if delay > 0 {
		time.Sleep(delay)
	}
	if err != nil {
		return platform.MediaAttachment{}, s.err
	}
	if len(data) > 0 {
		if _, err := output.Write(data); err != nil {
			return platform.MediaAttachment{}, fmt.Errorf("write media downloader stub output: %w", err)
		}
	}
	if writeErr != nil {
		return platform.MediaAttachment{}, writeErr
	}

	return attachment, nil
}

type moduleRuntimeStub struct {
	registry core.ServiceRegistry
	configs  core.ConfigRegistry
}

func (s moduleRuntimeStub) Services() core.ServiceRegistry { return s.registry }

func (s moduleRuntimeStub) Config() core.ConfigRegistry { return s.configs }

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

func (s serviceRegistryStub) Register(string, any) error { return nil }

func (s serviceRegistryStub) Resolve(name string) (any, error) {
	value, ok := s.values[name]
	if !ok {
		return nil, core.ErrServiceNotFound
	}

	return value, nil
}

func newTestModule(cfg Config) *Module {
	module := New()
	module.cfg = cfg
	return module
}

func cloneGenerateRequestForTest(req ai.LLMGenerateRequest) ai.LLMGenerateRequest {
	cloned := req
	if len(req.Messages) > 0 {
		cloned.Messages = make([]ai.LLMMessage, 0, len(req.Messages))
		for _, message := range req.Messages {
			messageClone := message
			if len(message.Parts) > 0 {
				messageClone.Parts = make([]ai.LLMMessagePart, 0, len(message.Parts))
				for _, part := range message.Parts {
					partClone := part
					if part.Image != nil {
						imageClone := *part.Image
						if len(part.Image.Data) > 0 {
							imageClone.Data = append([]byte(nil), part.Image.Data...)
						}
						partClone.Image = &imageClone
					}
					messageClone.Parts = append(messageClone.Parts, partClone)
				}
			}
			if len(message.ToolCalls) > 0 {
				messageClone.ToolCalls = append([]ai.LLMToolCall(nil), message.ToolCalls...)
			}
			cloned.Messages = append(cloned.Messages, messageClone)
		}
	}
	if len(req.Tools) > 0 {
		cloned.Tools = make([]ai.LLMToolDefinition, 0, len(req.Tools))
		for _, tool := range req.Tools {
			toolClone := tool
			if len(tool.Parameters) > 0 {
				toolClone.Parameters = append([]byte(nil), tool.Parameters...)
			}
			cloned.Tools = append(cloned.Tools, toolClone)
		}
	}
	cloned.Metadata = cloneStringMap(req.Metadata)

	return cloned
}

func allMessageContents(messages []ai.LLMMessage) []string {
	contents := make([]string, 0, len(messages))
	for _, message := range messages {
		var builder strings.Builder
		for _, part := range message.ContentParts() {
			if part.Type != ai.LLMMessagePartTypeText {
				continue
			}
			builder.WriteString(part.Text)
		}
		contents = append(contents, builder.String())
	}

	return contents
}

func sequenceClock(times []time.Time) func() time.Time {
	if len(times) == 0 {
		return time.Now
	}

	index := 0
	return func() time.Time {
		if index >= len(times) {
			return times[len(times)-1]
		}
		current := times[index]
		index++
		return current
	}
}
