// Package gemini is the Google Gemini language model provider: llm.Provider
// over the Gemini API's Interactions endpoint, used statelessly (store=false,
// the full conversation sent each turn) and streamed.
package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/sethvargo/go-retry"
	"google.golang.org/genai"
	ia "google.golang.org/genai/interactions/models/interactions"
	"google.golang.org/genai/interactions/models/operations"
	iaretry "google.golang.org/genai/interactions/retry"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Name is the provider's name in configuration, records, and llm.Native.
const Name = "gemini"

// maxAttempts bounds the attempts of one call, retries included.
const maxAttempts = 3

// Config configures the provider.
type Config struct {
	// APIKey authenticates requests.
	APIKey string
	// BaseURL is the API's endpoint, with or without its version, for
	// example https://generativelanguage.googleapis.com/v1beta; empty means
	// that one. A trailing version path selects the API version.
	BaseURL string
	// Recorder receives one record per call.
	Recorder otogi.Recorder
	// Logger receives diagnostics.
	Logger *slog.Logger
}

// Provider is llm.Provider for Gemini.
type Provider struct {
	client   *genai.Client
	recorder otogi.Recorder
	log      *slog.Logger
	// backoff is the first retry delay; shorter in tests.
	backoff time.Duration
}

// New builds the provider. It does no I/O.
func New(cfg Config) (*Provider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("gemini: an API key is required: %w", otogi.ErrInvalid)
	}
	base, version := splitVersion(cfg.BaseURL)
	// Backend and key are explicit so GOOGLE_API_KEY, GEMINI_API_KEY, and
	// GOOGLE_GENAI_USE_VERTEXAI in the environment cannot change them.
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:      cfg.APIKey,
		Backend:     genai.BackendGeminiAPI,
		HTTPClient:  &http.Client{Transport: transport{base: http.DefaultTransport}},
		HTTPOptions: genai.HTTPOptions{BaseURL: base, APIVersion: version},
	})
	if err != nil {
		return nil, fmt.Errorf("gemini: %w", err)
	}
	return &Provider{client: client, recorder: cfg.Recorder, log: cfg.Logger, backoff: time.Second}, nil
}

// apiVersion matches an API version path segment, such as v1beta.
var apiVersion = regexp.MustCompile(`^v\d+((alpha|beta)\d*)?$`)

// splitVersion splits an endpoint that ends in an API version into the base
// and the version; the version is empty when the endpoint has none.
func splitVersion(endpoint string) (base, version string) {
	trimmed := strings.TrimSuffix(endpoint, "/")
	i := strings.LastIndex(trimmed, "/")
	if i < 0 || !apiVersion.MatchString(trimmed[i+1:]) || strings.HasSuffix(trimmed[:i], ":/") {
		return endpoint, ""
	}
	return trimmed[:i], trimmed[i+1:]
}

// Generate runs one turn; see llm.Provider.
func (p *Provider) Generate(ctx context.Context, req llm.Request, partial func(otogi.Message) error) (llm.Response, error) {
	started := time.Now()
	input, err := inputSteps(req.Turns)
	if err != nil {
		return llm.Response{}, err
	}
	tools, err := toolList(req.Tools)
	if err != nil {
		return llm.Response{}, err
	}
	body, err := createBody(req, input, tools)
	if err != nil {
		return llm.Response{}, err
	}
	c := &call{started: started, partial: partial}
	ctx = context.WithValue(ctx, exchangesKey{}, &c.exchanges)

	var serverDelay time.Duration
	base := retry.WithMaxRetries(maxAttempts-1, retry.WithCappedDuration(30*time.Second, retry.WithJitterPercent(20, retry.NewExponential(p.backoff))))
	backoff := retry.BackoffFunc(func() (time.Duration, bool) {
		d, stop := base.Next()
		if stop {
			return 0, true
		}
		if serverDelay > 0 {
			d, serverDelay = serverDelay, 0
		}
		c.exchanges.waited(d)
		return d, false
	})
	err = retry.Do(ctx, backoff, func(ctx context.Context) error {
		c.reset()
		err := p.attempt(ctx, body, c)
		if err == nil || errors.Is(err, errStopped) || ctx.Err() != nil {
			return err
		}
		var retryable bool
		retryable, serverDelay = c.exchanges.retryable()
		if retryable {
			return retry.RetryableError(err)
		}
		return err
	})
	resp := c.response()
	var blocked *blockedError
	if errors.As(err, &blocked) {
		resp.Finish, resp.Reason, err = llm.FinishBlocked, blocked.reason, nil
	}
	p.record(ctx, req, input, c, resp, err)
	if err != nil {
		return llm.Response{}, err
	}
	return resp, nil
}

