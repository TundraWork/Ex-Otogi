package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// listService is a provider's model list: Gemini's GET /v1beta/models, or
// an OpenAI-compatible GET /models. It counts the requests, and answers 401
// while failing is set.
type listService struct {
	url      string
	requests atomic.Int32
	failing  atomic.Bool
}

func newListService(t *testing.T, path, list string) *listService {
	t.Helper()
	s := &listService{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path != path:
			http.Error(w, "no route "+r.URL.Path, http.StatusNotFound)
		case s.failing.Load():
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = fmt.Fprint(w, `{"error":{"message":"Authentication Fails, Your api key is invalid"}}`)
		default:
			_, _ = fmt.Fprint(w, list)
		}
	}))
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

// The model lists over the real runtime: fetched only when asked, saved
// with who asked, recorded as llm.models without the key, kept when a fetch
// fails, deleted with their provider, and still there after a restart.
func TestLLMModelsAPI(t *testing.T) {
	t.Setenv("OTOGI_TEST_GEMINI_KEY", "value-gemini-key")
	t.Setenv("OTOGI_TEST_DEEPSEEK_KEY", "value-deepseek-key")
	gemini := newListService(t, "/v1beta/models", `{"models":[
		{"name":"models/gemini-3.5-flash","displayName":"Gemini 3.5 Flash","inputTokenLimit":1048576,"outputTokenLimit":65536,"supportedGenerationMethods":["generateContent"]},
		{"name":"models/text-embedding-004","supportedGenerationMethods":["embedContent"]}]}`)
	deepseek := newListService(t, "/models", `{"object":"list","data":[
		{"id":"deepseek-flash","object":"model","owned_by":"deepseek","input_modalities":["text","image"],"effort":{"supported_levels":["low","high","max"]}}]}`)
	both := `{"gemini": {"api_key": "${OTOGI_TEST_GEMINI_KEY}", "base_url": "` + gemini.url + `/v1beta"},
		"openai": {"deepseek": {"base_url": "` + deepseek.url + `", "api_key": "${OTOGI_TEST_DEEPSEEK_KEY}"}}}`
	contains := func(what, body string, want ...string) {
		t.Helper()
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Fatalf("%s: %s lacks %s", what, body, w)
			}
		}
	}
	var seen []string // every answer, to look for the keys in
	dir := t.TempDir()

	t.Run("fetch", func(t *testing.T) {
		_, request := apiHarnessIn(t, dir, slog.New(slog.DiscardHandler))
		do := func(method, path, ifMatch, body string, wantCode int) string {
			t.Helper()
			_, out := request(method, path, ifMatch, body, wantCode)
			seen = append(seen, out)
			return out
		}
		if body := do("GET", "/api/system/llm/providers", "", "", 200); body != "[]\n" {
			t.Fatalf("providers before any are set: %s", body)
		}
		do("PUT", "/api/system/llm/settings", `"0"`, both, 200)
		body := do("GET", "/api/system/llm/providers", "", "", 200)
		contains("providers never fetched", body, `{"name":"deepseek","kind":"openai","models":[]}`, `{"name":"gemini","kind":"gemini","models":[]}`)
		if n := gemini.requests.Load() + deepseek.requests.Load(); n != 0 {
			t.Fatalf("%d list requests before any fetch was asked for", n)
		}

		body = do("POST", "/api/system/llm/providers/gemini/models", "", "", 200)
		contains("gemini fetched", body, `"name":"gemini"`, `"kind":"gemini"`, `"fetched_by":"management API"`, `"fetched_at":"`,
			`"models":[{"id":"gemini-3.5-flash","display_name":"Gemini 3.5 Flash","input_tokens":1048576,"output_tokens":65536}]`)
		deepseek.failing.Store(true)
		body = do("POST", "/api/system/llm/providers/deepseek/models", "", "", 502)
		contains("a failed fetch", body, "HTTP 401", "Authentication Fails")
		deepseek.failing.Store(false)
		do("POST", "/api/system/llm/providers/deepseek/models", "", "", 200)
		deepseek.failing.Store(true)
		do("POST", "/api/system/llm/providers/deepseek/models", "", "", 502)
		body = do("GET", "/api/system/llm/providers", "", "", 200)
		contains("the list a failed fetch leaves", body, `"models":[{"id":"deepseek-flash","input_modalities":["text","image"],"thinking_levels":["low","high","max"]}]`,
			`"models":[{"id":"gemini-3.5-flash"`)
		do("POST", "/api/system/llm/providers/openai/models", "", "", 404)

		body = do("GET", "/api/events?kind=llm.models", "", "", 200)
		contains("records", body, `"component":"llm:gemini"`, `"component":"llm:deepseek"`, `"outcome":"error"`, `"outcome":"ok"`,
			`"by":"management API"`, `"provider":"deepseek"`, `"models":[{"id":"gemini-3.5-flash"`, `HTTP 401: Authentication Fails`)
		if n := strings.Count(body, `"kind":"llm.models"`); n != 4 {
			t.Fatalf("%d llm.models records for 4 fetches: %s", n, body)
		}

		// A provider removed takes its list with it: added back, it has none.
		do("PUT", "/api/system/llm/settings", `"1"`, `{"gemini": {"api_key": "${OTOGI_TEST_GEMINI_KEY}", "base_url": "`+gemini.url+`/v1beta"}}`, 200)
		body = do("GET", "/api/system/llm/providers", "", "", 200)
		if strings.Contains(body, "deepseek") {
			t.Fatalf("a removed provider is listed: %s", body)
		}
		do("PUT", "/api/system/llm/settings", `"2"`, both, 200)
		body = do("GET", "/api/system/llm/providers", "", "", 200)
		contains("a provider added back", body, `{"name":"deepseek","kind":"openai","models":[]}`, `"models":[{"id":"gemini-3.5-flash"`)
	})

	t.Run("after a restart", func(t *testing.T) {
		before := gemini.requests.Load() + deepseek.requests.Load()
		_, request := apiHarnessIn(t, dir, slog.New(slog.DiscardHandler))
		_, body := request("GET", "/api/system/llm/providers", "", "", 200)
		seen = append(seen, body)
		contains("providers after a restart", body, `"fetched_by":"management API"`, `"models":[{"id":"gemini-3.5-flash"`)
		if n := gemini.requests.Load() + deepseek.requests.Load(); n != before {
			t.Fatalf("starting fetched lists: %d requests", n-before)
		}
	})

	for _, text := range seen {
		if strings.Contains(text, "value-") {
			t.Fatalf("a key is in %s", text)
		}
	}
}
