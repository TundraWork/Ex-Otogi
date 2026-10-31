package gemini

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/markdown"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// exchange is one HTTP request of a call, as the transport saw it.
type exchange struct {
	Status     int    `json:"status,omitempty"`
	Error      string `json:"error,omitempty"`       // a transport failure
	Body       string `json:"body,omitempty"`        // the full body of a non-2xx response
	RetryAfter string `json:"retry_after,omitempty"` // the Retry-After header
	Duration   string `json:"duration"`              // until the response headers
	Wait       string `json:"wait,omitempty"`        // waited before the next attempt
}

// exchanges are a call's HTTP requests: retry attempts and continuation hops.
type exchanges struct {
	mu   sync.Mutex
	list []exchange
}

type exchangesKey struct{}

// transport keeps each exchange of a call, found through the request's
// context, so errors and retries are decided on what the wire said, and the
// record shows it in full.
type transport struct{ base http.RoundTripper }

func (t transport) RoundTrip(req *http.Request) (*http.Response, error) {
	log, _ := req.Context().Value(exchangesKey{}).(*exchanges)
	started := time.Now()
	resp, err := t.base.RoundTrip(req)
	if log == nil {
		return resp, err
	}
	x := exchange{Duration: time.Since(started).String()}
	if err != nil {
		x.Error = err.Error()
	} else {
		x.Status = resp.StatusCode
		x.RetryAfter = resp.Header.Get("Retry-After")
		if resp.StatusCode >= 300 {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(body))
			x.Body = string(body)
			if readErr != nil {
				x.Error = "read body: " + readErr.Error()
			}
		}
	}
	log.mu.Lock()
	log.list = append(log.list, x)
	log.mu.Unlock()
	return resp, err
}

func (e *exchanges) last() (exchange, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.list) == 0 {
		return exchange{}, false
	}
	return e.list[len(e.list)-1], true
}

// waited notes the delay before the next attempt.
func (e *exchanges) waited(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.list) > 0 {
		e.list[len(e.list)-1].Wait = d.String()
	}
}

func (e *exchanges) all() []exchange {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]exchange(nil), e.list...)
}

// retryable says whether the last exchange failed in a way worth another
// attempt, and how long the server asked to wait. A daily quota is not
// worth retrying today.
func (e *exchanges) retryable() (bool, time.Duration) {
	x, ok := e.last()
	switch {
	case !ok || x.Status == 0:
		return true, 0 // the request never got an answer
	case x.Status != http.StatusRequestTimeout && x.Status != http.StatusTooManyRequests && x.Status < 500:
		return false, 0
	case strings.Contains(x.Body, "PerDay"):
		return false, 0
	}
	if seconds, err := strconv.Atoi(x.RetryAfter); err == nil {
		return true, time.Duration(seconds) * time.Second
	}
	return true, retryDelay(x.Body)
}

// retryDelay finds a google.rpc.RetryInfo retryDelay, such as "7s", in an
// error body.
func retryDelay(body string) time.Duration {
	var v any
	if json.Unmarshal([]byte(body), &v) != nil {
		return 0
	}
	var find func(any) time.Duration
	find = func(v any) time.Duration {
		switch v := v.(type) {
		case map[string]any:
			if s, ok := v["retryDelay"].(string); ok {
				if d, err := time.ParseDuration(s); err == nil {
					return d
				}
			}
			for _, x := range v {
				if d := find(x); d > 0 {
					return d
				}
			}
		case []any:
			for _, x := range v {
				if d := find(x); d > 0 {
					return d
				}
			}
		}
		return 0
	}
	return find(v)
}

// failure is the error for a request or stream that failed, from what the
// wire said: the SDK's own error text redacts response bodies.
func (e *exchanges) failure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("gemini: %w", ctx.Err())
	}
	x, ok := e.last()
	if !ok || x.Status < 300 {
		return fmt.Errorf("gemini: %w: %w", otogi.ErrUnavailable, err)
	}
	kind := otogi.ErrInvalid
	if x.Status == http.StatusRequestTimeout || x.Status == http.StatusTooManyRequests || x.Status >= 500 {
		kind = otogi.ErrUnavailable
	}
	return fmt.Errorf("gemini: HTTP %d: %s: %w", x.Status, x.Body, kind)
}

// inputSteps converts turns to Interactions input steps. A model turn the
// provider made is resent exactly as it was received.
func inputSteps(turns []llm.Turn) ([]json.RawMessage, error) {
	var out []json.RawMessage
	for i, t := range turns {
		if t.Role == llm.RoleModel && t.Native != nil && t.Native.Provider == Name {
			var steps []json.RawMessage
			if err := json.Unmarshal(t.Native.Data, &steps); err != nil {
				return nil, fmt.Errorf("gemini: turn %d: native steps: %w: %w", i, otogi.ErrInvalid, err)
			}
			out = append(out, steps...)
			continue
		}
		blocks, err := contentBlocks(t.Content)
		if err != nil {
			return nil, fmt.Errorf("gemini: turn %d: %w", i, err)
		}
		kind := "user_input"
		if t.Role == llm.RoleModel {
			kind = "model_output"
		}
		step, err := json.Marshal(map[string]any{"type": kind, "content": blocks})
		if err != nil {
			return nil, fmt.Errorf("gemini: turn %d: %w", i, err)
		}
		out = append(out, step)
	}
	return out, nil
}

// contentBlocks converts a message: text as Markdown, media and files as
// inline data of their kind.
func contentBlocks(msg otogi.Message) ([]map[string]any, error) {
	var blocks []map[string]any
	for _, p := range msg.Parts {
		switch p.Kind {
		case otogi.PartText:
			if text := markdown.Format(p.Text, p.Annotations); text != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": text})
			}
		case otogi.PartMedia, otogi.PartFile:
			if p.Media == nil || p.Media.Source.Kind != otogi.MediaSourceInline {
				return nil, fmt.Errorf("media part %q is not inline (see llm.Inline): %w", p.ID, otogi.ErrInvalid)
			}
			data := p.Media.Source.Inline
			mime := p.Media.MediaType
			if mime == "" {
				mime, _, _ = strings.Cut(http.DetectContentType(data), ";")
			}
			kind := "document"
			for _, k := range []string{"image", "audio", "video"} {
				if strings.HasPrefix(mime, k+"/") {
					kind = k
				}
			}
			blocks = append(blocks, map[string]any{"type": kind, "data": base64.StdEncoding.EncodeToString(data), "mime_type": mime})
		default:
			return nil, fmt.Errorf("%s part %q: %w", p.Kind, p.ID, otogi.ErrUnsupported)
		}
	}
	return blocks, nil
}

// digestMedia is v's JSON form with the data of every media block replaced
// by its size and SHA-256, for records: a conversation's images are resent
// on every call, and their bytes stay in the platform and the caller's
// history.
func digestMedia(v any) any {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("unencodable: %v", err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return fmt.Sprintf("undecodable: %v", err)
	}
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if encoded, ok := v["data"].(string); ok {
				if raw, err := base64.StdEncoding.DecodeString(encoded); err == nil {
					sum := sha256.Sum256(raw)
					v["data"] = map[string]any{"size": len(raw), "sha256": fmt.Sprintf("%x", sum)}
				}
			}
			for _, x := range v {
				walk(x)
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(out)
	return out
}

func sha256Hash(data []byte) otogi.MediaHash {
	sum := sha256.Sum256(data)
	return otogi.MediaHash{Algorithm: "sha256", Value: sum[:]}
}
