package gemini

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// modelsServer is a fake GET v1beta/models: pages, keyed by the page token
// that asks for them, the first under "".
type modelsServer struct {
	mu     sync.Mutex
	tokens []string
	pages  map[string]string
}

func (s *modelsServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.Path != "/v1beta/models" || r.Header.Get("x-goog-api-key") != "key" {
		http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		return
	}
	token := r.URL.Query().Get("pageToken")
	s.mu.Lock()
	s.tokens = append(s.tokens, token)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprint(w, s.pages[token])
}

func listProvider(t *testing.T, h http.Handler) *Provider {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	rec := &recorder{}
	p, err := New(Config{APIKey: "key", BaseURL: srv.URL + "/v1beta", Recorder: rec, Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if len(rec.records) > 0 {
			t.Errorf("listing recorded %d records; the caller records it", len(rec.records))
		}
	})
	return p
}

func TestListModelsReadsEveryPageAndKeepsGenerationModels(t *testing.T) {
	t.Parallel()
	s := &modelsServer{pages: map[string]string{
		"": `{"models":[
			{"name":"models/gemini-3.5-flash","displayName":"Gemini 3.5 Flash","inputTokenLimit":1048576,"outputTokenLimit":65536,"supportedGenerationMethods":["generateContent","countTokens"],"thinking":true},
			{"name":"models/text-embedding-004","displayName":"Text Embedding 004","supportedGenerationMethods":["embedContent"]}
		],"nextPageToken":"p2"}`,
		"p2": `{"models":[
			{"name":"models/gemini-3.5-pro","supportedGenerationMethods":["generateContent"]},
			{"name":"models/aqa","supportedGenerationMethods":["generateAnswer"]}
		]}`,
	}}
	got, err := listProvider(t, s).ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []llm.Model{
		{ID: "gemini-3.5-flash", DisplayName: "Gemini 3.5 Flash", InputTokens: 1048576, OutputTokens: 65536},
		{ID: "gemini-3.5-pro"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("models = %+v, want %+v", got, want)
	}
	if !reflect.DeepEqual(s.tokens, []string{"", "p2"}) {
		t.Errorf("page tokens asked = %q, want the first page, then p2", s.tokens)
	}
}

func TestListModelsFailsWithTheStatusAndTheProvidersMessage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		status int
		body   string
		kind   error
	}{
		{http.StatusUnauthorized, `{"error":{"code":401,"message":"API key not valid.","status":"UNAUTHENTICATED"}}`, otogi.ErrInvalid},
		{http.StatusNotFound, `{"error":{"code":404,"message":"no such route"}}`, otogi.ErrInvalid},
		{http.StatusServiceUnavailable, `{"error":{"code":503,"message":"overloaded"}}`, otogi.ErrUnavailable},
	} {
		p := listProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tc.status)
			_, _ = fmt.Fprint(w, tc.body)
		}))
		_, err := p.ListModels(context.Background())
		if !errors.Is(err, tc.kind) {
			t.Errorf("HTTP %d: error %v, want it to wrap %v", tc.status, err, tc.kind)
		}
		if err == nil || !strings.Contains(err.Error(), fmt.Sprint("HTTP ", tc.status)) || !strings.Contains(err.Error(), tc.body) {
			t.Errorf("HTTP %d: error %v, want the status and the body", tc.status, err)
		}
		// Operators read the text: it ends with what the service said, not the kind's word.
		if err != nil && strings.HasSuffix(err.Error(), ": "+tc.kind.Error()) {
			t.Errorf("HTTP %d: error %q ends with %q", tc.status, err, tc.kind)
		}
	}
}
