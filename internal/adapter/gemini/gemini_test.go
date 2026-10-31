package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type recorder struct {
	mu      sync.Mutex
	records []otogi.Record
}

func (r *recorder) Record(_ context.Context, rec otogi.Record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, rec)
}

func (r *recorder) only(t *testing.T) otogi.Record {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.records) != 1 {
		t.Fatalf("%d records, want 1", len(r.records))
	}
	return r.records[0]
}

// server is a fake Interactions endpoint answering each request with the
// next response, and keeping the request bodies.
type server struct {
	mu        sync.Mutex
	bodies    []map[string]any
	responses []func(w http.ResponseWriter, r *http.Request)
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	s.mu.Lock()
	s.bodies = append(s.bodies, body)
	n := len(s.bodies)
	s.mu.Unlock()
	if r.URL.Path != "/v1beta/interactions" || r.Header.Get("x-goog-api-key") != "key" {
		http.Error(w, "unexpected "+r.URL.Path, http.StatusNotFound)
		return
	}
	s.responses[min(n, len(s.responses))-1](w, r)
}

func (s *server) requests() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bodies
}

// stream answers with server-sent events, one JSON object each.
func stream(events ...string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range events {
			var head struct {
				Type string `json:"event_type"`
			}
			_ = json.Unmarshal([]byte(e), &head)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", head.Type, e)
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}
}

func failWith(status int, body string, header ...string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		for i := 0; i+1 < len(header); i += 2 {
			w.Header().Set(header[i], header[i+1])
		}
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, body)
	}
}

const (
	thought   = `{"event_type":"step.start","index":0,"step":{"type":"thought"}}`
	signature = `{"event_type":"step.delta","index":0,"delta":{"type":"thought_signature","signature":"c2ln"}}`
	output    = `{"event_type":"step.start","index":1,"step":{"type":"model_output"}}`
	completed = `{"event_type":"interaction.completed","interaction":{"id":"i","status":"completed","usage":{"total_input_tokens":10,"total_cached_tokens":4,"total_output_tokens":6,"total_thought_tokens":3}}}`
)

func text(s string) string {
	data, _ := json.Marshal(s)
	return `{"event_type":"step.delta","index":1,"delta":{"type":"text","text":` + string(data) + `}}`
}

// pngBytes starts like a PNG file, so its type can be sniffed.
var pngBytes = []byte("\x89PNG\r\n\x1a\n0000")

