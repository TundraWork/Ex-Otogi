// Package gemini is the Google Gemini language model provider: llm.Provider
// over the Gemini API's Interactions endpoint, used statelessly (store=false,
// the full conversation sent each turn) and streamed.
package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
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
	// BaseURL is the API's address; empty means
	// https://generativelanguage.googleapis.com.
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
	// Backend and key are explicit so GOOGLE_API_KEY, GEMINI_API_KEY, and
	// GOOGLE_GENAI_USE_VERTEXAI in the environment cannot change them.
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:      cfg.APIKey,
		Backend:     genai.BackendGeminiAPI,
		HTTPClient:  &http.Client{Transport: transport{base: http.DefaultTransport}},
		HTTPOptions: genai.HTTPOptions{BaseURL: cfg.BaseURL},
	})
	if err != nil {
		return nil, fmt.Errorf("gemini: %w", err)
	}
	return &Provider{client: client, recorder: cfg.Recorder, log: cfg.Logger, backoff: time.Second}, nil
}

// Generate runs one turn; see llm.Provider.
func (p *Provider) Generate(ctx context.Context, req llm.Request, partial func(otogi.Message) error) (llm.Response, error) {
	started := time.Now()
	input, err := inputSteps(req.Turns)
	if err != nil {
		return llm.Response{}, err
	}
	body, err := createBody(req, input)
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
		if err == nil || c.shown || ctx.Err() != nil {
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
func createBody(req llm.Request, input []json.RawMessage) (operations.CreateInteractionRequestBody, error) {
	var steps []ia.Step
	data, err := json.Marshal(input)
	if err != nil {
		return operations.CreateInteractionRequestBody{}, fmt.Errorf("gemini: encode input: %w", err)
	}
	if err := json.Unmarshal(data, &steps); err != nil {
		return operations.CreateInteractionRequestBody{}, fmt.Errorf("gemini: input steps: %w: %w", otogi.ErrInvalid, err)
	}
	interaction := ia.CreateModelInteraction{
		Model:  ia.Model(req.Model),
		Input:  new(ia.NewInteractionsInput(steps)),
		Store:  new(false),
		Stream: new(true),
	}
	if req.System != "" {
		interaction.SystemInstruction = &req.System
	}
	if req.Thinking != "" || req.MaxOutput > 0 {
		interaction.GenerationConfig = &ia.GenerationConfig{}
		if req.Thinking != "" {
			interaction.GenerationConfig.ThinkingLevel = ia.ThinkingLevel(req.Thinking).ToPointer()
		}
		if req.MaxOutput > 0 {
			interaction.GenerationConfig.MaxOutputTokens = &req.MaxOutput
		}
	}
	return operations.NewCreateInteractionRequestBody(interaction), nil
}

// record writes the call's one event log record.
func (p *Provider) record(ctx context.Context, req llm.Request, input []json.RawMessage, c *call, resp llm.Response, err error) {
	work, _ := otogi.WorkFrom(ctx)
	payload := map[string]any{
		"module": work.Module, "origin": work.Origin, "model": req.Model,
		"system": req.System, "thinking": req.Thinking, "max_output": req.MaxOutput,
		"input":  digestMedia(input),
		"output": digestMedia(c.stepList()),
		"reply":  resp.Markdown, "finish": resp.Finish, "reason": resp.Reason, "usage": resp.Usage,
		"status": c.status, "http": c.exchanges.all(),
	}
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
