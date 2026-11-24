package openai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// listProvider is a provider whose base is a fake service answering
// GET /v1/models with the handler.
func listProvider(t *testing.T, answer func(http.ResponseWriter, *http.Request)) *Provider {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer key" {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusTeapot)
			return
		}
		answer(w, r)
	}))
	t.Cleanup(ts.Close)
	rec := &recorder{}
	p, err := New(Config{Name: "deepseek", BaseURL: ts.URL + "/v1/", APIKey: "key", Recorder: rec, Logger: quiet})
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

func TestListModelsReadsPlainIDs(t *testing.T) {
	t.Parallel()
	p := listProvider(t, status(http.StatusOK, `{"object":"list","data":[
		{"id":"gpt-5.5","object":"model","created":1,"owned_by":"openai"},
		{"id":"gpt-5.5-mini","object":"model","created":2,"owned_by":"openai"}]}`))
	got, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []llm.Model{{ID: "gpt-5.5"}, {ID: "gpt-5.5-mini"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("models = %+v, want %+v", got, want)
	}
}

func TestListModelsReadsDeepSeeksOptionalFields(t *testing.T) {
	t.Parallel()
	p := listProvider(t, status(http.StatusOK, `{"object":"list","data":[
		{"id":"deepseek-flash","object":"model","owned_by":"deepseek","name":"DeepSeek-V4.1-Flash","context_window":1048576,"max_output_tokens":393216,
		 "input_modalities":["text","image"],"output_modalities":["text"],"effort":{"supported_levels":["low","high","max"],"default_level":"high"}},
		{"id":"deepseek-v4-pro","object":"model","owned_by":"deepseek","input_modalities":["text"]}]}`))
	got, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []llm.Model{
		{ID: "deepseek-flash", DisplayName: "DeepSeek-V4.1-Flash", ContextTokens: 1048576, OutputTokens: 393216, InputModalities: []string{"text", "image"}, ThinkingLevels: []string{"low", "high", "max"}},
		{ID: "deepseek-v4-pro", InputModalities: []string{"text"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("models = %+v, want %+v", got, want)
	}
}

func TestListModelsFailsWithTheStatusAndTheServicesMessage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		status  int
		body    string
		message string
		kind    error
	}{
		{http.StatusUnauthorized, `{"error":{"message":"Authentication Fails, Your api key is invalid","type":"authentication_error"}}`, "Authentication Fails, Your api key is invalid", otogi.ErrInvalid},
		{http.StatusNotFound, `404 page not found`, "404 page not found", otogi.ErrInvalid},
		{http.StatusBadGateway, `upstream down`, "upstream down", otogi.ErrUnavailable},
	} {
		p := listProvider(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, tc.body)
		})
		_, err := p.ListModels(context.Background())
		if !errors.Is(err, tc.kind) {
			t.Errorf("HTTP %d: error %v, want it to wrap %v", tc.status, err, tc.kind)
		}
		if err == nil || !strings.Contains(err.Error(), fmt.Sprint("HTTP ", tc.status)) || !strings.Contains(err.Error(), tc.message) {
			t.Errorf("HTTP %d: error %v, want the status and %q", tc.status, err, tc.message)
		}
	}
}

func TestListModelsRefusesWhatIsNotAList(t *testing.T) {
	t.Parallel()
	p := listProvider(t, status(http.StatusOK, `<html>a gateway's page</html>`))
	if _, err := p.ListModels(context.Background()); !errors.Is(err, otogi.ErrInvalid) {
		t.Errorf("error %v, want one wrapping otogi.ErrInvalid", err)
	}
}
