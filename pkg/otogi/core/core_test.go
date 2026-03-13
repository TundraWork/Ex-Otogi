package core

import (
	"encoding/json"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/platform"
)

type testConfigRegistry struct {
	values map[string]json.RawMessage
}

func (r *testConfigRegistry) Register(moduleName string, raw json.RawMessage) error {
	if r.values == nil {
		r.values = make(map[string]json.RawMessage)
	}
	r.values[moduleName] = raw
	return nil
}

func (r *testConfigRegistry) Resolve(moduleName string) (json.RawMessage, error) {
	return r.values[moduleName], nil
}

func TestRuntimeHelpers(t *testing.T) {
	t.Run("default subscription spec", func(t *testing.T) {
		spec := NewDefaultSubscriptionSpec("worker")
		if spec.Name != "worker" {
			t.Fatalf("spec.Name = %q, want %q", spec.Name, "worker")
		}
	})

	t.Run("parse module config", func(t *testing.T) {
		registry := &testConfigRegistry{
			values: map[string]json.RawMessage{
				"demo": json.RawMessage(`{"enabled":true}`),
			},
		}

		type config struct {
			Enabled bool `json:"enabled"`
		}

		cfg, err := ParseModuleConfig[config](registry, "demo")
		if err != nil {
			t.Fatalf("ParseModuleConfig() error = %v", err)
		}
		if !cfg.Enabled {
			t.Fatal("cfg.Enabled = false, want true")
		}
	})

	t.Run("memory lookup from event", func(t *testing.T) {
		event := &platform.Event{
			Kind: platform.EventKindArticleCreated,
			Source: platform.EventSource{
				Platform: platform.PlatformTelegram,
				ID:       "telegram-primary",
			},
			Conversation: platform.Conversation{
				ID:   "chat-1",
				Type: platform.ConversationTypeGroup,
			},
			Article: &platform.Article{
				ID: "article-1",
			},
			OccurredAt: time.Now().UTC(),
		}

		lookup, err := MemoryLookupFromEvent(event)
		if err != nil {
			t.Fatalf("MemoryLookupFromEvent() error = %v", err)
		}
		if lookup.ArticleID != "article-1" {
			t.Fatalf("lookup.ArticleID = %q, want %q", lookup.ArticleID, "article-1")
		}
	})
}
