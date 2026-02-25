package otogi

import (
	"context"
	"fmt"
	"strings"
)

// ServiceLLMProviderRegistry is the canonical service registry key for LLM providers.
const ServiceLLMProviderRegistry = "otogi.llm_provider_registry"

// LLMProviderRegistry resolves LLM providers by stable provider name.
//
// Implementations must be concurrency-safe because modules can resolve providers
// from multiple workers at the same time.
type LLMProviderRegistry interface {
	// Resolve returns one configured provider by name.
	Resolve(provider string) (LLMProvider, error)
}

// LLMProvider exposes one stream-first LLM text generation operation.
//
// Implementations should keep provider-specific transport details hidden behind
// this neutral interface.
type LLMProvider interface {
	// GenerateStream starts one streaming generation request.
	GenerateStream(ctx context.Context, req LLMGenerateRequest) (LLMStream, error)
}

// LLMStream is a pull-based stream of generated text chunks.
type LLMStream interface {
	// Recv returns the next generated chunk.
	//
	// io.EOF should be returned when the stream completes normally.
	Recv(ctx context.Context) (LLMGenerateChunk, error)
	// Close releases provider-side resources for this stream.
	Close() error
}

// LLMMessageRole identifies one message role in a multi-turn LLM request.
type LLMMessageRole string

const (
	// LLMMessageRoleSystem identifies system-level instructions.
	LLMMessageRoleSystem LLMMessageRole = "system"
	// LLMMessageRoleUser identifies user-authored conversational turns.
	LLMMessageRoleUser LLMMessageRole = "user"
	// LLMMessageRoleAssistant identifies assistant-authored conversational turns.
	LLMMessageRoleAssistant LLMMessageRole = "assistant"
)

// Validate checks whether this role value is supported.
func (r LLMMessageRole) Validate() error {
	switch r {
	case LLMMessageRoleSystem, LLMMessageRoleUser, LLMMessageRoleAssistant:
		return nil
	default:
		return fmt.Errorf("validate llm message role: unsupported role %q", r)
	}
}

// LLMMessage is one ordered message entry in one generation request.
type LLMMessage struct {
	// Role identifies which side of the conversation this message belongs to.
	Role LLMMessageRole
	// Content is one plain text message body.
	Content string
}

// Validate checks one message contract.
func (m LLMMessage) Validate() error {
	if err := m.Role.Validate(); err != nil {
		return fmt.Errorf("validate llm message: %w", err)
	}
	if strings.TrimSpace(m.Content) == "" {
		return fmt.Errorf("validate llm message: missing content")
	}

	return nil
}

// LLMGenerateRequest describes one provider generation call.
type LLMGenerateRequest struct {
	// Model identifies which provider model should be used.
	Model string
	// Messages is the ordered conversation context sent to the provider.
	Messages []LLMMessage
	// MaxOutputTokens optionally bounds generated output token count.
	MaxOutputTokens int
	// Temperature optionally controls output randomness.
	Temperature float64
	// Metadata carries optional provider-agnostic context.
	Metadata map[string]string
}

// Validate checks one generation request contract.
func (r LLMGenerateRequest) Validate() error {
	if strings.TrimSpace(r.Model) == "" {
		return fmt.Errorf("validate llm generate request: missing model")
	}
	if len(r.Messages) == 0 {
		return fmt.Errorf("validate llm generate request: missing messages")
	}
	for index, message := range r.Messages {
		if err := message.Validate(); err != nil {
			return fmt.Errorf("validate llm generate request messages[%d]: %w", index, err)
		}
	}
	if r.MaxOutputTokens < 0 {
		return fmt.Errorf("validate llm generate request: max_output_tokens must be >= 0")
	}
	if r.Temperature < 0 {
		return fmt.Errorf("validate llm generate request: temperature must be >= 0")
	}

	return nil
}

// LLMGenerateChunk carries incremental text from one stream.
type LLMGenerateChunk struct {
	// Delta is the newly generated text segment.
	Delta string
}
