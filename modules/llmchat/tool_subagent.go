package llmchat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"text/template"
	"time"

	"ex-otogi/pkg/otogi/ai"
)

// subAgentTool implements ToolHandler by making a standalone LLM call with
// provider-native tools enabled (via request metadata) and no function tools.
// This works around the provider limitation that forbids tool_use alongside
// internal tools like Google Search or URL Context.
type subAgentTool struct {
	cfg      SubAgentConfig
	provider ai.LLMProvider
	logger   *slog.Logger
	tmpl     *template.Template
}

// newSubAgentTool creates one sub-agent tool handler. The prompt template is
// pre-compiled so Execute never fails on template parse errors.
func newSubAgentTool(cfg SubAgentConfig, provider ai.LLMProvider, logger *slog.Logger) (ToolHandler, error) {
	if provider == nil {
		return nil, fmt.Errorf("new sub-agent tool %s: nil provider", cfg.Name)
	}
	if strings.TrimSpace(cfg.Name) == "" {
		return nil, fmt.Errorf("new sub-agent tool: empty name")
	}
	if strings.TrimSpace(cfg.PromptTemplate) == "" {
		return nil, fmt.Errorf("new sub-agent tool %s: empty prompt template", cfg.Name)
	}

	tmpl, err := template.New("sub-agent-prompt").Option("missingkey=error").Parse(cfg.PromptTemplate)
	if err != nil {
		return nil, fmt.Errorf("new sub-agent tool %s: parse prompt template: %w", cfg.Name, err)
	}

	return &subAgentTool{
		cfg:      cfg,
		provider: provider,
		logger:   logger,
		tmpl:     tmpl,
	}, nil
}

// Name returns the sub-agent tool's unique identifier.
func (t *subAgentTool) Name() string {
	return t.cfg.Name
}

// Definition returns the LLM tool definition exposed to the parent model.
func (t *subAgentTool) Definition() ai.LLMToolDefinition {
	return ai.LLMToolDefinition{
		Name:        t.cfg.Name,
		Description: t.cfg.Description,
		Parameters:  append(json.RawMessage(nil), t.cfg.Parameters...),
	}
}

// Execute runs the sub-agent: renders the prompt template with the tool call
// arguments, makes a streaming LLM call with no function tools, and returns
// the collected response text.
func (t *subAgentTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("sub-agent tool %s: nil context", t.cfg.Name)
	}
	if t.provider == nil {
		return "", fmt.Errorf("sub-agent tool %s: nil provider", t.cfg.Name)
	}

	var argsMap map[string]any
	if err := json.Unmarshal(args, &argsMap); err != nil {
		return "", fmt.Errorf("sub-agent tool %s: decode arguments: %w", t.cfg.Name, err)
	}

	var promptBuf bytes.Buffer
	if err := t.tmpl.Execute(&promptBuf, argsMap); err != nil {
		return "", fmt.Errorf("sub-agent tool %s: render prompt template: %w", t.cfg.Name, err)
	}
	userPrompt := strings.TrimSpace(promptBuf.String())
	if userPrompt == "" {
		return "", fmt.Errorf("sub-agent tool %s: rendered prompt is empty", t.cfg.Name)
	}

	req := ai.LLMGenerateRequest{
		Model: t.cfg.Model,
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleSystem, Content: t.cfg.SystemPrompt},
			{Role: ai.LLMMessageRoleUser, Content: userPrompt},
		},
		MaxOutputTokens: t.cfg.MaxOutputTokens,
		Temperature:     t.cfg.Temperature,
		Metadata:        cloneStringMap(t.cfg.RequestMetadata),
		Tools:           nil, // No function tools — allows provider-native tools.
	}

	t.debugStart(ctx, userPrompt)
	start := time.Now()

	stream, err := t.provider.GenerateStream(ctx, req)
	if err != nil {
		return "", fmt.Errorf("sub-agent tool %s: generate stream: %w", t.cfg.Name, err)
	}

	result, err := collectStreamText(ctx, stream)
	closeErr := stream.Close()
	if err != nil {
		if closeErr != nil {
			err = errors.Join(err, fmt.Errorf("sub-agent tool %s: close stream: %w", t.cfg.Name, closeErr))
		}
		return "", err
	}
	if closeErr != nil {
		return "", fmt.Errorf("sub-agent tool %s: close stream: %w", t.cfg.Name, closeErr)
	}

	result = strings.TrimSpace(result)
	if result == "" {
		return "", fmt.Errorf("sub-agent tool %s: empty response from provider", t.cfg.Name)
	}

	t.debugEnd(ctx, start, len(result))

	return result, nil
}

// collectStreamText reads all chunks from a stream, concatenating output text
// deltas. Thinking and tool call chunks are skipped.
func collectStreamText(ctx context.Context, stream ai.LLMStream) (string, error) {
	var builder strings.Builder
	for {
		chunk, err := stream.Recv(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", fmt.Errorf("collect stream text: %w", err)
		}

		switch chunk.Kind.Normalize() {
		case ai.LLMGenerateChunkKindOutputText:
			builder.WriteString(chunk.Delta)
		case ai.LLMGenerateChunkKindThinkingSummary,
			ai.LLMGenerateChunkKindToolCall:
			continue
		default:
			builder.WriteString(chunk.Delta)
		}
	}

	return builder.String(), nil
}

func (t *subAgentTool) debugStart(ctx context.Context, prompt string) {
	if t.logger == nil {
		return
	}

	t.logger.DebugContext(ctx, "sub-agent tool execute start",
		"sub_agent", t.cfg.Name,
		"model", t.cfg.Model,
		"prompt_runes", len([]rune(prompt)),
	)
}

func (t *subAgentTool) debugEnd(ctx context.Context, start time.Time, resultLength int) {
	if t.logger == nil {
		return
	}

	t.logger.DebugContext(ctx, "sub-agent tool execute end",
		"sub_agent", t.cfg.Name,
		"model", t.cfg.Model,
		"elapsed", time.Since(start),
		"result_length", resultLength,
	)
}

var _ ToolHandler = (*subAgentTool)(nil)
