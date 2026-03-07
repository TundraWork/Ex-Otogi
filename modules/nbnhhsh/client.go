package nbnhhsh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxResponseBodyBytes = 1 << 20

type guessClient interface {
	Guess(ctx context.Context, text string) ([]guessResult, error)
}

type guessResult struct {
	Name      string   `json:"name"`
	Trans     []string `json:"trans"`
	Inputting []string `json:"inputting"`
}

type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type clientErrorKind string

const (
	clientErrorKindTemporary clientErrorKind = "temporary"
	clientErrorKindInvalid   clientErrorKind = "invalid_response"
)

type clientError struct {
	kind clientErrorKind
	err  error
}

func (e *clientError) Error() string {
	if e == nil || e.err == nil {
		return "nbnhhsh client error"
	}

	return e.err.Error()
}

func (e *clientError) Unwrap() error {
	if e == nil {
		return nil
	}

	return e.err
}

func newHTTPGuessClient(apiURL string, requestTimeout time.Duration) guessClient {
	return &httpGuessClient{
		apiURL:         apiURL,
		requestTimeout: requestTimeout,
		doer:           &http.Client{},
	}
}

type httpGuessClient struct {
	apiURL         string
	requestTimeout time.Duration
	doer           httpDoer
}

func (c *httpGuessClient) Guess(ctx context.Context, text string) ([]guessResult, error) {
	if strings.TrimSpace(text) == "" {
		return nil, &clientError{
			kind: clientErrorKindInvalid,
			err:  fmt.Errorf("guess request: text is required"),
		}
	}

	requestCtx := ctx
	cancel := func() {}
	if c.requestTimeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, c.requestTimeout)
	}
	defer cancel()

	payload, err := json.Marshal(struct {
		Text string `json:"text"`
	}{
		Text: text,
	})
	if err != nil {
		return nil, &clientError{
			kind: clientErrorKindInvalid,
			err:  fmt.Errorf("guess request marshal payload: %w", err),
		}
	}

	req, err := http.NewRequestWithContext(
		requestCtx,
		http.MethodPost,
		c.apiURL,
		bytes.NewReader(payload),
	)
	if err != nil {
		return nil, &clientError{
			kind: clientErrorKindInvalid,
			err:  fmt.Errorf("guess request build request: %w", err),
		}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, &clientError{
			kind: clientErrorKindTemporary,
			err:  fmt.Errorf("guess request send: %w", err),
		}
	}

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	closeErr := resp.Body.Close()
	if readErr != nil {
		return nil, &clientError{
			kind: clientErrorKindInvalid,
			err:  fmt.Errorf("guess request read response: %w", readErr),
		}
	}
	if closeErr != nil {
		return nil, &clientError{
			kind: clientErrorKindInvalid,
			err:  fmt.Errorf("guess request close response: %w", closeErr),
		}
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 160 {
			snippet = snippet[:160]
		}

		err := fmt.Errorf("guess request unexpected status %s", resp.Status)
		if snippet != "" {
			err = fmt.Errorf("%w: %s", err, snippet)
		}

		return nil, &clientError{
			kind: clientErrorKindTemporary,
			err:  err,
		}
	}

	var parsed []guessResult
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, &clientError{
			kind: clientErrorKindInvalid,
			err:  fmt.Errorf("guess request parse response: %w", err),
		}
	}

	return normalizeGuessResults(parsed), nil
}

func normalizeGuessResults(results []guessResult) []guessResult {
	if len(results) == 0 {
		return nil
	}

	normalized := make([]guessResult, 0, len(results))
	for _, result := range results {
		item := guessResult{
			Name:      strings.TrimSpace(result.Name),
			Trans:     normalizeResultStrings(result.Trans),
			Inputting: normalizeResultStrings(result.Inputting),
		}
		if item.Name == "" {
			continue
		}

		normalized = append(normalized, item)
	}

	return normalized
}

func normalizeResultStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}

		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}

	return normalized
}

func isTemporaryClientError(err error) bool {
	var target *clientError
	return errors.As(err, &target) && target.kind == clientErrorKindTemporary
}
