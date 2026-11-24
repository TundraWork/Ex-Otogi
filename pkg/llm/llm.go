// Package llm is the contract between modules and language model providers.
// Modules that use language models depend only on it; the composition root
// supplies providers, such as Gemini, configured by the operator.
//
// Content is otogi.Message both ways: a chat message, images included, can be
// handed to a model as it is, and a model's reply, rich text and any images
// it generated, can be shown as it is. Text goes to models as Markdown and
// comes back from Markdown (see package markdown).
package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Provider runs language models. Implementations are safe for concurrent use
// and record every call in the event log, attributed to the module and event
// through otogi.WorkFrom(ctx).
type Provider interface {
	// Generate runs one model turn. If partial is non-nil, it is called as
	// output arrives from the model, per streamed chunk, with the whole reply
	// so far: rich text and any images completed so far, ready for
	// otogi.MessageStream.Update. When the provider retries after output
	// began, for example because the connection dropped mid-reply, partial
	// starts over: the reply so far can get shorter. A non-nil error from
	// partial stops generation, and Generate returns it. Generate returns
	// the complete reply when the turn ends.
	//
	// Errors wrap otogi.ErrInvalid when the provider rejects the request,
	// and otogi.ErrUnavailable for rate limits, outages, and timeouts left
	// after retries; their text carries the provider's own message. A
	// blocked prompt or reply is not an error: see Finish.
	Generate(ctx context.Context, req Request, partial func(otogi.Message) error) (Response, error)
}

// ModelLister is a Provider that can list the models it offers. Not every
// provider can: a proxy may not serve the list, so callers check for it.
type ModelLister interface {
	// ListModels asks the provider, now, for the models it offers that
	// generate content, every page of the list, in the provider's order.
	// It records nothing: the caller knows who asked and records the
	// listing.
	//
	// Errors wrap otogi.ErrInvalid when the provider refuses the request,
	// such as for a wrong key or an endpoint with no list, and
	// otogi.ErrUnavailable for outages; their text carries the HTTP status
	// and the provider's own message.
	ListModels(ctx context.Context) ([]Model, error)
}

// Model is a model a provider offers, as its list describes it. Only the ID
// is always known; every other field is empty when the list does not say.
type Model struct {
	// ID is what Request.Model takes, for example "gemini-3.5-flash".
	ID string `json:"id"`
	// DisplayName is the provider's name for it, for people.
	DisplayName string `json:"display_name,omitempty"`
	// InputTokens is the most tokens a request may have.
	InputTokens int `json:"input_tokens,omitempty"`
	// OutputTokens is the most tokens a reply may have.
	OutputTokens int `json:"output_tokens,omitempty"`
	// ContextTokens is the most tokens a request and its reply may have
	// together, for a provider that gives that instead of InputTokens.
	ContextTokens int `json:"context_tokens,omitempty"`
	// InputModalities are the kinds of input it takes, such as "text" and
	// "image", as the provider names them.
	InputModalities []string `json:"input_modalities,omitempty"`
	// ThinkingLevels are the thinking levels it accepts, as the provider
	// names them; a Request.Thinking not among them is likely refused.
	ThinkingLevels []string `json:"thinking_levels,omitempty"`
}

// Request is one model turn.
type Request struct {
	// Model is the provider's model ID, for example "gemini-3.8-flash".
	Model string
	// System is the system instruction, in Markdown.
	System string
	// Turns is the conversation so far, oldest first; the last is the
	// user's turn to answer.
	Turns []Turn
	// Thinking is "minimal", "low", "medium", or "high"; empty is the
	// model's default.
	Thinking string
	// MaxOutput limits output tokens; zero is the model's default.
	MaxOutput int
	// Temperature is the sampling temperature, from 0 (most predictable)
	// to 2; nil is the model's default.
	Temperature *float64
	// Tools are offered to the model. Run adds the functions of the tools
	// it runs.
	Tools []ToolSpec
}

// Role says who a turn is from.
type Role string

const (
	// RoleUser is the people talking to the model.
	RoleUser Role = "user"
	// RoleModel is the model.
	RoleModel Role = "model"
	// RoleTool carries the results of a model turn's function calls.
	RoleTool Role = "tool"
)

