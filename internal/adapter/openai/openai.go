// Package openai is the language model provider for services that speak the
// OpenAI Chat Completions API, such as DeepSeek: llm.Provider over
// POST <base>/chat/completions, used statelessly (the whole conversation sent
// each turn) and streamed.
//
// It speaks the API with net/http rather than the openai-go SDK. Most of what
// it sends back and keeps are a service's own fields: DeepSeek's
// reasoning_content and server_tool_calls on assistant messages, its
// server tools, server_tool_event on stream chunks, and its cache and tool
// usage. The SDK reaches those only through its raw-field escape hatches, its
// stream accumulator does not know them, and a saved assistant message must
// go back as it came. What is left for the SDK is posting a request and
// splitting a stream into lines, which this package does itself.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sethvargo/go-retry"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// maxAttempts bounds the attempts of one call, retries included.
const maxAttempts = 3

// Config configures one service.
type Config struct {
	// Name is the provider's name in configuration, records, and
	// llm.Native, such as "deepseek".
	Name string
	// BaseURL is the API's base, without /chat/completions, for example
	// https://api.deepseek.com or https://api.openai.com/v1.
	BaseURL string
	// APIKey authenticates requests.
	APIKey string
	// Recorder receives one record per call.
	Recorder otogi.Recorder
	// Logger receives diagnostics.
	Logger *slog.Logger
}

// Provider is llm.Provider for one Chat Completions service.
type Provider struct {
	name     string
	endpoint string
	key      string
	client   *http.Client
	recorder otogi.Recorder
	log      *slog.Logger
	// backoff is the first retry delay; shorter in tests.
	backoff time.Duration
}

// New builds the provider. It does no I/O.
func New(cfg Config) (*Provider, error) {
	switch {
	case cfg.Name == "":
		return nil, fmt.Errorf("openai: a name is required: %w", otogi.ErrInvalid)
	case cfg.BaseURL == "":
		return nil, fmt.Errorf("openai %s: a base URL is required: %w", cfg.Name, otogi.ErrInvalid)
	case cfg.APIKey == "":
		return nil, fmt.Errorf("openai %s: an API key is required: %w", cfg.Name, otogi.ErrInvalid)
	}
	return &Provider{
		name: cfg.Name, endpoint: strings.TrimSuffix(cfg.BaseURL, "/") + "/chat/completions", key: cfg.APIKey,
		client: &http.Client{}, recorder: cfg.Recorder, log: cfg.Logger, backoff: time.Second,
	}, nil
}

// Generate runs one turn; see llm.Provider.
func (p *Provider) Generate(ctx context.Context, req llm.Request, partial func(otogi.Message) error) (llm.Response, error) {
	started := time.Now()
	messages, replaced, err := p.messages(req)
	if err != nil {
		return llm.Response{}, err
	}
	tools, err := toolList(req.Tools)
	if err != nil {
		return llm.Response{}, fmt.Errorf("openai %s: %w", p.name, err)
	}
	body, err := json.Marshal(request{
		Model: req.Model, Messages: messages, Stream: true, StreamOptions: streamOptions{IncludeUsage: true},
		ReasoningEffort: req.Thinking, MaxTokens: req.MaxOutput, Temperature: req.Temperature, Tools: tools,
	})
	if err != nil {
		return llm.Response{}, fmt.Errorf("openai %s: encode request: %w", p.name, err)
	}
	c := &call{started: started, partial: partial, name: p.name}

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
		c.waited(d)
		return d, false
	})
	err = retry.Do(ctx, backoff, func(ctx context.Context) error {
		c.reset()
		err := p.attempt(ctx, body, c)
		if err == nil || errors.Is(err, errStopped) || ctx.Err() != nil || !errors.Is(err, otogi.ErrUnavailable) {
			return err
		}
		serverDelay = c.retryAfter()
		return retry.RetryableError(err)
	})
	if err != nil && ctx.Err() != nil && !errors.Is(err, errStopped) {
		err = fmt.Errorf("openai %s: %w", p.name, ctx.Err())
	}
	resp := c.response()
	p.record(ctx, req, messages, replaced, c, resp, err)
	if err != nil {
		return llm.Response{}, err
	}
	for _, r := range replaced {
		resp.Withheld = append(resp.Withheld, llm.WithheldPart{Part: otogi.PartID(r.Part), MediaType: r.MediaType, Bytes: r.Bytes})
	}
	return resp, nil
}

