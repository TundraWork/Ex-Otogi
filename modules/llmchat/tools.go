package llmchat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"ex-otogi/pkg/otogi/ai"
)

// ToolHandler executes one named tool and returns one text result.
type ToolHandler interface {
	// Name returns the tool's unique identifier.
	Name() string
	// Definition returns the LLM tool definition exposed to the model.
	Definition() ai.LLMToolDefinition
	// Execute runs the tool with JSON arguments and returns one result string.
	Execute(ctx context.Context, args json.RawMessage) (string, error)
}

// ToolRegistry holds tool handlers available for one agent request.
type ToolRegistry struct {
	handlers map[string]ToolHandler
	order    []string
}

// accumulatedToolCall collects streamed tool call chunks into one complete call.
type accumulatedToolCall struct {
	ID               string
	Name             string
	Arguments        strings.Builder
	ThoughtSignature []byte
}

// NewToolRegistry builds one request-scoped tool registry.
func NewToolRegistry(handlers []ToolHandler) *ToolRegistry {
	registry := &ToolRegistry{
		handlers: make(map[string]ToolHandler, len(handlers)),
		order:    make([]string, 0, len(handlers)),
	}
	for _, handler := range handlers {
		if handler == nil {
			continue
		}

		name := strings.TrimSpace(handler.Name())
		if name == "" {
			continue
		}
		if _, exists := registry.handlers[name]; exists {
			continue
		}

		registry.handlers[name] = handler
		registry.order = append(registry.order, name)
	}

	return registry
}

// HasTools reports whether the registry exposes at least one tool.
func (r *ToolRegistry) HasTools() bool {
	return r != nil && len(r.order) > 0
}

// Definitions returns tool definitions in registration order.
func (r *ToolRegistry) Definitions() []ai.LLMToolDefinition {
	if !r.HasTools() {
		return nil
	}

	definitions := make([]ai.LLMToolDefinition, 0, len(r.order))
	for _, name := range r.order {
		definition := r.handlers[name].Definition()
		if len(definition.Parameters) > 0 {
			definition.Parameters = append(json.RawMessage(nil), definition.Parameters...)
		}
		definitions = append(definitions, definition)
	}

	return definitions
}

// Execute runs one named tool with JSON arguments.
func (r *ToolRegistry) Execute(ctx context.Context, name string, args json.RawMessage) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("tool registry execute: nil context")
	}
	if !r.HasTools() {
		return "", fmt.Errorf("tool registry execute %s: no tools registered", strings.TrimSpace(name))
	}

	handler, exists := r.handlers[strings.TrimSpace(name)]
	if !exists || handler == nil {
		return "", fmt.Errorf("tool registry execute %s: tool not found", strings.TrimSpace(name))
	}

	result, err := handler.Execute(ctx, append(json.RawMessage(nil), args...))
	if err != nil {
		return "", fmt.Errorf("tool registry execute %s: %w", strings.TrimSpace(name), err)
	}

	return result, nil
}

func (c *accumulatedToolCall) ToolCall() (ai.LLMToolCall, error) {
	call := ai.LLMToolCall{
		ID:               c.ID,
		Name:             c.Name,
		Arguments:        c.Arguments.String(),
		ThoughtSignature: c.ThoughtSignature,
	}
	if err := call.Validate(); err != nil {
		return ai.LLMToolCall{}, fmt.Errorf("finalize tool call %s: %w", strings.TrimSpace(c.ID), err)
	}

	return call, nil
}
