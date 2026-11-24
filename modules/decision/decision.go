// Package decision provides the decision service, contract.Service: yes/no
// questions about a state, answered by TypeSafe Jev with the probability of
// yes. It handles no events. Every request is recorded in full, state,
// questions, answers, and Jev's reply, so a threshold can be tuned against
// what a judgment saw.
package decision

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	contract "github.com/TundraWork/Ex-Otogi/pkg/decision"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Config is the module's settings; each unset field keeps the default.
type Config struct {
	Jev *JevConfig `json:"jev" doc:"TypeSafe Jev, the decider: the only one for now."`
}

// JevConfig configures TypeSafe Jev.
type JevConfig struct {
	APIKey  otogi.Secret   `json:"api_key" doc:"The TypeSafe API key."`
	BaseURL string         `json:"base_url,omitempty" pattern:"^https?://" patternDescription:"an http:// or https:// URL" default:"https://api.typesafe.ai/v1/systemone" doc:"The Jev endpoint."`
	Model   string         `json:"model,omitempty" default:"jev-latest" doc:"The Jev model. jev-latest follows TypeSafe's upgrades, so judgments can change with no change here. Thresholds are tuned against one version: to keep judgments stable, name it, such as jev-1.14.0."`
	Timeout otogi.Duration `json:"timeout,omitempty" default:"5s" doc:"The longest one request may take. A request is not retried: a timeout is no decision."`
}

// Defaults for unset settings.
const (
	defaultBaseURL = "https://api.typesafe.ai/v1/systemone"
	defaultModel   = "jev-latest"
	defaultTimeout = 5 * time.Second
)

// Module is the decision module. Once started it is the decision service.
type Module struct {
	jev  *jev
	host otogi.Host
}

// New reads the API key through secret, which returns a secret by name, and
// builds the module, filling unset fields with their defaults. It does no
// I/O.
func New(cfg Config, secret func(name string) (string, bool)) (*Module, error) {
	c := JevConfig{}
	if cfg.Jev != nil {
		c = *cfg.Jev
	}
	key, err := c.APIKey.Resolve(secret)
	if err != nil {
		return nil, &otogi.SettingError{Path: "jev.api_key", Err: err}
	}
	j := &jev{url: c.BaseURL, key: key, model: c.Model, timeout: time.Duration(c.Timeout), http: &http.Client{}}
	if j.url == "" {
		j.url = defaultBaseURL
	}
	if j.model == "" {
		j.model = defaultModel
	}
	if j.timeout == 0 {
		j.timeout = defaultTimeout
	}
	return &Module{jev: j}, nil
}

// Descriptor declares the module: it handles no events and provides the
// decision service.
func (*Module) Descriptor() otogi.ModuleDescriptor {
	return otogi.ModuleDescriptor{ID: "decision", Provides: []string{contract.Service}}
}

// Start provides the decision service.
func (m *Module) Start(_ context.Context, host otogi.Host) error {
	m.host = host
	return host.Provide(contract.Service, contract.Decider(m))
}

// Handle is never called: the module subscribes to nothing.
func (*Module) Handle(context.Context, otogi.Event, otogi.Invocation) error { return nil }

// Decide asks Jev and records the request. It does not retry.
func (m *Module) Decide(ctx context.Context, req contract.Request) (contract.Answers, error) {
	start := time.Now()
	answers, ex, err := m.jev.decide(ctx, req)
	took := time.Since(start)

	payload := map[string]any{
		"model":     ex.model,
		"questions": ex.questions,
		"answers":   answers,
		"usage":     ex.usage,
	}
	if w, ok := otogi.WorkFrom(ctx); ok {
		payload["module"] = w.Module
	}
	if ex.state != nil {
		payload["state"] = ex.state
	}
	if ex.requestID != "" {
		payload["request_id"] = ex.requestID
	}
	if ex.reply != nil {
		if json.Valid(ex.reply) {
			payload["reply"] = json.RawMessage(ex.reply)
		} else {
			payload["reply"] = string(ex.reply)
		}
	}
	r := otogi.Record{Kind: "decision.completed", Outcome: "ok", Duration: took, Payload: payload}
	log := m.host.Logger()
	if err != nil {
		r.Kind, r.Outcome = "decision.failed", "error"
		payload["error"] = err.Error()
		if ex.status != 0 {
			payload["status"] = ex.status
		}
		log.WarnContext(ctx, "decision: request failed", "model", ex.model, "duration", took, "error", err)
	} else {
		log.DebugContext(ctx, "decision: answered", "model", ex.model, "duration", took, "questions", len(ex.questions), "input_tokens", ex.usage.InputTokens)
	}
	m.host.Record(ctx, r)
	if err != nil {
		return nil, fmt.Errorf("decision: %w", err)
	}
	return answers, nil
}
