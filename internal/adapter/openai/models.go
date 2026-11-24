package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

var _ llm.ModelLister = (*Provider)(nil)

// modelList is GET <base>/models. Every field but the ID is optional:
// DeepSeek adds name, context_window, max_output_tokens, input_modalities,
// and effort; OpenAI's own list has none of them.
type modelList struct {
	Data []struct {
		ID              string   `json:"id"`
		Name            string   `json:"name"`
		ContextWindow   int      `json:"context_window"`
		MaxOutputTokens int      `json:"max_output_tokens"`
		InputModalities []string `json:"input_modalities"`
		Effort          struct {
			SupportedLevels []string `json:"supported_levels"`
		} `json:"effort"`
	} `json:"data"`
}

// ListModels lists the service's models, GET <base>/models with the key
// calls use; see llm.ModelLister. The list is one page.
func (p *Provider) ListModels(ctx context.Context) ([]llm.Model, error) {
	url := strings.TrimSuffix(p.endpoint, "/chat/completions") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("openai %s: %w: %w", p.name, otogi.ErrInvalid, err)
	}
	req.Header.Set("Authorization", "Bearer "+p.key)
	res, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai %s: GET %s: %w: %w", p.name, url, otogi.ErrUnavailable, err)
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("openai %s: GET %s: read: %w: %w", p.name, url, otogi.ErrUnavailable, err)
	}
	if res.StatusCode >= 300 {
		return nil, statusError(p.name, res.StatusCode, data)
	}
	var list modelList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("openai %s: GET %s: not a list of models: %s: %w", p.name, url, data, otogi.ErrInvalid)
	}
	out := make([]llm.Model, 0, len(list.Data))
	for _, m := range list.Data {
		out = append(out, llm.Model{
			ID: m.ID, DisplayName: m.Name, ContextTokens: m.ContextWindow, OutputTokens: m.MaxOutputTokens,
			InputModalities: m.InputModalities, ThinkingLevels: m.Effort.SupportedLevels,
		})
	}
	return out, nil
}
