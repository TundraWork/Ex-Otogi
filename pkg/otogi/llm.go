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
	//
	// This legacy shorthand remains supported for text-only callers. When Parts is
	// non-empty, Content must be empty.
	Content string
	// Parts is an optional structured content list for multimodal requests.
	//
	// Text-only callers can continue using Content. Multimodal callers should
	// prefer Parts so providers can preserve input ordering.
	Parts []LLMMessagePart
}

// Validate checks one message contract.
func (m LLMMessage) Validate() error {
	if err := m.Role.Validate(); err != nil {
		return fmt.Errorf("validate llm message: %w", err)
	}
	if len(m.Parts) > 0 {
		if strings.TrimSpace(m.Content) != "" {
			return fmt.Errorf("validate llm message: content and parts are mutually exclusive")
		}
		for index, part := range m.Parts {
			if err := part.Validate(); err != nil {
				return fmt.Errorf("validate llm message parts[%d]: %w", index, err)
			}
		}
		return nil
	}
	if strings.TrimSpace(m.Content) == "" {
		return fmt.Errorf("validate llm message: missing content")
	}

	return nil
}

// ContentParts returns message parts, preserving text-only callers as one text part.
func (m LLMMessage) ContentParts() []LLMMessagePart {
	if len(m.Parts) > 0 {
		parts := make([]LLMMessagePart, 0, len(m.Parts))
		for _, part := range m.Parts {
			parts = append(parts, part.clone())
		}
		return parts
	}
	if strings.TrimSpace(m.Content) == "" {
		return nil
	}

	return []LLMMessagePart{{
		Type: LLMMessagePartTypeText,
		Text: m.Content,
	}}
}

// LLMMessagePartType identifies one structured LLM message content part.
type LLMMessagePartType string

const (
	// LLMMessagePartTypeText identifies one text part.
	LLMMessagePartTypeText LLMMessagePartType = "text"
	// LLMMessagePartTypeImage identifies one inline image part.
	LLMMessagePartTypeImage LLMMessagePartType = "image"
)

// LLMMessagePart is one ordered content part inside an LLM message.
type LLMMessagePart struct {
	// Type identifies which content union field is populated.
	Type LLMMessagePartType
	// Text carries plain text when Type is text.
	Text string
	// Image carries one inline image when Type is image.
	Image *LLMInputImage
}

// Validate checks one content part contract.
func (p LLMMessagePart) Validate() error {
	switch p.Type {
	case LLMMessagePartTypeText:
		if strings.TrimSpace(p.Text) == "" {
			return fmt.Errorf("validate llm message part: missing text")
		}
		if p.Image != nil {
			return fmt.Errorf("validate llm message part: text part must not include image")
		}
	case LLMMessagePartTypeImage:
		if p.Image == nil {
			return fmt.Errorf("validate llm message part: missing image")
		}
		if strings.TrimSpace(p.Text) != "" {
			return fmt.Errorf("validate llm message part: image part must not include text")
		}
		if err := p.Image.Validate(); err != nil {
			return fmt.Errorf("validate llm message part image: %w", err)
		}
	default:
		return fmt.Errorf("validate llm message part: unsupported type %q", p.Type)
	}

	return nil
}

func (p LLMMessagePart) clone() LLMMessagePart {
	cloned := p
	if p.Image != nil {
		image := *p.Image
		if len(p.Image.Data) > 0 {
			image.Data = append([]byte(nil), p.Image.Data...)
		}
		cloned.Image = &image
	}

	return cloned
}

// LLMInputImage is one inline image payload sent to an LLM provider.
type LLMInputImage struct {
	// MIMEType is the image content type.
	MIMEType string
	// Data holds the raw image bytes.
	Data []byte
	// Detail optionally hints desired provider-side visual fidelity.
	Detail LLMInputImageDetail
}

// Validate checks one inline image contract.
func (i LLMInputImage) Validate() error {
	if strings.TrimSpace(i.MIMEType) == "" {
		return fmt.Errorf("validate llm input image: missing mime type")
	}
	if len(i.Data) == 0 {
		return fmt.Errorf("validate llm input image: missing data")
	}
	if err := i.Detail.Validate(); err != nil {
		return fmt.Errorf("validate llm input image: %w", err)
	}

	return nil
}

// LLMInputImageDetail hints how much image detail providers should preserve.
type LLMInputImageDetail string

const (
	// LLMInputImageDetailAuto leaves image detail selection to the provider.
	LLMInputImageDetailAuto LLMInputImageDetail = "auto"
	// LLMInputImageDetailLow requests lower-cost image detail when supported.
	LLMInputImageDetailLow LLMInputImageDetail = "low"
	// LLMInputImageDetailHigh requests maximum image detail when supported.
	LLMInputImageDetailHigh LLMInputImageDetail = "high"
)

// Validate checks one image detail hint.
func (d LLMInputImageDetail) Validate() error {
	switch d {
	case "", LLMInputImageDetailAuto, LLMInputImageDetailLow, LLMInputImageDetailHigh:
		return nil
	default:
		return fmt.Errorf("validate llm input image detail: unsupported value %q", d)
	}
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
	// Kind identifies the semantic category of this chunk.
	//
	// Empty values must be treated as output text for backward compatibility with
	// older providers and tests.
	Kind LLMGenerateChunkKind
	// Delta is the newly generated text segment.
	Delta string
}

// LLMGenerateChunkKind identifies the semantic category of one stream chunk.
type LLMGenerateChunkKind string

const (
	// LLMGenerateChunkKindOutputText identifies normal assistant answer text.
	LLMGenerateChunkKindOutputText LLMGenerateChunkKind = "output_text"
	// LLMGenerateChunkKindThinkingSummary identifies short model thinking summary text.
	LLMGenerateChunkKindThinkingSummary LLMGenerateChunkKind = "thinking_summary"
)

// Normalize returns one supported chunk kind.
//
// Empty and unknown values are normalized to output text for backward
// compatibility.
func (k LLMGenerateChunkKind) Normalize() LLMGenerateChunkKind {
	switch k {
	case LLMGenerateChunkKindThinkingSummary:
		return LLMGenerateChunkKindThinkingSummary
	case LLMGenerateChunkKindOutputText:
		return LLMGenerateChunkKindOutputText
	default:
		return LLMGenerateChunkKindOutputText
	}
}