// attempt makes one request and reads its stream to the end.
func (p *Provider) attempt(ctx context.Context, body []byte, c *call) error {
	a := c.begin()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("openai %s: %w: %w", p.name, otogi.ErrInvalid, err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.key)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	res, err := p.client.Do(httpReq)
	a.Headers = since(a.started)
	if err != nil {
		a.Error = err.Error()
		return fmt.Errorf("openai %s: %w: %w", p.name, otogi.ErrUnavailable, err)
	}
	defer func() { _ = res.Body.Close() }()
	a.Status, a.ResponseHeaders, a.RetryAfter = res.StatusCode, res.Header.Clone(), res.Header.Get("Retry-After")
	if res.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		a.Body = string(data)
		return statusError(p.name, res.StatusCode, data)
	}
	err = c.read(res.Body)
	a.Done = since(a.started)
	if err != nil && !errors.Is(err, errStopped) {
		a.Error = err.Error()
	}
	return err
}

// statusError is the error for a response with a failing status: rate
// limits and server faults are worth retrying; any other is the request's.
func statusError(name string, status int, body []byte) error {
	kind := otogi.ErrInvalid
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		kind = otogi.ErrUnavailable
	}
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	message := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		message = e.Error.Message
	}
	return &llm.HTTPError{Provider: "openai " + name, Status: status, Message: message, Kind: kind}
}

// retryAfter is the wait the last attempt's Retry-After asked for, in
// seconds.
func (c *call) retryAfter() time.Duration {
	if len(c.attempts) == 0 {
		return 0
	}
	if seconds, err := strconv.Atoi(c.attempts[len(c.attempts)-1].RetryAfter); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return 0
}

// errStopped marks an error the caller's partial callback returned, which
// is never retried.
var errStopped = errors.New("stopped by the caller")

// A call slower than slowCall is logged as a warning.
const slowCall = 60 * time.Second

// record writes the call's one event log record.
func (p *Provider) record(ctx context.Context, req llm.Request, messages []json.RawMessage, replaced []replacedPart, c *call, resp llm.Response, err error) {
	work, _ := otogi.WorkFrom(ctx)
	payload := map[string]any{
		"module": work.Module, "origin": work.Origin, "model": req.Model,
		"system": req.System, "thinking": req.Thinking, "max_output": req.MaxOutput, "temperature": req.Temperature, "tools": req.Tools,
		"messages": digest(messages),
		"reply":    resp.Markdown, "reasoning": c.reasoning.String(), "calls": resp.Turn.Calls, "sources": resp.Sources,
		"finish": resp.Finish, "reason": resp.Reason, "usage": resp.Usage, "http": c.attempts,
	}
	if len(c.serverCalls) > 0 {
		payload["server_tool_calls"] = digest(c.serverCalls)
	}
	if len(c.events) > 0 {
		payload["server_tool_events"] = digest(c.events)
	}
	if c.serverUse != nil {
		payload["server_tool_use"] = c.serverUse
	}
	if len(replaced) > 0 {
		payload["replaced"] = replaced
	}
	if c.firstOutput > 0 {
		payload["first_output"] = c.firstOutput.String()
	}
	kind, outcome := "llm.completed", "ok"
	if err != nil {
		kind, outcome, payload["error"] = "llm.failed", "error", err.Error()
	}
	took := time.Since(c.started)
	level := slog.LevelDebug
	switch {
	case err != nil:
		level = slog.LevelError
	case len(c.attempts) > 1 || took > slowCall:
		level = slog.LevelWarn
	}
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	p.log.Log(ctx, level, p.name+": call", "model", req.Model, "outcome", outcome, "took", took.Round(time.Millisecond).String(),
		"attempts", len(c.attempts), "first_output", c.firstOutput.Round(time.Millisecond).String(), "error", errText)
	p.recorder.Record(context.Background(), otogi.Record{
		At: time.Now(), Kind: kind, Component: "llm:" + p.name, Event: work.Event,
		Platform: work.Destination.Platform, Account: work.Destination.Account, Conversation: work.Destination.Conversation,
		Outcome: outcome, Duration: took, Payload: payload,
	})
}
