package llmchat

import (
	"context"
	"fmt"
	"strings"

	"ex-otogi/pkg/otogi/platform"
)

const (
	articleTagFrameworkModule = "platform.module"
	articleTagAgent           = "llmchat.agent"
)

func llmchatArticleTags(agent Agent) map[string]string {
	if strings.TrimSpace(agent.Name) == "" {
		return nil
	}

	return map[string]string{
		articleTagFrameworkModule: "llmchat",
		articleTagAgent:           agent.Name,
	}
}

func isLLMChatTaggedArticle(article platform.Article) bool {
	return strings.EqualFold(strings.TrimSpace(article.Tags[articleTagFrameworkModule]), "llmchat") &&
		strings.TrimSpace(article.Tags[articleTagAgent]) != ""
}

func (m *Module) resolveTriggeredAgent(
	ctx context.Context,
	event *platform.Event,
) (Agent, string, bool, error) {
	if event == nil || event.Article == nil {
		return Agent{}, "", false, nil
	}

	if agent, prompt, matched := m.matchTriggeredAgent(event.Article.Text); matched {
		return agent, prompt, true, nil
	}
	if m.memory == nil {
		return Agent{}, "", false, nil
	}

	replied, found, err := m.memory.GetReplied(ctx, event)
	if err != nil {
		return Agent{}, "", false, fmt.Errorf("get replied article: %w", err)
	}
	if !found || !isLLMChatTaggedArticle(replied.Article) {
		return Agent{}, "", false, nil
	}

	agent, configured := m.findConfiguredAgent(replied.Article.Tags[articleTagAgent])
	if !configured {
		return Agent{}, "", false, nil
	}

	return agent, strings.TrimSpace(event.Article.Text), true, nil
}

func (m *Module) findConfiguredAgent(agentName string) (Agent, bool) {
	normalized := normalizeAgentName(agentName)
	if normalized == "" {
		return Agent{}, false
	}

	for _, agent := range m.cfg.Agents {
		if normalizeAgentName(agent.Name) == normalized {
			return agent, true
		}
	}

	return Agent{}, false
}
