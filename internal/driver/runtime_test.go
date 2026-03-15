package driver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

const (
	testDriverType     = "test-chat"
	testDriverTypeAlt  = "test-chat-alt"
	testDriverPlatform = platform.Platform("test-platform")
)

func TestNewRegistryRejectsDuplicateDescriptorType(t *testing.T) {
	t.Parallel()

	_, err := NewRegistry([]Descriptor{
		{
			Type:     testDriverType,
			Platform: testDriverPlatform,
			Builder:  stubRuntimeBuilder,
		},
		{
			Type:     testDriverType,
			Platform: testDriverPlatform,
			Builder:  stubRuntimeBuilder,
		},
	})
	if err == nil {
		t.Fatal("expected duplicate descriptor type error")
	}
}

func TestNewRegistryRejectsEmptyPlatform(t *testing.T) {
	t.Parallel()

	_, err := NewRegistry([]Descriptor{
		{
			Type:     testDriverType,
			Platform: "",
			Builder:  stubRuntimeBuilder,
		},
	})
	if err == nil {
		t.Fatal("expected empty platform error")
	}
}

func TestRegistryPlatformForType(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry([]Descriptor{
		{
			Type:     testDriverType,
			Platform: testDriverPlatform,
			Builder:  stubRuntimeBuilder,
		},
	})
	if err != nil {
		t.Fatalf("new registry failed: %v", err)
	}

	platform, err := registry.PlatformForType(testDriverType)
	if err != nil {
		t.Fatalf("platform for type failed: %v", err)
	}
	if platform != testDriverPlatform {
		t.Fatalf("platform = %s, want %s", platform, testDriverPlatform)
	}

	if _, err := registry.PlatformForType("unknown"); err == nil {
		t.Fatal("expected unknown type error")
	}
}

func TestRegistryTypesSorted(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry([]Descriptor{
		{
			Type:     testDriverTypeAlt,
			Platform: platform.Platform("test-platform-alt"),
			Builder:  stubRuntimeBuilder,
		},
		{
			Type:     testDriverType,
			Platform: testDriverPlatform,
			Builder:  stubRuntimeBuilder,
		},
	})
	if err != nil {
		t.Fatalf("new registry failed: %v", err)
	}

	types := registry.Types()
	want := []string{testDriverType, testDriverTypeAlt}
	if !slices.Equal(types, want) {
		t.Fatalf("types = %v, want %v", types, want)
	}
}

