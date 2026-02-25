package llmchat

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"text/template"
	"time"

	"ex-otogi/pkg/otogi"
)

func (m *Module) buildGenerateRequest(
	ctx context.Context,
	event *otogi.Event,
	agent Agent,
	currentPrompt string,
) (otogi.LLMGenerateRequest, error) {
	if event == nil {
		return otogi.LLMGenerateRequest{}, fmt.Errorf("build llm request: nil event")
	}
	if m.memory == nil {
		return otogi.LLMGenerateRequest{}, fmt.Errorf("build llm request: memory service unavailable")
	}

	chain, err := m.memory.GetReplyChain(ctx, event)
	if err != nil {
		return otogi.LLMGenerateRequest{}, fmt.Errorf("build llm request get reply chain: %w", err)
	}
	if len(chain) == 0 {
		return otogi.LLMGenerateRequest{}, fmt.Errorf("build llm request: empty reply chain")
	}
	chain[len(chain)-1].Article.Text = currentPrompt

	renderedSystemPrompt, err := renderSystemPrompt(agent, event, m.now())
	if err != nil {
		return otogi.LLMGenerateRequest{}, fmt.Errorf("build llm request render system prompt: %w", err)
	}

	messages := make([]otogi.LLMMessage, 0, len(chain)+1)
	messages = append(messages, otogi.LLMMessage{
		Role:    otogi.LLMMessageRoleSystem,
		Content: renderedSystemPrompt,
	})

	for _, entry := range chain {
		formatted := formatReplyChainEntry(entry)
		if formatted == "" {
			continue
		}

		role := otogi.LLMMessageRoleUser
		if entry.Actor.IsBot {
			role = otogi.LLMMessageRoleAssistant
		}
		messages = append(messages, otogi.LLMMessage{
			Role:    role,
			Content: formatted,
		})
	}

	req := otogi.LLMGenerateRequest{
		Model:           agent.Model,
		Messages:        messages,
		MaxOutputTokens: agent.MaxOutputTokens,
		Temperature:     agent.Temperature,
		Metadata: map[string]string{
			"agent":           agent.Name,
			"provider":        agent.Provider,
			"conversation_id": event.Conversation.ID,
		},
	}
	if err := req.Validate(); err != nil {
		return otogi.LLMGenerateRequest{}, fmt.Errorf("build llm request validate: %w", err)
	}

	return req, nil
}

func formatReplyChainEntry(entry otogi.ReplyChainEntry) string {
	text := strings.TrimSpace(entry.Article.Text)
	if text == "" {
		return ""
	}

	speaker := speakerLabel(entry.Actor)
	return speaker + ": " + text
}

func speakerLabel(actor otogi.Actor) string {
	if name := strings.TrimSpace(actor.Username); name != "" {
		return name
	}
	if name := strings.TrimSpace(actor.DisplayName); name != "" {
		return name
	}
	if id := strings.TrimSpace(actor.ID); id != "" {
		return id
	}

	return "unknown"
}

func renderSystemPrompt(agent Agent, event *otogi.Event, now time.Time) (string, error) {
	tmpl, err := template.New("system_prompt").Option("missingkey=error").Parse(agent.SystemPromptTemplate)
	if err != nil {
		return "", fmt.Errorf("parse system prompt template: %w", err)
	}

	now = now.UTC()
	data := map[string]any{
		"Now":                 now,
		"NowRFC3339":          now.Format(time.RFC3339),
		"DateTimeUTC":         now.Format(time.RFC3339),
		"DateUTC":             now.Format("2006-01-02"),
		"TimeUTC":             now.Format("15:04:05"),
		"Unix":                now.Unix(),
		"AgentName":           agent.Name,
		"AgentDescription":    agent.Description,
		"ConversationID":      event.Conversation.ID,
		"ConversationTitle":   event.Conversation.Title,
		"ActorUsername":       event.Actor.Username,
		"ActorDisplayName":    event.Actor.DisplayName,
		"ActorID":             event.Actor.ID,
		"ActorIsBot":          event.Actor.IsBot,
		"TemplateVariables":   cloneStringMap(agent.TemplateVariables),
		"EventConversationID": event.Conversation.ID,
	}
	for key, value := range agent.TemplateVariables {
		data[key] = value
	}

	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, data); err != nil {
		return "", fmt.Errorf("execute system prompt template: %w", err)
	}

	result := strings.TrimSpace(rendered.String())
	if result == "" {
		return "", fmt.Errorf("rendered system prompt is empty")
	}

	return result, nil
}
