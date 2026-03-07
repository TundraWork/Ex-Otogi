package nbnhhsh

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestHTTPGuessClientGuess(t *testing.T) {
	t.Parallel()

	client := &httpGuessClient{
		apiURL:         "https://example.com/api/nbnhhsh/guess",
		requestTimeout: 2 * time.Second,
		doer: doerFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", req.Method)
			}
			if req.URL.String() != "https://example.com/api/nbnhhsh/guess" {
				t.Fatalf("url = %s, want example endpoint", req.URL.String())
			}
			if contentType := req.Header.Get("Content-Type"); contentType != "application/json" {
				t.Fatalf("content-type = %q, want application/json", contentType)
			}
			if accept := req.Header.Get("Accept"); accept != "application/json" {
				t.Fatalf("accept = %q, want application/json", accept)
			}
			if userAgent := req.Header.Get("User-Agent"); userAgent != "" {
				t.Fatalf("user-agent = %q, want empty", userAgent)
			}
			if _, ok := req.Context().Deadline(); !ok {
				t.Fatal("expected request context to carry a deadline")
			}

			body, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read request body: %v", err)
			}
			if closeErr := req.Body.Close(); closeErr != nil {
				t.Fatalf("close request body: %v", closeErr)
			}
			if string(body) != `{"text":"yyds,xswl"}` {
				t.Fatalf("request body = %s, want compact json payload", string(body))
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body: io.NopCloser(strings.NewReader(
					`[
						{"name":"yyds","trans":[" 永远的神 ","永远的神",""]},
						{"name":"xswl","inputting":[" 笑死我了 ","笑死我了"]}
					]`,
				)),
			}, nil
		}),
	}

	results, err := client.Guess(context.Background(), "yyds,xswl")
	if err != nil {
		t.Fatalf("Guess() unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("result count = %d, want 2", len(results))
	}
	if results[0].Name != "yyds" || strings.Join(results[0].Trans, ",") != "永远的神" {
		t.Fatalf("first result = %+v, want normalized yyds translation", results[0])
	}
	if results[1].Name != "xswl" || strings.Join(results[1].Inputting, ",") != "笑死我了" {
		t.Fatalf("second result = %+v, want normalized xswl guesses", results[1])
	}
}

func TestHTTPGuessClientGuessErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		text          string
		doer          httpDoer
		wantTemporary bool
		wantErrSubstr string
	}{
		{
			name: "empty text rejected",
			text: "",
			doer: doerFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("doer should not be called for empty text")
				return nil, nil
			}),
			wantErrSubstr: "text is required",
		},
		{
			name: "transport error is temporary",
			text: "yyds",
			doer: doerFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("network down")
			}),
			wantTemporary: true,
			wantErrSubstr: "network down",
		},
		{
			name: "non-2xx status is temporary",
			text: "yyds",
			doer: doerFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusBadGateway,
					Status:     "502 Bad Gateway",
					Body:       io.NopCloser(strings.NewReader("bad gateway")),
				}, nil
			}),
			wantTemporary: true,
			wantErrSubstr: "502 Bad Gateway",
		},
		{
			name: "invalid json is not temporary",
			text: "yyds",
			doer: doerFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Body:       io.NopCloser(strings.NewReader("{not-json")),
				}, nil
			}),
			wantErrSubstr: "parse response",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			client := &httpGuessClient{
				apiURL:         "https://example.com/api/nbnhhsh/guess",
				requestTimeout: time.Second,
				doer:           testCase.doer,
			}

			_, err := client.Guess(context.Background(), testCase.text)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), testCase.wantErrSubstr) {
				t.Fatalf("error = %v, want substring %q", err, testCase.wantErrSubstr)
			}
			if gotTemporary := isTemporaryClientError(err); gotTemporary != testCase.wantTemporary {
				t.Fatalf("isTemporaryClientError(%v) = %v, want %v", err, gotTemporary, testCase.wantTemporary)
			}
		})
	}
}

type doerFunc func(req *http.Request) (*http.Response, error)

func (f doerFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}