func TestRegistryBuildEnabled(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry([]Descriptor{
		{
			Type:     testDriverType,
			Platform: testDriverPlatform,
			Builder: func(
				_ context.Context,
				definition Definition,
				_ *slog.Logger,
			) (Runtime, error) {
				if definition.Name == "broken" {
					return Runtime{}, errors.New("broken build")
				}

				return Runtime{
					Source: platform.EventSource{
						Platform: testDriverPlatform,
					},
					Driver: stubDriver{name: definition.Name},
				}, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("new registry failed: %v", err)
	}

	_, err = registry.BuildEnabled(context.Background(), []Definition{
		{Name: "primary", Type: testDriverType, Enabled: true, Config: []byte("{}")},
		{Name: "broken", Type: testDriverType, Enabled: true, Config: []byte("{}")},
	}, slog.Default())
	if err == nil {
		t.Fatal("expected build error")
	}
}

func TestRegistryBuildEnabledBridgesArticleTagsToDriverEvents(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry([]Descriptor{
		{
			Type:     testDriverType,
			Platform: testDriverPlatform,
			Builder: func(
				_ context.Context,
				_ Definition,
				_ *slog.Logger,
			) (Runtime, error) {
				return Runtime{
					Source: platform.EventSource{Platform: testDriverPlatform},
					Driver: publishingDriver{
						event: &platform.Event{
							ID:         "evt-1",
							Kind:       platform.EventKindArticleCreated,
							OccurredAt: time.Unix(1, 0).UTC(),
							Conversation: platform.Conversation{
								ID:   "chat-1",
								Type: platform.ConversationTypeGroup,
							},
							Article: &platform.Article{ID: "1", Text: "hello"},
						},
					},
					SinkDispatcher: &stubSinkDispatcher{},
				}, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("new registry failed: %v", err)
	}

	runtimes, err := registry.BuildEnabled(context.Background(), []Definition{
		{Name: "main", Type: testDriverType, Enabled: true, Config: []byte("{}")},
	}, slog.Default())
	if err != nil {
		t.Fatalf("build enabled failed: %v", err)
	}
	if len(runtimes) != 1 {
		t.Fatalf("runtimes len = %d, want 1", len(runtimes))
	}

	response, err := runtimes[0].SinkDispatcher.SendMessage(context.Background(), platform.SendMessageRequest{
		Target: platform.OutboundTarget{
			Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
		},
		Text: "hello",
		Tags: map[string]string{
			"llmchat.agent": "Otogi",
		},
	})
	if err != nil {
		t.Fatalf("send message failed: %v", err)
	}
	if response == nil {
		t.Fatal("expected outbound response")
	}
	if response.Tags["llmchat.agent"] != "Otogi" {
		t.Fatalf("response tags = %+v, want llmchat.agent=Otogi", response.Tags)
	}

	capture := &captureEventDispatcher{}
	if err := runtimes[0].Driver.Start(context.Background(), capture); err != nil {
		t.Fatalf("driver start failed: %v", err)
	}
	if len(capture.events) != 1 {
		t.Fatalf("captured events len = %d, want 1", len(capture.events))
	}
	if capture.events[0].Article == nil {
		t.Fatal("expected article payload")
	}
	if capture.events[0].Article.Tags["llmchat.agent"] != "Otogi" {
		t.Fatalf("article tags = %+v, want llmchat.agent=Otogi", capture.events[0].Article.Tags)
	}
}

func TestRegistryBuildEnabledDoesNotBridgeArticleTagsForMismatchedArticles(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry([]Descriptor{
		{
			Type:     testDriverType,
			Platform: testDriverPlatform,
			Builder: func(
				_ context.Context,
				_ Definition,
				_ *slog.Logger,
			) (Runtime, error) {
				return Runtime{
					Source: platform.EventSource{Platform: testDriverPlatform},
					Driver: publishingDriver{
						event: &platform.Event{
							ID:         "evt-1",
							Kind:       platform.EventKindArticleCreated,
							OccurredAt: time.Unix(1, 0).UTC(),
							Conversation: platform.Conversation{
								ID:   "chat-1",
								Type: platform.ConversationTypeGroup,
							},
							Article: &platform.Article{ID: "2", Text: "hello"},
						},
					},
					SinkDispatcher: &stubSinkDispatcher{},
				}, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("new registry failed: %v", err)
	}

	runtimes, err := registry.BuildEnabled(context.Background(), []Definition{
		{Name: "main", Type: testDriverType, Enabled: true, Config: []byte("{}")},
	}, slog.Default())
	if err != nil {
		t.Fatalf("build enabled failed: %v", err)
	}

	if _, err := runtimes[0].SinkDispatcher.SendMessage(context.Background(), platform.SendMessageRequest{
		Target: platform.OutboundTarget{
			Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
		},
		Text: "hello",
		Tags: map[string]string{
			"llmchat.agent": "Otogi",
		},
	}); err != nil {
		t.Fatalf("send message failed: %v", err)
	}

	capture := &captureEventDispatcher{}
	if err := runtimes[0].Driver.Start(context.Background(), capture); err != nil {
		t.Fatalf("driver start failed: %v", err)
	}
	if len(capture.events) != 1 {
		t.Fatalf("captured events len = %d, want 1", len(capture.events))
	}
	if capture.events[0].Article == nil {
		t.Fatal("expected article payload")
	}
	if len(capture.events[0].Article.Tags) != 0 {
		t.Fatalf("article tags = %+v, want none", capture.events[0].Article.Tags)
	}
}

func TestRegistryBuildEnabledBridgesArticleTagsToEditEvents(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry([]Descriptor{
		{
			Type:     testDriverType,
			Platform: testDriverPlatform,
			Builder: func(
				_ context.Context,
				_ Definition,
				_ *slog.Logger,
			) (Runtime, error) {
				return Runtime{
					Source: platform.EventSource{Platform: testDriverPlatform},
					Driver: publishingDriver{
						event: &platform.Event{
							ID:         "evt-edit-1",
							Kind:       platform.EventKindArticleEdited,
							OccurredAt: time.Unix(2, 0).UTC(),
							Conversation: platform.Conversation{
								ID:   "chat-1",
								Type: platform.ConversationTypeGroup,
							},
							Mutation: &platform.ArticleMutation{
								Type:            platform.MutationTypeEdit,
								TargetArticleID: "1",
								After: &platform.ArticleSnapshot{
									Text: "hello edited",
								},
							},
						},
					},
					SinkDispatcher: &stubSinkDispatcher{},
				}, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("new registry failed: %v", err)
	}

	runtimes, err := registry.BuildEnabled(context.Background(), []Definition{
		{Name: "main", Type: testDriverType, Enabled: true, Config: []byte("{}")},
	}, slog.Default())
	if err != nil {
		t.Fatalf("build enabled failed: %v", err)
	}
	if len(runtimes) != 1 {
		t.Fatalf("runtimes len = %d, want 1", len(runtimes))
	}

	if _, err := runtimes[0].SinkDispatcher.SendMessage(context.Background(), platform.SendMessageRequest{
		Target: platform.OutboundTarget{
			Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
		},
		Text: "hello",
		Tags: map[string]string{
			"platform.module": "llmchat",
			"llmchat.agent":   "Otogi",
		},
	}); err != nil {
		t.Fatalf("send message failed: %v", err)
	}

	capture := &captureEventDispatcher{}
	if err := runtimes[0].Driver.Start(context.Background(), capture); err != nil {
		t.Fatalf("driver start failed: %v", err)
	}
	if len(capture.events) != 1 {
		t.Fatalf("captured events len = %d, want 1", len(capture.events))
	}
	event := capture.events[0]
	if event.Article == nil {
		t.Fatal("expected article payload on edit event")
	}
	if event.Article.Tags["platform.module"] != "llmchat" {
		t.Fatalf("article tags = %+v, want platform.module=llmchat", event.Article.Tags)
	}
	if event.Article.Tags["llmchat.agent"] != "Otogi" {
		t.Fatalf("article tags = %+v, want llmchat.agent=Otogi", event.Article.Tags)
	}
	if event.Article.ID != "1" {
		t.Fatalf("article id = %q, want 1", event.Article.ID)
	}
}

func TestRegistryBuildEnabledBridgeEditDoesNotConsumeTags(t *testing.T) {
	t.Parallel()

	// After projecting tags onto an edit, the bridge entry must still be
	// available for a subsequent article.created event (peek, not take).
	bridge := newArticleTagBridge(defaultArticleTagBridgeTTL, defaultArticleTagBridgeMaxEntries)
	source := platform.EventSource{Platform: testDriverPlatform, ID: "main"}
	bridge.remember(source, "chat-1", "1", map[string]string{"llmchat.agent": "Otogi"}, time.Now().UTC())

	dispatcher := &articleTagEventDispatcher{
		source: source,
		base:   &captureEventDispatcher{},
		bridge: bridge,
	}

	editEvent := &platform.Event{
		ID:         "evt-edit-1",
		Kind:       platform.EventKindArticleEdited,
		OccurredAt: time.Unix(2, 0).UTC(),
		Source:     source,
		Conversation: platform.Conversation{
			ID:   "chat-1",
			Type: platform.ConversationTypeGroup,
		},
		Mutation: &platform.ArticleMutation{
			Type:            platform.MutationTypeEdit,
			TargetArticleID: "1",
			After:           &platform.ArticleSnapshot{Text: "edited"},
		},
	}
	if err := dispatcher.Publish(context.Background(), editEvent); err != nil {
		t.Fatalf("publish edit failed: %v", err)
	}
	if editEvent.Article == nil || editEvent.Article.Tags["llmchat.agent"] != "Otogi" {
		t.Fatalf("edit event article tags = %+v, want llmchat.agent=Otogi", editEvent.Article)
	}

	// The tags must still be available for take (article.created).
	tags, ok := bridge.take(source, "chat-1", "1", time.Now().UTC())
	if !ok {
		t.Fatal("expected bridge entry to survive after edit peek")
	}
	if tags["llmchat.agent"] != "Otogi" {
		t.Fatalf("take tags = %+v, want llmchat.agent=Otogi", tags)
	}
}

func TestRegistryBuildEnabledDoesNotBridgeEditTagsForMismatchedArticles(t *testing.T) {
	t.Parallel()

	bridge := newArticleTagBridge(defaultArticleTagBridgeTTL, defaultArticleTagBridgeMaxEntries)
	source := platform.EventSource{Platform: testDriverPlatform, ID: "main"}
	bridge.remember(source, "chat-1", "1", map[string]string{"llmchat.agent": "Otogi"}, time.Now().UTC())

	dispatcher := &articleTagEventDispatcher{
		source: source,
		base:   &captureEventDispatcher{},
		bridge: bridge,
	}

	editEvent := &platform.Event{
		ID:         "evt-edit-1",
		Kind:       platform.EventKindArticleEdited,
		OccurredAt: time.Unix(2, 0).UTC(),
		Source:     source,
		Conversation: platform.Conversation{
			ID:   "chat-1",
			Type: platform.ConversationTypeGroup,
		},
		Mutation: &platform.ArticleMutation{
			Type:            platform.MutationTypeEdit,
			TargetArticleID: "999",
			After:           &platform.ArticleSnapshot{Text: "edited"},
		},
	}
	if err := dispatcher.Publish(context.Background(), editEvent); err != nil {
		t.Fatalf("publish edit failed: %v", err)
	}
	if editEvent.Article != nil {
		t.Fatalf("expected no article on mismatched edit, got %+v", editEvent.Article)
	}
}

func TestSyntheticArticleCreatedOnSendWithTags(t *testing.T) {
	t.Parallel()

	bridge := newArticleTagBridge(defaultArticleTagBridgeTTL, defaultArticleTagBridgeMaxEntries)
	source := platform.EventSource{Platform: testDriverPlatform, ID: "main"}
	kernelCapture := &captureEventDispatcher{}
	bridge.setDispatcher(kernelCapture)

	dispatcher := &articleTagSinkDispatcher{
		source: source,
		base:   &stubSinkDispatcher{},
		bridge: bridge,
	}

	_, err := dispatcher.SendMessage(context.Background(), platform.SendMessageRequest{
		Target: platform.OutboundTarget{
			Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
		},
		Text:             "placeholder",
		ReplyToMessageID: "msg-100",
		Tags: map[string]string{
			"platform.module": "llmchat",
			"llmchat.agent":   "Otogi",
		},
	})
	if err != nil {
		t.Fatalf("send message failed: %v", err)
	}

	if len(kernelCapture.events) != 1 {
		t.Fatalf("synthetic events = %d, want 1", len(kernelCapture.events))
	}

	synth := kernelCapture.events[0]
	if synth.Kind != platform.EventKindArticleCreated {
		t.Fatalf("kind = %s, want %s", synth.Kind, platform.EventKindArticleCreated)
	}
	if synth.Source != source {
		t.Fatalf("source = %+v, want %+v", synth.Source, source)
	}
	if synth.Conversation.ID != "chat-1" {
		t.Fatalf("conversation id = %s, want chat-1", synth.Conversation.ID)
	}
	if !synth.Actor.IsBot {
		t.Fatal("expected Actor.IsBot = true on synthetic event")
	}
	if synth.Article == nil {
		t.Fatal("expected article on synthetic event")
	}
	if synth.Article.Text != "placeholder" {
		t.Fatalf("article text = %q, want placeholder", synth.Article.Text)
	}
	if synth.Article.Tags["platform.module"] != "llmchat" {
		t.Fatalf("article tags = %+v, want platform.module=llmchat", synth.Article.Tags)
	}
	if synth.Article.Tags["llmchat.agent"] != "Otogi" {
		t.Fatalf("article tags = %+v, want llmchat.agent=Otogi", synth.Article.Tags)
	}
	if synth.Article.ReplyToArticleID != "msg-100" {
		t.Fatalf("reply to = %q, want msg-100", synth.Article.ReplyToArticleID)
	}
}

func TestSyntheticArticleEditedOnEditWithTags(t *testing.T) {
	t.Parallel()

	bridge := newArticleTagBridge(defaultArticleTagBridgeTTL, defaultArticleTagBridgeMaxEntries)
	source := platform.EventSource{Platform: testDriverPlatform, ID: "main"}
	kernelCapture := &captureEventDispatcher{}
	bridge.setDispatcher(kernelCapture)

	bridge.remember(source, "chat-1", "msg-1", map[string]string{
		"platform.module": "llmchat",
		"llmchat.agent":   "Otogi",
	}, time.Now().UTC())

	dispatcher := &articleTagSinkDispatcher{
		source: source,
		base:   &stubSinkDispatcher{},
		bridge: bridge,
	}

	err := dispatcher.EditMessage(context.Background(), platform.EditMessageRequest{
		Target: platform.OutboundTarget{
			Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
		},
		MessageID: "msg-1",
		Text:      "LLM response",
	})
	if err != nil {
		t.Fatalf("edit message failed: %v", err)
	}

	if len(kernelCapture.events) != 1 {
		t.Fatalf("synthetic events = %d, want 1", len(kernelCapture.events))
	}

	synth := kernelCapture.events[0]
	if synth.Kind != platform.EventKindArticleEdited {
		t.Fatalf("kind = %s, want %s", synth.Kind, platform.EventKindArticleEdited)
	}
	if !synth.Actor.IsBot {
		t.Fatal("expected Actor.IsBot = true on synthetic event")
	}
	if synth.Mutation == nil {
		t.Fatal("expected mutation on synthetic edit event")
	}
	if synth.Mutation.TargetArticleID != "msg-1" {
		t.Fatalf("mutation target = %q, want msg-1", synth.Mutation.TargetArticleID)
	}
	if synth.Mutation.After == nil || synth.Mutation.After.Text != "LLM response" {
		t.Fatalf("mutation after text = %v, want LLM response", synth.Mutation)
	}
	if synth.Article == nil || synth.Article.Tags["llmchat.agent"] != "Otogi" {
		t.Fatalf("article tags = %+v, want llmchat.agent=Otogi", synth.Article)
	}
}

func TestNoSyntheticEventWhenNoTags(t *testing.T) {
	t.Parallel()

	bridge := newArticleTagBridge(defaultArticleTagBridgeTTL, defaultArticleTagBridgeMaxEntries)
	source := platform.EventSource{Platform: testDriverPlatform, ID: "main"}
	kernelCapture := &captureEventDispatcher{}
	bridge.setDispatcher(kernelCapture)

	dispatcher := &articleTagSinkDispatcher{
		source: source,
		base:   &stubSinkDispatcher{},
		bridge: bridge,
	}

	_, err := dispatcher.SendMessage(context.Background(), platform.SendMessageRequest{
		Target: platform.OutboundTarget{
			Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
		},
		Text: "no tags",
	})
	if err != nil {
		t.Fatalf("send message failed: %v", err)
	}

	if len(kernelCapture.events) != 0 {
		t.Fatalf("synthetic events = %d, want 0", len(kernelCapture.events))
	}
}

func TestNoSyntheticEventWhenDispatcherNotSet(t *testing.T) {
	t.Parallel()

	bridge := newArticleTagBridge(defaultArticleTagBridgeTTL, defaultArticleTagBridgeMaxEntries)
	source := platform.EventSource{Platform: testDriverPlatform, ID: "main"}

	dispatcher := &articleTagSinkDispatcher{
		source: source,
		base:   &stubSinkDispatcher{},
		bridge: bridge,
	}

	_, err := dispatcher.SendMessage(context.Background(), platform.SendMessageRequest{
		Target: platform.OutboundTarget{
			Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
		},
		Text: "hello",
		Tags: map[string]string{"llmchat.agent": "Otogi"},
	})
	if err != nil {
		t.Fatalf("send message failed: %v", err)
	}

	// No panic, no error — synthetic event silently skipped.
}

func TestSyntheticSendDoesNotConsumeBridgeEntry(t *testing.T) {
	t.Parallel()

	bridge := newArticleTagBridge(defaultArticleTagBridgeTTL, defaultArticleTagBridgeMaxEntries)
	source := platform.EventSource{Platform: testDriverPlatform, ID: "main"}
	kernelCapture := &captureEventDispatcher{}
	bridge.setDispatcher(kernelCapture)

	dispatcher := &articleTagSinkDispatcher{
		source: source,
		base:   &stubSinkDispatcher{},
		bridge: bridge,
	}

	_, err := dispatcher.SendMessage(context.Background(), platform.SendMessageRequest{
		Target: platform.OutboundTarget{
			Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
		},
		Text: "hello",
		Tags: map[string]string{"llmchat.agent": "Otogi"},
	})
	if err != nil {
		t.Fatalf("send message failed: %v", err)
	}

	// Bridge entry must still exist for subsequent real inbound events.
	tags, ok := bridge.take(source, "chat-1", "1", time.Now().UTC())
	if !ok {
		t.Fatal("expected bridge entry to survive after synthetic event")
	}
	if tags["llmchat.agent"] != "Otogi" {
		t.Fatalf("take tags = %+v, want llmchat.agent=Otogi", tags)
	}
}

func TestSyntheticEditDoesNotConsumeBridgeEntry(t *testing.T) {
	t.Parallel()

	bridge := newArticleTagBridge(defaultArticleTagBridgeTTL, defaultArticleTagBridgeMaxEntries)
	source := platform.EventSource{Platform: testDriverPlatform, ID: "main"}
	kernelCapture := &captureEventDispatcher{}
	bridge.setDispatcher(kernelCapture)

	bridge.remember(source, "chat-1", "msg-1", map[string]string{"llmchat.agent": "Otogi"}, time.Now().UTC())

	dispatcher := &articleTagSinkDispatcher{
		source: source,
		base:   &stubSinkDispatcher{},
		bridge: bridge,
	}

	if err := dispatcher.EditMessage(context.Background(), platform.EditMessageRequest{
		Target: platform.OutboundTarget{
			Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
		},
		MessageID: "msg-1",
		Text:      "edited",
	}); err != nil {
		t.Fatalf("edit message failed: %v", err)
	}

	// Bridge entry must still exist.
	tags, ok := bridge.take(source, "chat-1", "msg-1", time.Now().UTC())
	if !ok {
		t.Fatal("expected bridge entry to survive after synthetic edit")
	}
	if tags["llmchat.agent"] != "Otogi" {
		t.Fatalf("take tags = %+v, want llmchat.agent=Otogi", tags)
	}
}

func TestSinkDispatcherRoutesByID(t *testing.T) {
	t.Parallel()

	primary := &stubSinkDispatcher{}
	secondary := &stubSinkDispatcher{}
	dispatcher, err := NewSinkDispatcher([]Runtime{
		{
			Source: platform.EventSource{
				Platform: testDriverPlatform,
				ID:       "main",
			},
			SinkDispatcher: primary,
		},
		{
			Source: platform.EventSource{
				Platform: testDriverPlatform,
				ID:       "alt",
			},
			SinkDispatcher: secondary,
		},
	})
	if err != nil {
		t.Fatalf("new sink dispatcher failed: %v", err)
	}

	_, err = dispatcher.SendMessage(context.Background(), platform.SendMessageRequest{
		Target: platform.OutboundTarget{
			Conversation: platform.Conversation{ID: "1", Type: platform.ConversationTypeGroup},
			Sink: &platform.EventSink{
				ID: "main",
			},
		},
		Text: "hello",
	})
	if err != nil {
		t.Fatalf("send message failed: %v", err)
	}
	if primary.sendCalls != 1 {
		t.Fatalf("primary calls = %d, want 1", primary.sendCalls)
	}
	if secondary.sendCalls != 0 {
		t.Fatalf("secondary calls = %d, want 0", secondary.sendCalls)
	}
}

func TestMediaDownloaderRoutesBySource(t *testing.T) {
	t.Parallel()

	primary := &stubMediaDownloader{
		attachment: platform.MediaAttachment{ID: "photo-1", Type: platform.MediaTypePhoto},
	}
	secondary := &stubMediaDownloader{
		attachment: platform.MediaAttachment{ID: "doc-2", Type: platform.MediaTypeDocument},
	}
	downloader, err := NewMediaDownloader([]Runtime{
		{
			Source: platform.EventSource{
				Platform: testDriverPlatform,
				ID:       "main",
			},
			MediaDownloader: primary,
		},
		{
			Source: platform.EventSource{
				Platform: testDriverPlatform,
				ID:       "alt",
			},
			MediaDownloader: secondary,
		},
	})
	if err != nil {
		t.Fatalf("new media downloader failed: %v", err)
	}

	got, err := downloader.Download(context.Background(), platform.MediaDownloadRequest{
		Source: platform.EventSource{
			Platform: testDriverPlatform,
			ID:       "main",
		},
		Conversation: platform.Conversation{
			ID:   "1",
			Type: platform.ConversationTypeGroup,
		},
		ArticleID:    "55",
		AttachmentID: "photo-1",
	}, io.Discard)
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if got.ID != "photo-1" {
		t.Fatalf("attachment id = %q, want photo-1", got.ID)
	}
	if primary.downloadCalls != 1 {
		t.Fatalf("primary calls = %d, want 1", primary.downloadCalls)
	}
	if secondary.downloadCalls != 0 {
		t.Fatalf("secondary calls = %d, want 0", secondary.downloadCalls)
	}
}

func TestMediaDownloaderRejectsUnknownSource(t *testing.T) {
	t.Parallel()

	downloader, err := NewMediaDownloader([]Runtime{
		{
			Source: platform.EventSource{
				Platform: testDriverPlatform,
				ID:       "main",
			},
			MediaDownloader: &stubMediaDownloader{},
		},
	})
	if err != nil {
		t.Fatalf("new media downloader failed: %v", err)
	}

	_, err = downloader.Download(context.Background(), platform.MediaDownloadRequest{
		Source: platform.EventSource{
			Platform: testDriverPlatform,
			ID:       "missing",
		},
		Conversation: platform.Conversation{
			ID:   "1",
			Type: platform.ConversationTypeGroup,
		},
		ArticleID:    "55",
		AttachmentID: "photo-1",
	}, io.Discard)
	if !errors.Is(err, platform.ErrMediaDownloadUnsupported) {
		t.Fatalf("download error = %v, want %v", err, platform.ErrMediaDownloadUnsupported)
	}
}

func TestSinkDispatcherAmbiguousPlatform(t *testing.T) {
	t.Parallel()

	dispatcher, err := NewSinkDispatcher([]Runtime{
		{
			Source:         platform.EventSource{Platform: testDriverPlatform, ID: "main"},
			SinkDispatcher: &stubSinkDispatcher{},
		},
		{
			Source:         platform.EventSource{Platform: testDriverPlatform, ID: "alt"},
			SinkDispatcher: &stubSinkDispatcher{},
		},
	})
	if err != nil {
		t.Fatalf("new sink dispatcher failed: %v", err)
	}

	_, err = dispatcher.SendMessage(context.Background(), platform.SendMessageRequest{
		Target: platform.OutboundTarget{
			Conversation: platform.Conversation{ID: "1", Type: platform.ConversationTypeGroup},
			Sink: &platform.EventSink{
				Platform: testDriverPlatform,
			},
		},
		Text: "hello",
	})
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
}

func TestSinkDispatcherRouteEditPreservesOutboundError(t *testing.T) {
	t.Parallel()

	rootCause := errors.New("rpc flood wait")
	routedErr := &platform.OutboundError{
		Operation:  platform.OutboundOperationEditMessage,
		Kind:       platform.OutboundErrorKindRateLimited,
		Platform:   testDriverPlatform,
		SinkID:     "main",
		RetryAfter: 5 * time.Second,
		Cause:      rootCause,
	}

	dispatcher, err := NewSinkDispatcher([]Runtime{
		{
			Source: platform.EventSource{
				Platform: testDriverPlatform,
				ID:       "main",
			},
			SinkDispatcher: &stubSinkDispatcher{editErr: routedErr},
		},
	})
	if err != nil {
		t.Fatalf("new sink dispatcher failed: %v", err)
	}

	err = dispatcher.EditMessage(context.Background(), platform.EditMessageRequest{
		Target: platform.OutboundTarget{
			Conversation: platform.Conversation{ID: "1", Type: platform.ConversationTypeGroup},
			Sink:         &platform.EventSink{ID: "main"},
		},
		MessageID: "9",
		Text:      "updated",
	})
	if err == nil {
		t.Fatal("expected routed edit error")
	}

	outboundErr, ok := platform.AsOutboundError(err)
	if !ok {
		t.Fatalf("AsOutboundError(%v) = false, want true", err)
	}
	if outboundErr.Kind != platform.OutboundErrorKindRateLimited {
		t.Fatalf("kind = %s, want %s", outboundErr.Kind, platform.OutboundErrorKindRateLimited)
	}
	if outboundErr.Operation != platform.OutboundOperationEditMessage {
		t.Fatalf("operation = %s, want %s", outboundErr.Operation, platform.OutboundOperationEditMessage)
	}
	if !errors.Is(err, rootCause) {
		t.Fatalf("errors.Is(err, rootCause) = false, want true (err=%v)", err)
	}
}

func TestSinkDispatcherListSinksByPlatform(t *testing.T) {
	t.Parallel()

	dispatcher, err := NewSinkDispatcher([]Runtime{
		{
			Source:         platform.EventSource{Platform: testDriverPlatform, ID: "main"},
			SinkDispatcher: &stubSinkDispatcher{},
		},
	})
	if err != nil {
		t.Fatalf("new sink dispatcher failed: %v", err)
	}

	sinks, err := dispatcher.ListSinksByPlatform(context.Background(), testDriverPlatform)
	if err != nil {
		t.Fatalf("list sinks by platform failed: %v", err)
	}
	if len(sinks) != 1 {
		t.Fatalf("sinks len = %d, want 1", len(sinks))
	}
	if sinks[0].ID != "main" {
		t.Fatalf("sink id = %s, want main", sinks[0].ID)
	}
}

type stubDriver struct {
	name string
}

func (d stubDriver) Name() string {
	return d.name
}

func (d stubDriver) Start(_ context.Context, _ core.EventDispatcher) error {
	return nil
}

func (d stubDriver) Shutdown(_ context.Context) error {
	return nil
}

type publishingDriver struct {
	event *platform.Event
}

func (d publishingDriver) Name() string {
	return "publishing"
}

func (d publishingDriver) Start(ctx context.Context, dispatcher core.EventDispatcher) error {
	if d.event == nil {
		return nil
	}
	if err := dispatcher.Publish(ctx, d.event); err != nil {
		return fmt.Errorf("publishing driver publish: %w", err)
	}

	return nil
}

func (d publishingDriver) Shutdown(context.Context) error {
	return nil
}

type captureEventDispatcher struct {
	events []*platform.Event
}

func (d *captureEventDispatcher) Publish(_ context.Context, event *platform.Event) error {
	d.events = append(d.events, event)
	return nil
}

type stubSinkDispatcher struct {
	sendCalls int
	sendErr   error
	editErr   error
}

type stubMediaDownloader struct {
	downloadCalls int
	attachment    platform.MediaAttachment
	err           error
}

func (d *stubMediaDownloader) Download(
	_ context.Context,
	_ platform.MediaDownloadRequest,
	_ io.Writer,
) (platform.MediaAttachment, error) {
	d.downloadCalls++
	if d.err != nil {
		return platform.MediaAttachment{}, d.err
	}

	return d.attachment, nil
}

func (d *stubSinkDispatcher) SendMessage(
	_ context.Context,
	request platform.SendMessageRequest,
) (*platform.OutboundMessage, error) {
	d.sendCalls++
	if d.sendErr != nil {
		return nil, d.sendErr
	}

	return &platform.OutboundMessage{
		ID: "1",
		Target: platform.OutboundTarget{
			Conversation: request.Target.Conversation,
			Sink:         request.Target.Sink,
		},
	}, nil
}

func (d *stubSinkDispatcher) EditMessage(context.Context, platform.EditMessageRequest) error {
	return d.editErr
}

func (*stubSinkDispatcher) DeleteMessage(context.Context, platform.DeleteMessageRequest) error {
	return nil
}

func (*stubSinkDispatcher) SetReaction(context.Context, platform.SetReactionRequest) error {
	return nil
}

func (*stubSinkDispatcher) ListSinks(context.Context) ([]platform.EventSink, error) {
	return nil, nil
}

func (*stubSinkDispatcher) ListSinksByPlatform(
	context.Context,
	platform.Platform,
) ([]platform.EventSink, error) {
	return nil, nil
}

func stubRuntimeBuilder(_ context.Context, definition Definition, _ *slog.Logger) (Runtime, error) {
	return Runtime{
		Source: platform.EventSource{
			Platform: testDriverPlatform,
		},
		Driver: stubDriver{name: definition.Name},
	}, nil
}

func ensureNoContextCancellationError(err error) bool {
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

func TestSinkDispatcherListSinksHonorsContext(t *testing.T) {
	t.Parallel()

	dispatcher, err := NewSinkDispatcher(nil)
	if err != nil {
		t.Fatalf("new sink dispatcher failed: %v", err)
	}

	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
	cancel()
	_, err = dispatcher.ListSinks(ctx)
	if err == nil {
		t.Fatal("expected context error")
	}
	if ensureNoContextCancellationError(err) {
		t.Fatalf("error = %v, expected context cancellation", err)
	}
}