func newProvider(t *testing.T, s *server) (*Provider, *recorder) {
	t.Helper()
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	rec := &recorder{}
	p, err := New(Config{APIKey: "key", BaseURL: srv.URL, Recorder: rec, Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	p.backoff = time.Millisecond
	return p, rec
}

func userTurn(parts ...otogi.Part) llm.Turn {
	msg := otogi.Message{}
	for i, p := range parts {
		p.ID = otogi.PartID(fmt.Sprint(i))
		msg.Parts = append(msg.Parts, p)
		msg.Roots = append(msg.Roots, p.ID)
	}
	return llm.Turn{Role: llm.RoleUser, Content: msg}
}

func TestGenerateStreamsRichTextAndImages(t *testing.T) {
	s := &server{responses: []func(http.ResponseWriter, *http.Request){stream(
		thought, signature, output, text("Hel"), text("lo **world**"),
		`{"event_type":"step.delta","index":1,"delta":{"type":"image","data":"aW1n","mime_type":"image/png"}}`,
		text("after"), completed,
	)}}
	p, rec := newProvider(t, s)
	ctx := otogi.ContextWithWork(context.Background(), otogi.Work{Module: "chat", Origin: "e1", Event: "e1", Destination: otogi.Destination{Platform: "telegram", Account: "main", Conversation: "user:7"}})
	var partials []string
	resp, err := p.Generate(ctx, llm.Request{
		Model: "gemini-test", System: "be brief", Thinking: "low", MaxOutput: 100,
		Turns: []llm.Turn{userTurn(
			otogi.Part{Kind: otogi.PartText, Text: "hi there", Annotations: []otogi.Annotation{{Offset: 0, Length: 2, Kind: otogi.AnnotationBold}}},
			otogi.Part{Kind: otogi.PartMedia, Media: &otogi.Media{Role: otogi.MediaImage, Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: pngBytes}}},
		)},
	}, func(m otogi.Message) error {
		partials = append(partials, fmt.Sprintf("%d:%s", len(m.Parts), m.PlainText()))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if got, want := strings.Join(partials, "|"), "1:Hel|1:Hello world|2:Hello world|3:Hello worldafter"; got != want {
		t.Errorf("partials %q, want %q", got, want)
	}
	parts := resp.Turn.Content.Parts
	if len(parts) != 3 || parts[0].Text != "Hello world" || len(parts[0].Annotations) != 1 || parts[0].Annotations[0].Kind != otogi.AnnotationBold ||
		parts[1].Media == nil || string(parts[1].Media.Source.Inline) != "img" || parts[1].Media.MediaType != "image/png" || parts[2].Text != "after" {
		t.Fatalf("reply parts %+v", parts)
	}
	if resp.Markdown != "Hello **world**\n\nafter" || resp.Finish != llm.FinishStop || resp.Usage != (llm.Usage{Input: 10, Cached: 4, Output: 6, Thoughts: 3}) {
		t.Fatalf("response %q %s %+v", resp.Markdown, resp.Finish, resp.Usage)
	}
	if got, want := string(resp.Turn.Native.Data), `[{"signature":"c2ln","type":"thought"},{"content":[{"text":"Hello **world**","type":"text"},{"data":"aW1n","mime_type":"image/png","type":"image"},{"text":"after","type":"text"}],"type":"model_output"}]`; got != want {
		t.Fatalf("native %s\nwant   %s", got, want)
	}

	body := s.requests()[0]
	got, _ := json.Marshal(body)
	for _, want := range []string{`"model":"gemini-test"`, `"store":false`, `"stream":true`, `"system_instruction":"be brief"`,
		`"generation_config":{"max_output_tokens":100,"thinking_level":"low"}`,
		`{"text":"**hi** there","type":"text"}`, `{"data":"iVBORw0KGgowMDAw","mime_type":"image/png","type":"image"}`} {
		if !strings.Contains(string(got), want) {
			t.Errorf("request lacks %s:\n%s", want, got)
		}
	}

	r := rec.only(t)
	payload, _ := json.Marshal(r.Payload)
	if r.Kind != "llm.completed" || r.Component != "llm:gemini" || r.Event != "e1" || r.Conversation != "user:7" ||
		!strings.Contains(string(payload), `"size":12`) || strings.Contains(string(payload), "iVBORw0KGgowMDAw") || !strings.Contains(string(payload), `"module":"chat"`) ||
		strings.Contains(string(payload), `"signature":"c2ln"`) || !strings.Contains(string(payload), `"signature":{"sha256"`) {
		t.Fatalf("record %+v\n%s", r, payload)
	}
}

func TestGenerateResendsItsOwnTurnsExactly(t *testing.T) {
	native := `[{"signature":"c2ln","type":"thought"},{"content":[{"text":"Hello","type":"text"}],"type":"model_output"}]`
	s := &server{responses: []func(http.ResponseWriter, *http.Request){stream(output, text("ok"), completed)}}
	p, _ := newProvider(t, s)
	_, err := p.Generate(context.Background(), llm.Request{Model: "m", Turns: []llm.Turn{
		userTurn(otogi.Part{Kind: otogi.PartText, Text: "hi"}),
		{Role: llm.RoleModel, Content: otogi.TextMessage("Hello"), Native: &llm.Native{Provider: Name, Data: json.RawMessage(native)}},
		{Role: llm.RoleModel, Content: otogi.TextMessage("from elsewhere"), Native: &llm.Native{Provider: "other", Data: json.RawMessage(`{}`)}},
		userTurn(otogi.Part{Kind: otogi.PartText, Text: "again"}),
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(s.requests()[0]["input"])
	want := `[{"content":[{"text":"hi","type":"text"}],"type":"user_input"},` + native[1:len(native)-1] +
		`,{"content":[{"text":"from elsewhere","type":"text"}],"type":"model_output"},{"content":[{"text":"again","type":"text"}],"type":"user_input"}]`
	if string(input) != want {
		t.Fatalf("input %s\nwant  %s", input, want)
	}
}

func TestGenerateRetries(t *testing.T) {
	for name, c := range map[string]struct {
		first     func(http.ResponseWriter, *http.Request)
		attempts  int
		wantErr   error
		wantInLog string
	}{
		"rate limit with a delay": {failWith(429, `{"error":{"code":429,"details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"0.02s"}]}}`), 2, nil, `"wait":"20ms"`},
		"retry-after header":      {failWith(503, `{"error":{"message":"overloaded"}}`, "Retry-After", "0"), 2, nil, `"status":503`},
		"daily quota":             {failWith(429, `{"error":{"message":"quota GenerateRequestsPerDayPerProject exceeded"}}`), 1, otogi.ErrUnavailable, "PerDay"},
		"bad request":             {failWith(400, `{"error":{"code":"invalid","message":"API key not valid"}}`), 1, otogi.ErrInvalid, "API key not valid"},
	} {
		t.Run(name, func(t *testing.T) {
			s := &server{responses: []func(http.ResponseWriter, *http.Request){c.first, stream(output, text("ok"), completed)}}
			p, rec := newProvider(t, s)
			_, err := p.Generate(context.Background(), llm.Request{Model: "m", Turns: []llm.Turn{userTurn(otogi.Part{Kind: otogi.PartText, Text: "hi"})}}, nil)
			if (c.wantErr == nil) != (err == nil) || (c.wantErr != nil && !errors.Is(err, c.wantErr)) {
				t.Fatalf("err %v, want %v", err, c.wantErr)
			}
			if c.wantErr != nil && !strings.Contains(err.Error(), c.wantInLog) {
				t.Errorf("error %q lacks the server's message", err)
			}
			if n := len(s.requests()); n != c.attempts {
				t.Fatalf("%d requests, want %d", n, c.attempts)
			}
			payload, _ := json.Marshal(rec.only(t).Payload)
			if !strings.Contains(string(payload), c.wantInLog) {
				t.Fatalf("record lacks %s:\n%s", c.wantInLog, payload)
			}
		})
	}
}

func TestGenerateRetriesAStreamCutMidReply(t *testing.T) {
	cut := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "event: step.start\ndata: %s\n\nevent: step.delta\ndata: %s\n\n", output, text("partial"))
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler) // drop the connection mid-stream
	}
	s := &server{responses: []func(http.ResponseWriter, *http.Request){cut, stream(output, text("whole"), completed)}}
	p, rec := newProvider(t, s)
	var partials []string
	resp, err := p.Generate(context.Background(), llm.Request{Model: "m", Turns: []llm.Turn{userTurn(otogi.Part{Kind: otogi.PartText, Text: "hi"})}}, func(m otogi.Message) error {
		partials = append(partials, m.PlainText())
		return nil
	})
	if err != nil || resp.Markdown != "whole" || strings.Join(partials, "|") != "partial|whole" || len(s.requests()) != 2 {
		t.Fatalf("resp %q, err %v, partials %q, requests %d", resp.Markdown, err, partials, len(s.requests()))
	}
	payload, _ := json.Marshal(rec.only(t).Payload)
	if !strings.Contains(string(payload), `"wait"`) {
		t.Fatalf("record lacks the retry:\n%s", payload)
	}
}

func TestGenerateStopsWhenPartialFails(t *testing.T) {
	s := &server{responses: []func(http.ResponseWriter, *http.Request){stream(output, text("a"), text("b"), completed)}}
	p, rec := newProvider(t, s)
	gone := errors.New("chat deleted")
	_, err := p.Generate(context.Background(), llm.Request{Model: "m", Turns: []llm.Turn{userTurn(otogi.Part{Kind: otogi.PartText, Text: "hi"})}}, func(otogi.Message) error { return gone })
	if !errors.Is(err, gone) || len(s.requests()) != 1 || rec.only(t).Kind != "llm.failed" {
		t.Fatalf("err %v, requests %d", err, len(s.requests()))
	}
}

func TestGenerateReportsBlocksAsAFinish(t *testing.T) {
	s := &server{responses: []func(http.ResponseWriter, *http.Request){failWith(400, `{"error":{"message":"Image generation blocked due to copyright/recitation.","code":"image_recitation"}}`)}}
	p, rec := newProvider(t, s)
	resp, err := p.Generate(context.Background(), llm.Request{Model: "m", Turns: []llm.Turn{userTurn(otogi.Part{Kind: otogi.PartText, Text: "draw"})}}, nil)
	if err != nil || resp.Finish != llm.FinishBlocked || resp.Reason != "image_recitation: Image generation blocked due to copyright/recitation." || len(s.requests()) != 1 {
		t.Fatalf("resp %+v, err %v, requests %d", resp, err, len(s.requests()))
	}
	if r := rec.only(t); r.Kind != "llm.completed" {
		t.Fatalf("record kind %s", r.Kind)
	}
}

func TestGenerateReportsIncompleteTurns(t *testing.T) {
	s := &server{responses: []func(http.ResponseWriter, *http.Request){stream(output, text("cut"), `{"event_type":"interaction.completed","interaction":{"id":"i","status":"incomplete"}}`)}}
	p, _ := newProvider(t, s)
	resp, err := p.Generate(context.Background(), llm.Request{Model: "m", Turns: []llm.Turn{userTurn(otogi.Part{Kind: otogi.PartText, Text: "hi"})}}, nil)
	if err != nil || resp.Finish != llm.FinishMaxTokens || resp.Reason != "incomplete" {
		t.Fatalf("resp %+v, err %v", resp, err)
	}
}

func TestGenerateRejectsUnsupportedParts(t *testing.T) {
	s := &server{}
	p, _ := newProvider(t, s)
	_, err := p.Generate(context.Background(), llm.Request{Model: "m", Turns: []llm.Turn{userTurn(otogi.Part{Kind: otogi.PartLocation, Location: &otogi.Location{}})}}, nil)
	if !errors.Is(err, otogi.ErrUnsupported) || len(s.requests()) != 0 {
		t.Fatalf("err %v, requests %d", err, len(s.requests()))
	}
}

func TestGenerateStopsOnCancel(t *testing.T) {
	stall := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "event: step.start\ndata: %s\n\n", output)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}
	s := &server{responses: []func(http.ResponseWriter, *http.Request){stall}}
	p, _ := newProvider(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := p.Generate(ctx, llm.Request{Model: "m", Turns: []llm.Turn{userTurn(otogi.Part{Kind: otogi.PartText, Text: "hi"})}}, nil)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 2*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(started))
	}
}

