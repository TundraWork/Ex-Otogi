package main

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/gemini"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/management"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// The models of each language model provider, as last fetched when an
// operator asked, are kept in the module store under the llm section, one
// key per provider: the panel offers them for the settings that name a
// model. They are fetched only when asked, never on a timer or at start,
// and a provider removed from the section, or renamed, loses its list.

// modelsKey starts the key of a provider's list.
const modelsKey = "models/"

// modelList is a provider's models as last fetched.
type modelList struct {
	At     time.Time   `json:"at"`
	By     string      `json:"by"`
	Models []llm.Model `json:"models"`
}

func (a systemAPI) Providers(ctx context.Context) ([]management.LLMProvider, error) {
	out := []management.LLMProvider{}
	for _, name := range slices.Sorted(maps.Keys(*a.settings.system.providers.Load())) {
		p, err := a.settings.savedModels(ctx, name)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (a systemAPI) FetchModels(ctx context.Context, name, by string) (management.LLMProvider, error) {
	return a.settings.fetchModels(ctx, name, by)
}

// providerOf is a provider by name, as the API shows it, without models.
func providerOf(name string) management.LLMProvider {
	kind := "openai"
	if name == gemini.Name {
		kind = "gemini"
	}
	return management.LLMProvider{Name: name, Kind: kind, Models: []llm.Model{}}
}

// savedModels is a provider with the list last fetched from it, if any.
func (s *moduleSettings) savedModels(ctx context.Context, name string) (management.LLMProvider, error) {
	out := providerOf(name)
	data, ok, err := s.store.Store(llmSection).Get(ctx, modelsKey+name)
	if err != nil || !ok {
		return out, err
	}
	var saved modelList
	if err := json.Unmarshal(data, &saved); err != nil {
		return out, fmt.Errorf("the saved models of %s: %w", name, err)
	}
	out.FetchedAt, out.FetchedBy, out.Models = saved.At, saved.By, saved.Models
	return out, nil
}

// fetchModels asks a provider for its models, records the fetch, and saves
// the list in place of the last one. A failed fetch leaves the last list.
func (s *moduleSettings) fetchModels(ctx context.Context, name, by string) (management.LLMProvider, error) {
	p, ok := (*s.system.providers.Load())[name]
	if !ok {
		return management.LLMProvider{}, fmt.Errorf("provider %q is not among the language model providers set: %w", name, management.ErrNotFound)
	}
	lister, ok := p.(llm.ModelLister)
	if !ok {
		return management.LLMProvider{}, fmt.Errorf("provider %q cannot list its models: %w", name, otogi.ErrUnsupported)
	}
	started := time.Now()
	models, err := lister.ListModels(ctx)
	s.modelsFetched(ctx, name, by, started, models, err)
	if err != nil {
		return management.LLMProvider{}, &management.UpstreamError{Err: err}
	}
	saved := modelList{At: time.Now(), By: by, Models: models}
	data, err := json.Marshal(saved)
	if err != nil {
		return management.LLMProvider{}, fmt.Errorf("encode the models of %s: %w", name, err)
	}
	// A save of the section that removed the provider meanwhile has
	// deleted its list: one fetched before must not come back.
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := (*s.system.providers.Load())[name]; !ok {
		return management.LLMProvider{}, fmt.Errorf("provider %q was removed while its models were fetched: %w", name, management.ErrNotFound)
	}
	if err := s.store.Store(llmSection).Put(ctx, modelsKey+name, data); err != nil {
		return management.LLMProvider{}, err
	}
	out := providerOf(name)
	out.FetchedAt, out.FetchedBy, out.Models = saved.At, saved.By, saved.Models
	return out, nil
}

// modelsFetched logs and records a fetch of a provider's models: the list,
// or the failure with the HTTP status the provider answered.
func (s *moduleSettings) modelsFetched(ctx context.Context, name, by string, started time.Time, models []llm.Model, err error) {
	took := time.Since(started)
	payload := map[string]any{"provider": name, "by": by}
	outcome := "ok"
	if err != nil {
		outcome, payload["error"] = "error", err.Error()
		s.log.Warn("llm models: fetch failed", "provider", name, "by", by, "took", took.Round(time.Millisecond).String(), "error", err)
	} else {
		payload["models"] = models
		s.log.Info("llm models: fetched", "provider", name, "by", by, "took", took.Round(time.Millisecond).String(), "models", len(models))
	}
	s.recorder.Record(ctx, otogi.Record{
		At: time.Now(), Kind: "llm.models", Component: "llm:" + name, Outcome: outcome, Duration: took, Payload: payload,
	})
}

// forgetModels deletes the lists of the providers the llm section no longer
// sets, once a save has applied it. A failure is logged: the next save that
// leaves the provider out deletes its list.
func (s *moduleSettings) forgetModels(ctx context.Context) {
	store := s.store.Store(llmSection)
	keys, err := store.List(ctx, modelsKey)
	if err != nil {
		s.log.Error("llm models: list the saved lists", "error", err)
		return
	}
	providers := *s.system.providers.Load()
	for _, key := range keys {
		if _, ok := providers[strings.TrimPrefix(key, modelsKey)]; ok {
			continue
		}
		if err := store.Delete(ctx, key); err != nil {
			s.log.Error("llm models: delete the list of a provider removed", "key", key, "error", err)
		}
	}
}
