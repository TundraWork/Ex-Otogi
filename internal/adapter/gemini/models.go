package gemini

import (
	"context"
	"slices"
	"strings"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
)

var _ llm.ModelLister = (*Provider)(nil)

// ListModels lists the models that generate content, every page of
// GET models, with the key and endpoint calls use; see llm.ModelLister.
// Gemini says whether a model thinks, not at which levels, so
// ThinkingLevels stays empty, and its list has no input modalities.
func (p *Provider) ListModels(ctx context.Context) ([]llm.Model, error) {
	var xs exchanges
	ctx = context.WithValue(ctx, exchangesKey{}, &xs)
	out := []llm.Model{}
	for m, err := range p.client.Models.All(ctx) {
		if err != nil {
			return nil, xs.failure(ctx, err)
		}
		if !slices.Contains(m.SupportedActions, "generateContent") {
			continue
		}
		// Calls name a model without the resource prefix.
		out = append(out, llm.Model{
			ID: strings.TrimPrefix(m.Name, "models/"), DisplayName: m.DisplayName,
			InputTokens: int(m.InputTokenLimit), OutputTokens: int(m.OutputTokenLimit),
		})
	}
	return out, nil
}