// Turn is one turn of a conversation.
type Turn struct {
	// Role says who the turn is from.
	Role Role `json:"role"`
	// Content is the turn as rich text and media, in order. Text parts with
	// their annotations, media parts, and file parts are sent; a media or
	// file type the provider's model cannot take becomes a short text mark,
	// listed in the call's record and in Response.Withheld, and does not
	// fail the call. Media and files must be inline (see Inline). Other part
	// kinds are an otogi.ErrUnsupported error: the caller decides how to
	// describe a location, a poll, and so on. Who said what in a group is
	// content too: write it into the text.
	Content otogi.Message `json:"content"`
	// Native is the provider's own encoding of a model turn, as returned in
	// Response.Turn. The provider that made it resends it exactly, which
	// keeps the model's hidden reasoning and generated images intact; other
	// providers use Content. Store it with the turn.
	Native *Native `json:"native,omitempty"`
	// Calls are the functions a model turn calls, in order.
	Calls []Call `json:"calls,omitempty"`
	// Results answer the calls of the model turn before, in a RoleTool turn.
	Results []Result `json:"results,omitempty"`
}

// Native is a provider's own encoding of a turn.
type Native struct {
	// Provider names the encoding, for example "gemini".
	Provider string `json:"provider"`
	// Data is provider-defined.
	Data json.RawMessage `json:"data"`
}

// Finish says how a turn ended.
type Finish string

const (
	// FinishStop is a complete reply.
	FinishStop Finish = "stop"
	// FinishMaxTokens is a reply cut off at the output limit.
	FinishMaxTokens Finish = "max_tokens"
	// FinishBlocked is a prompt or reply the provider blocked, for example
	// for safety; Reason says why.
	FinishBlocked Finish = "blocked"
	// FinishToolCalls is a turn that ends in function calls to run; see
	// Run.
	FinishToolCalls Finish = "tool_calls"
	// FinishOther is any other end; Reason says what happened.
	FinishOther Finish = "other"
)

// Response is a model's turn.
type Response struct {
	// Turn is the model's turn. Its Content is the reply as the model
	// produced it, rich text and any images it generated, in order; Native
	// is set. Keep the Turn to continue the conversation.
	Turn Turn
	// Markdown is the reply's text as the model wrote it.
	Markdown string
	// Finish says how the turn ended.
	Finish Finish
	// Reason is the provider's own account of the end, such as a status or
	// block reason, when it is not a plain stop.
	Reason string
	// Usage counts tokens.
	Usage Usage
	// Sources are the pages the reply cites, when a search tool grounded it.
	Sources []Source
	// Withheld are the media and file parts of the request the model did
	// not get: the provider sent each as a text mark instead. Empty when the
	// model got everything.
	Withheld []WithheldPart
}

// WithheldPart is a media or file part a provider did not give the model.
type WithheldPart struct {
	// Part is the part's ID.
	Part otogi.PartID `json:"part"`
	// MediaType is its MIME type.
	MediaType string `json:"media_type"`
	// Bytes is its size.
	Bytes int `json:"bytes"`
}

// Source is a page a reply cites.
type Source struct {
	// Title names the page, often its site.
	Title string `json:"title"`
	// URL is the page's address, possibly through the provider's redirect.
	URL string `json:"url"`
}

// Usage counts the tokens of a turn.
type Usage struct {
	// Input counts prompt tokens, including cached ones.
	Input int `json:"input"`
	// Cached counts prompt tokens served from the provider's cache.
	Cached int `json:"cached"`
	// Output counts reply tokens.
	Output int `json:"output"`
	// Thoughts counts tokens the model spent thinking.
	Thoughts int `json:"thoughts"`
}

// Add sums two usages.
func (u Usage) Add(v Usage) Usage {
	return Usage{Input: u.Input + v.Input, Cached: u.Cached + v.Cached, Output: u.Output + v.Output, Thoughts: u.Thoughts + v.Thoughts}
}

// Inline returns msg with the bytes of every media and file part fetched
// inline through inv.FetchMedia, ready for a Turn. from is where msg was
// seen. Parts already inline are kept as they are.
func Inline(ctx context.Context, inv otogi.Invocation, from otogi.Destination, msg otogi.Message) (otogi.Message, error) {
	parts := make([]otogi.Part, len(msg.Parts))
	copy(parts, msg.Parts)
	for i, p := range parts {
		if p.Media == nil || p.Media.Source.Kind == otogi.MediaSourceInline {
			continue
		}
		data, err := inv.FetchMedia(ctx, from, p.Media.Source)
		if err != nil {
			return otogi.Message{}, fmt.Errorf("inline media %q: %w", p.ID, err)
		}
		media := *p.Media
		media.Source = otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: data}
		parts[i].Media = &media
	}
	msg.Parts = parts
	return msg, nil
}