// attempt makes one request and reads its stream to the end.
func (p *Provider) attempt(ctx context.Context, body operations.CreateInteractionRequestBody, c *call) error {
	res, err := p.client.Interactions.Create(ctx, operations.CreateInteractionRequest{Body: body}, operations.WithRetries(iaretry.Config{Strategy: "none"}))
	if err != nil {
		return c.exchanges.failure(ctx, err)
	}
	s := res.InteractionSSEStreamEvent
	if s == nil {
		return fmt.Errorf("gemini: the response was not a stream: %w", otogi.ErrUnavailable)
	}
	defer func() {
		if err := s.Close(); err != nil {
			p.log.Debug("gemini: close stream", "error", err)
		}
	}()
	for s.Next() {
		if err := c.event(s.Value()); err != nil {
			return err
		}
	}
	if err := s.Err(); err != nil {
		return c.exchanges.failure(ctx, err)
	}
	return c.failure()
}

// createBody is the request: stateless and streamed, with the SDK's own
// retries off because Generate retries.
func createBody(req llm.Request, input []json.RawMessage, tools []map[string]any) (operations.CreateInteractionRequestBody, error) {
	var steps []ia.Step
	if err := convert(input, &steps); err != nil {
		return operations.CreateInteractionRequestBody{}, fmt.Errorf("gemini: input steps: %w", err)
	}
	var declared []ia.Tool
	if err := convert(tools, &declared); err != nil {
		return operations.CreateInteractionRequestBody{}, fmt.Errorf("gemini: tools: %w", err)
	}
	interaction := ia.CreateModelInteraction{
		Model:  ia.Model(req.Model),
		Input:  new(ia.NewInteractionsInput(steps)),
		Store:  new(false),
		Stream: new(true),
		Tools:  declared,
	}
	if req.System != "" {
		interaction.SystemInstruction = &req.System
	}
	if req.Thinking != "" || req.MaxOutput > 0 || req.Temperature != nil {
		interaction.GenerationConfig = &ia.GenerationConfig{}
		if req.Thinking != "" {
			interaction.GenerationConfig.ThinkingLevel = ia.ThinkingLevel(req.Thinking).ToPointer()
		}
		if req.MaxOutput > 0 {
			interaction.GenerationConfig.MaxOutputTokens = &req.MaxOutput
		}
		if req.Temperature != nil {
			// The Interactions API marks temperature deprecated, but still
			// applies it (checked live 2026-11-05: it validates the range).
			// When the SDK drops the field, the build fails here.
			t := float32(*req.Temperature)
			interaction.GenerationConfig.Temperature = &t //nolint:staticcheck // see above
		}
	}
	return operations.NewCreateInteractionRequestBody(interaction), nil
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// convert re-reads from, our wire-shaped JSON, as the SDK's typed form to.
func convert(from, to any) error {
	data, err := json.Marshal(from)
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	if err := json.Unmarshal(data, to); err != nil {
		return fmt.Errorf("%w: %w", otogi.ErrInvalid, err)
	}
	return nil
}

// builtins are the built-in tools Gemini runs itself.
var builtins = map[string]bool{llm.BuiltinGoogleSearch: true, llm.BuiltinURLContext: true, llm.BuiltinCodeExecution: true}

// toolList declares the request's tools on the wire.
func toolList(specs []llm.ToolSpec) ([]map[string]any, error) {
	var tools []map[string]any
	for _, s := range specs {
		switch {
		case s.Function != nil:
			tool := map[string]any{"type": "function", "name": s.Function.Name, "description": s.Function.Description}
			if len(s.Function.Parameters) > 0 {
				tool["parameters"] = s.Function.Parameters
			}
			tools = append(tools, tool)
		case builtins[s.Builtin]:
			tools = append(tools, map[string]any{"type": s.Builtin})
		default:
			return nil, fmt.Errorf("gemini: tool %q: %w", s.Builtin, otogi.ErrUnsupported)
		}
	}
	return tools, nil
}

// A call slower than slowCall in all, or silent for slowSilence at a
// stretch, is logged as a warning. Normal calls on the test deployment take
// 5 to 50 s, image generation included.
const (
	slowCall    = 60 * time.Second
	slowSilence = 30 * time.Second
)

// record writes the call's one event log record.
func (p *Provider) record(ctx context.Context, req llm.Request, input []json.RawMessage, c *call, resp llm.Response, err error) {
	work, _ := otogi.WorkFrom(ctx)
	payload := map[string]any{
		"module": work.Module, "origin": work.Origin, "model": req.Model,
		"system": req.System, "thinking": req.Thinking, "max_output": req.MaxOutput, "temperature": req.Temperature, "tools": req.Tools,
		"input":  digestMedia(input),
		"output": digestMedia(c.stepList()),
		"reply":  resp.Markdown, "finish": resp.Finish, "reason": resp.Reason, "usage": resp.Usage,
		"status": c.status,
	}
	xs := c.exchanges.all()
	payload["http"] = xs
	took := time.Since(c.started)
	quiet := c.longestSilence(took)
	payload["started"], payload["timeline"], payload["interactions"], payload["longest_silence"] = c.started, c.timeline, c.interactions, quiet
	if c.firstOutput > 0 {
		payload["first_output"] = c.firstOutput.String()
	}
	if len(c.unhandled) > 0 {
		payload["unhandled"] = c.unhandled
	}
	kind, outcome := "llm.completed", "ok"
	if err != nil {
		kind, outcome, payload["error"] = "llm.failed", "error", err.Error()
	}
	headers := make([]string, 0, len(xs))
	for _, x := range xs {
		headers = append(headers, x.Duration)
	}
	// Every call is recorded; the process log keeps the ones worth a look.
	level := slog.LevelDebug
	switch {
	case err != nil:
		level = slog.LevelError
	case len(xs) > 1 || took > slowCall || quiet.d > slowSilence:
		level = slog.LevelWarn
	}
	p.log.Log(ctx, level, "gemini: call", "model", req.Model, "outcome", outcome, "took", took.Round(time.Millisecond).String(),
		"headers_after", headers, "first_output", c.firstOutput.Round(time.Millisecond).String(), "events", len(c.timeline),
		"longest_silence", quiet.For, "silence_after", quiet.After, "silence_at", quiet.At, "interactions", c.interactions, "error", errText(err))
	p.recorder.Record(context.Background(), otogi.Record{
		At: time.Now(), Kind: kind, Component: "llm:" + Name, Event: work.Event,
		Platform: work.Destination.Platform, Account: work.Destination.Account, Conversation: work.Destination.Conversation,
		Outcome: outcome, Duration: time.Since(c.started), Payload: payload,
	})
}

// failure is the error of a call whose stream ended without completing.
func (c *call) failure() error {
	switch {
	case c.streamErr != "":
		return fmt.Errorf("gemini: %s: %w", c.streamErr, otogi.ErrUnavailable)
	case c.status == "":
		return fmt.Errorf("gemini: the stream ended before the interaction completed: %w", otogi.ErrUnavailable)
	}
	return nil
}

// errStopped marks an error the caller's partial callback returned, which
// is never retried.
var errStopped = errors.New("stopped by the caller")

// finishOf maps an interaction's final status to a finish.
func finishOf(status string) llm.Finish {
	switch strings.ToLower(status) {
	case string(ia.InteractionStatusCompleted):
		return llm.FinishStop
	case string(ia.InteractionStatusIncomplete):
		return llm.FinishMaxTokens
	default:
		return llm.FinishOther
	}
}