func TestEndpointsMayIncludeTheVersion(t *testing.T) {
	t.Parallel()
	for endpoint, want := range map[string][2]string{
		"https://generativelanguage.googleapis.com/v1beta":  {"https://generativelanguage.googleapis.com", "v1beta"},
		"https://generativelanguage.googleapis.com/v1beta/": {"https://generativelanguage.googleapis.com", "v1beta"},
		"https://gw.example/gemini/v1alpha2":                {"https://gw.example/gemini", "v1alpha2"},
		"https://gw.example/gemini":                         {"https://gw.example/gemini", ""},
		"https://v1.example":                                {"https://v1.example", ""},
		"":                                                  {"", ""},
	} {
		if base, version := splitVersion(endpoint); base != want[0] || version != want[1] {
			t.Errorf("splitVersion(%q) = %q, %q; want %q, %q", endpoint, base, version, want[0], want[1])
		}
	}

	s := &server{responses: []func(http.ResponseWriter, *http.Request){stream(output, text("ok"), completed)}}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	p, err := New(Config{APIKey: "key", BaseURL: srv.URL + "/v1beta", Recorder: &recorder{}, Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Generate(context.Background(), llm.Request{Model: "m", Turns: []llm.Turn{userTurn(otogi.Part{Kind: otogi.PartText, Text: "hi"})}}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateReturnsFunctionCalls(t *testing.T) {
	s := &server{responses: []func(http.ResponseWriter, *http.Request){stream(
		thought, signature,
		`{"event_type":"step.start","index":1,"step":{"id":"call_1","type":"function_call","name":"set_reminder","arguments":{}}}`,
		`{"event_type":"step.delta","index":1,"delta":{"type":"arguments_delta","arguments":"{\"delay\":\"10m\","}}`,
		`{"event_type":"step.delta","index":1,"delta":{"type":"arguments_delta","arguments":"\"text\":\"water\"}"}}`,
		`{"event_type":"step.stop","index":1}`,
		`{"event_type":"interaction.completed","interaction":{"id":"i","status":"requires_action"}}`,
	)}}
	p, _ := newProvider(t, s)
	resp, err := p.Generate(context.Background(), llm.Request{Model: "m", Turns: []llm.Turn{userTurn(otogi.Part{Kind: otogi.PartText, Text: "remind me"})}, Tools: []llm.ToolSpec{
		{Function: &llm.Function{Name: "set_reminder", Description: "remind", Parameters: json.RawMessage(`{"type":"object"}`)}},
		{Builtin: llm.BuiltinGoogleSearch},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Finish != llm.FinishToolCalls || len(resp.Turn.Calls) != 1 || resp.Turn.Calls[0].ID != "call_1" || string(resp.Turn.Calls[0].Args) != `{"delay":"10m","text":"water"}` {
		t.Fatalf("response %+v", resp)
	}
	if !strings.Contains(string(resp.Turn.Native.Data), `"arguments":{"delay":"10m","text":"water"},"id":"call_1","name":"set_reminder","type":"function_call"`) {
		t.Fatalf("native %s", resp.Turn.Native.Data)
	}
	tools, _ := json.Marshal(s.requests()[0]["tools"])
	if string(tools) != `[{"description":"remind","name":"set_reminder","parameters":{"type":"object"},"type":"function"},{"type":"google_search"}]` {
		t.Fatalf("tools %s", tools)
	}
}

func TestGenerateSendsFunctionResults(t *testing.T) {
	s := &server{responses: []func(http.ResponseWriter, *http.Request){stream(output, text("Done."), completed)}}
	p, _ := newProvider(t, s)
	native := `[{"arguments":{"a":1},"id":"c1","name":"add","type":"function_call"},{"arguments":{},"id":"c2","name":"fail","type":"function_call"}]`
	_, err := p.Generate(context.Background(), llm.Request{Model: "m", Turns: []llm.Turn{
		userTurn(otogi.Part{Kind: otogi.PartText, Text: "go"}),
		{Role: llm.RoleModel, Native: &llm.Native{Provider: Name, Data: json.RawMessage(native)}},
		{Role: llm.RoleTool, Results: []llm.Result{{CallID: "c1", Name: "add", Output: json.RawMessage(`{"sum":1}`)}, {CallID: "c2", Name: "fail", Error: "no such chat"}}},
		{Role: llm.RoleModel, Calls: []llm.Call{{ID: "c3", Name: "add", Args: json.RawMessage(`{"a":2}`)}}},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(s.requests()[0]["input"])
	for _, want := range []string{
		`{"call_id":"c1","name":"add","result":"{\"sum\":1}","type":"function_result"}`,
		`{"call_id":"c2","is_error":true,"name":"fail","result":"no such chat","type":"function_result"}`,
		`{"arguments":{"a":2},"id":"c3","name":"add","type":"function_call"}`,
	} {
		if !strings.Contains(string(input), want) {
			t.Errorf("input lacks %s:\n%s", want, input)
		}
	}
}

func TestGenerateKeepsSearchStepsAndSources(t *testing.T) {
	s := &server{responses: []func(http.ResponseWriter, *http.Request){stream(
		`{"event_type":"step.start","index":0,"step":{"id":"s1","type":"google_search_call","signature":""}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"google_search_call","signature":"c2ln","arguments":{"queries":["q"]}}}`,
		`{"event_type":"step.stop","index":0}`,
		`{"event_type":"step.start","index":1,"step":{"type":"model_output"}}`,
		`{"event_type":"step.delta","index":1,"delta":{"type":"text","text":"Kane won."}}`,
		`{"event_type":"step.delta","index":1,"delta":{"type":"text_annotation_delta","annotations":[{"type":"url_citation","url":"https://r/1","title":"uefa.com","start_index":0,"end_index":9},{"type":"url_citation","url":"https://r/1","title":"uefa.com"},{"type":"url_citation","url":"https://r/2","title":"bbc.com"}]}}`,
		completed,
	)}}
	p, _ := newProvider(t, s)
	resp, err := p.Generate(context.Background(), llm.Request{Model: "m", Turns: []llm.Turn{userTurn(otogi.Part{Kind: otogi.PartText, Text: "who won"})}, Tools: []llm.ToolSpec{{Builtin: llm.BuiltinGoogleSearch}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Sources) != 2 || resp.Sources[0] != (llm.Source{Title: "uefa.com", URL: "https://r/1"}) || resp.Turn.Content.PlainText() != "Kane won." {
		t.Fatalf("response %+v", resp)
	}
	if !strings.Contains(string(resp.Turn.Native.Data), `{"arguments":{"queries":["q"]},"id":"s1","signature":"c2ln","type":"google_search_call"}`) {
		t.Fatalf("native %s", resp.Turn.Native.Data)
	}
}

func TestGenerateRejectsUnknownBuiltins(t *testing.T) {
	s := &server{}
	p, _ := newProvider(t, s)
	_, err := p.Generate(context.Background(), llm.Request{Model: "m", Turns: []llm.Turn{userTurn(otogi.Part{Kind: otogi.PartText, Text: "hi"})}, Tools: []llm.ToolSpec{{Builtin: "maps"}}}, nil)
	if !errors.Is(err, otogi.ErrUnsupported) || len(s.requests()) != 0 {
		t.Fatalf("err %v", err)
	}
}
