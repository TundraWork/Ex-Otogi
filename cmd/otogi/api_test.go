package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/capture"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/management"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/internal/kernel"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// apiHarness serves the management API over the real runtime, with no
// module on, and returns its kernel and a function that makes a request and
// checks the status of the response.
func apiHarness(t *testing.T, log *slog.Logger) (*kernel.Kernel, func(method, path, ifMatch, body string, wantCode int) (http.Header, string)) {
	t.Helper()
	return apiHarnessIn(t, t.TempDir(), log)
}

// apiHarnessIn is apiHarness with its data in dir, to start again on.
func apiHarnessIn(t *testing.T, dir string, log *slog.Logger) (*kernel.Kernel, func(method, path, ifMatch, body string, wantCode int) (http.Header, string)) {
	t.Helper()
	ctx := context.Background()
	storage, err := modulestore.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	events, err := management.OpenEventLog(ctx, filepath.Join(dir, "eventlog.sqlite"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = events.Close() })
	settings := &moduleSettings{store: storage, kinds: moduleKinds, recorder: events, log: log, system: newSystem(new(slog.LevelVar), events, log)}
	settings.env = moduleEnv{llm: settings.system.provider}
	if err := settings.loadSections(ctx, Config{}); err != nil {
		t.Fatal(err)
	}
	k, err := kernel.New(kernel.Config{Storage: storage, Recorder: events}, nil, nil, capture.New(), log)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = k.Stop(ctx) })
	settings.kernel = k
	server, err := management.StartServer(management.ServerConfig{
		Addr: "127.0.0.1:0", Token: "secret", Events: events, Logger: log, Ready: func() bool { return true }, Shutdown: func() {}, Restart: func(string) {}, ConfigFile: "/etc/otogi/otogi.yaml",
		Status: func(context.Context) management.Status { return management.Status{} }, Modules: newModuleAPI(settings), System: newSystemAPI(settings), Secrets: newModuleAPI(settings),
		Media: func(context.Context, otogi.Destination, otogi.MediaSource) ([]byte, error) {
			return []byte("JPEG"), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Stop(ctx) })
	return k, func(method, path, ifMatch, body string, wantCode int) (http.Header, string) {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, method, "http://"+server.Addr()+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer secret")
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if ifMatch != "" {
			req.Header.Set("If-Match", ifMatch)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != wantCode {
			t.Fatalf("%s %s = %d %s, want %d", method, path, resp.StatusCode, data, wantCode)
		}
		return resp.Header, string(data)
	}
}

// The management API over the real runtime: settings saved and applied,
// allowlists, and the chats the bot has seen with their conversations.
func TestManagementAPI(t *testing.T) {
	ctx := context.Background()
	k, do := apiHarness(t, slog.New(slog.DiscardHandler))
	contains := func(what, body string, want ...string) {
		t.Helper()
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Fatalf("%s: %s lacks %s", what, body, w)
			}
		}
	}

	// Settings: read with their schema, saved against their revision.
	header, body := do("GET", "/api/modules/echo/settings", "", "", 200)
	contains("echo settings", body, `"revision":0`, `"schema":{`)
	if header.Get("ETag") != `"0"` {
		t.Fatalf("ETag %q", header.Get("ETag"))
	}
	_, body = do("PUT", "/api/modules/notes/settings", `"0"`, `{"max_notes": "many"}`, 422)
	contains("invalid notes", body, `"location":"max_notes"`)
	do("PUT", "/api/modules/notes/settings", "", `{}`, 422) // If-Match is required
	header, _ = do("PUT", "/api/modules/notes/settings", `"0"`, `{"max_notes": 5}`, 200)
	if header.Get("ETag") != `"1"` {
		t.Fatalf("ETag after saving %q", header.Get("ETag"))
	}
	do("PUT", "/api/modules/notes/settings", `"0"`, `{"max_notes": 6}`, 412)
	do("PUT", "/api/modules/nope/settings", `"0"`, `{}`, 404)
	do("PATCH", "/api/modules/echo", "", `{"enabled": true}`, 200)
	_, body = do("PATCH", "/api/modules/chat", "", `{"enabled": true}`, 422) // never configured
	contains("chat without settings", body, "profiles")
	t.Setenv("OTOGI_TEST_GEMINI_KEY", "value-gemini")
	do("PUT", "/api/system/llm/settings", `"0"`, `{"gemini": {"api_key": "${OTOGI_TEST_GEMINI_KEY}"}}`, 200)
	do("PUT", "/api/modules/chat/settings", `"0"`, `{"profiles": [{"name": "Luna", "keywords": ["luna"], "provider": "gemini", "model": "m"}]}`, 200)
	_, body = do("PATCH", "/api/modules/chat", "", `{"enabled": true}`, 200)
	contains("chat", body, `"running":true`, `"allowlists":[{"name":"Luna","title":"Luna"`)
	contains("chat ignores bots by default", body, `"ignore_bots":true`, `"handles":true`)
	_, body = do("PATCH", "/api/modules/chat", "", `{"ignore_bots": false}`, 200)
	contains("chat takes bots", body, `"ignore_bots":false`, `"running":true`)
	do("PATCH", "/api/modules/chat", "", `{}`, 422)
	do("PATCH", "/api/modules/nope", "", `{"ignore_bots": false}`, 404)
	_, body = do("GET", "/api/modules/notes/settings/revisions", "", "", 200)
	contains("notes history", body, `"revision":1`, `"by":"management API"`, `"more":false`)
	do("POST", "/api/modules/notes/settings/revisions/1/restore", `"1"`, "", 200)
	do("POST", "/api/modules/notes/settings/revisions/9/restore", `"2"`, "", 404)

	// A person's message, with a photo, and echo's answer.
	photo := otogi.Part{ID: "p1", Kind: otogi.PartMedia, Media: &otogi.Media{Role: otogi.MediaImage, MediaType: "image/jpeg", Source: otogi.MediaSource{Kind: otogi.MediaSourceOpaque}}}
	msg := otogi.TextMessage("hi")
	msg.Parts, msg.Roots = append(msg.Parts, photo), append(msg.Roots, "p1")
	done := make(chan otogi.Outcome, 1)
	err := k.Submit(ctx, otogi.Event{
		ID: "e1", Platform: "telegram", Account: "main", Conversation: otogi.Conversation{ID: "chat:1", Kind: otogi.ConversationGroup, Title: "Friends"},
		Actor: otogi.Actor{ID: "user:5", Name: "Alice"}, Action: otogi.ActionMessageCreated, Direction: otogi.DirectionIncoming, OccurredAt: time.Now(), Message: msg,
	}, func(o otogi.Outcome) { done <- o })
	if err != nil {
		t.Fatal(err)
	}
	<-done

	// Allowlists, with what the directory knows of each chat.
	do("PUT", "/api/modules/chat/allowlists/Luna/chats/telegram:chat:1", "", "", 204)
	do("PUT", "/api/modules/chat/allowlists/Luna/chats/G1", "", "", 400)
	do("PUT", "/api/modules/chat/allowlists/Mizuki/chats/telegram:chat:1", "", "", 404)
	_, body = do("GET", "/api/modules/chat/allowlists/Luna", "", "", 200)
	contains("Luna's chats", body, `"chat":"telegram:chat:1"`, `"added_by":"management API"`, `"title":"Friends"`)

	// Chats, their names, and their conversations.
	_, body = do("GET", "/api/chats", "", "", 200)
	contains("chats", body, `"id":"telegram:chat:1"`, `"lists":[{"module":"chat","list":"Luna"`)
	_, body = do("PATCH", "/api/chats/telegram:chat:1", "", `{"name": "Old friends"}`, 200)
	contains("named chat", body, `"name":"Old friends"`, `"days":[{"day":"`)
	do("GET", "/api/chats/telegram:chat:404", "", "", 404)
	_, body = do("GET", "/api/chats/telegram:chat:1/messages", "", "", 200)
	var page struct {
		Messages []management.ChatMessage `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 2 {
		t.Fatalf("messages %s", body)
	}
	answer, question := page.Messages[0], page.Messages[1]
	if !answer.Bot || answer.Module != "echo" || answer.Message.PlainText() != "echo: hi" || answer.Event != "e1" || len(answer.Records) == 0 || answer.Records[0].Kind != "module.completed" {
		t.Fatalf("the bot's message %+v", answer)
	}
	if question.Bot || question.Actor.Name != "Alice" || question.Message.PlainText() != "hi" {
		t.Fatalf("the person's message %+v", question)
	}
	_, media := do("GET", "/api/chats/telegram:chat:1/media/"+jsonInt(question.ID)+"/p1", "", "", 200)
	if !bytes.Equal([]byte(media), []byte("JPEG")) {
		t.Fatalf("media %q", media)
	}
	do("DELETE", "/api/modules/chat/allowlists/Luna/chats/telegram:chat:1", "", "", 204)

	_, body = do("GET", "/api/openapi.json", "", "", 200)
	contains("OpenAPI document", body, `"openapi":"3.1.0"`, `"/api/chats/{chat}/messages"`)
}

// Settings a module or a section refuses when it is built are refused at
// the fields they are about, as the schema's checks are, each with only its
// reason; settings refused for no field of theirs keep the reason alone.
func TestRefusedSettingsNameTheirFields(t *testing.T) {
	_, do := apiHarness(t, slog.New(slog.DiscardHandler))
	t.Setenv("OTOGI_TEST_GEMINI_KEY", "value-gemini")
	t.Setenv("OTOGI_TEST_QQ_SECRET", "value-qq")
	do("PUT", "/api/system/llm/settings", `"0"`, `{"gemini": {"api_key": "${OTOGI_TEST_GEMINI_KEY}"}}`, 200)
	notImage := filepath.Join(t.TempDir(), "sticker.txt")
	if err := os.WriteFile(notImage, []byte("not a picture"), 0o600); err != nil {
		t.Fatal(err)
	}
	const missing = "/nonexistent/chrome-headless-shell"
	profile := func(name, keyword, provider string) string {
		return `{"name": "` + name + `", "keywords": ["` + keyword + `"], "provider": "` + provider + `", "model": "m"}`
	}
	type refusal struct {
		Detail string `json:"detail"`
		Errors []struct {
			Location string `json:"location"`
			Message  string `json:"message"`
		} `json:"errors"`
	}
	refused := func(path, ifMatch, settings string) refusal {
		t.Helper()
		_, body := do("PUT", path, ifMatch, settings, 422)
		var r refusal
		if err := json.Unmarshal([]byte(body), &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	for _, c := range []struct {
		path, settings string
		want           []string // the fields, in order
	}{
		{"/api/modules/chat/settings", `{"profiles": [` + profile("Luna", "luna", "nope") + `, ` + profile("Luna", "moon", "gemini") + `, ` + profile("Mizuki", "LUNA", "gemini") + `], "tools": ["notes", "notes"],
			"people": [{"user": "qq user"}, {"user": "qq:user:1"}, {"user": "qq:user:1"}]}`,
			[]string{"profiles[0].provider", "profiles[1].name", "profiles[2].keywords[0]", "tools[1]", "people[0].user", "people[2].user"}},
		{"/api/modules/chat/settings", `{"profiles": [` + profile("tools:x", "luna", "gemini") + `]}`, []string{"profiles[0].name"}},
		{"/api/modules/media/settings", `{"steps": [{"provider": "nope", "model": "m"}, {"provider": "gemini", "model": "m"}, {"provider": "other", "model": "m"}]}`,
			[]string{"steps[0].provider", "steps[2].provider"}},
		{"/api/modules/twitter/settings", `{"chrome": "` + missing + `", "timezone": "Mars/Olympus_Mons", "translate": {"provider": "nope", "model": "m"}}`,
			[]string{"chrome", "timezone", "translate.provider"}},
		{"/api/modules/bilibili/settings", `{"chrome": "` + missing + `"}`, []string{"chrome"}},
		{"/api/modules/booth/settings", `{"chrome": "` + missing + `"}`, []string{"chrome"}},
		{"/api/modules/safety/settings", `{"sticker": "/nonexistent/sticker.png"}`, []string{"sticker"}},
		{"/api/modules/safety/settings", `{"sticker": "` + notImage + `"}`, []string{"sticker"}},
		{"/api/modules/todou/settings", `{"cli": "todou", "agents": {"Main": {"server": "https://todou.test", "token": "${OTOGI_TEST_UNSET}"}}}`, []string{"agents.Main"}},
		{"/api/modules/todou/settings", `{"cli": "todou", "agents": {"main": {"server": "https://todou.test", "token": "${OTOGI_TEST_UNSET}"}}}`, []string{"agents.main.token"}},
		{"/api/modules/decision/settings", `{"jev": {"api_key": "${OTOGI_TEST_UNSET}"}}`, []string{"jev.api_key"}},
		{"/api/system/llm/settings", `{"gemini": {"api_key": "${OTOGI_TEST_GEMINI_KEY}", "base_url": "ftp://gemini.test"},
			"openai": {"DeepSeek": {"base_url": "https://deepseek.test", "api_key": "${OTOGI_TEST_GEMINI_KEY}"}, "ds": {"base_url": "deepseek.test", "api_key": "${OTOGI_TEST_GEMINI_KEY}"}}}`,
			[]string{"gemini.base_url", "openai.DeepSeek", "openai.ds.base_url"}},
		{"/api/system/llm/settings", `{"openai": {"ds": {"base_url": "https://deepseek.test", "api_key": "${OTOGI_TEST_UNSET}"}}}`, []string{"openai.ds.api_key"}},
		{"/api/system/general/settings", `{"operators": ["alice", "telegram:user:1"], "command_prefixes": ["/", ""]}`, []string{"operators[0]", "command_prefixes[1]"}},
		{"/api/system/accounts/settings", `{"telegram": [{"account": "a", "app_id": 1, "app_hash": "${OTOGI_TEST_UNSET}"}, {"account": "a", "app_id": 2, "app_hash": "${OTOGI_TEST_UNSET}"}]}`,
			[]string{"telegram[1].account"}},
		{"/api/system/accounts/settings", `{"qq": [{"account": "bot", "app_id": "1", "app_secret": "${OTOGI_TEST_UNSET}"}]}`, []string{"qq[0].app_secret"}},
	} {
		ifMatch := `"0"`
		if strings.HasPrefix(c.path, "/api/system/llm/") {
			ifMatch = `"1"`
		}
		r := refused(c.path, ifMatch, c.settings)
		if strings.HasSuffix(r.Detail, ": invalid") {
			t.Errorf("%s: the detail ends in its error type: %q", c.path, r.Detail)
		}
		var where []string
		for _, e := range r.Errors {
			where = append(where, e.Location)
			if e.Message == "" || strings.HasSuffix(e.Message, ": invalid") || strings.HasPrefix(e.Message, e.Location) {
				t.Errorf("%s: at %s, the message %q", c.path, e.Location, e.Message)
			}
		}
		if strings.Join(where, " ") != strings.Join(c.want, " ") {
			t.Errorf("%s %s: refused at %q, want %q (%s)", c.path, c.settings, where, c.want, r.Detail)
		}
	}

	// An account renamed is refused at the list it is in.
	do("PUT", "/api/system/accounts/settings", `"0"`, `{"qq": [{"account": "bot", "app_id": "1", "app_secret": "${OTOGI_TEST_QQ_SECRET}"}]}`, 200)
	if r := refused("/api/system/accounts/settings", `"1"`, `{"qq": [{"account": "robot", "app_id": "1", "app_secret": "${OTOGI_TEST_QQ_SECRET}"}]}`); len(r.Errors) != 1 || r.Errors[0].Location != "qq" || strings.HasSuffix(r.Errors[0].Message, ": invalid") {
		t.Errorf("a renamed account refused at %+v", r.Errors)
	}

	// Refused for nothing in the settings: a module whose tools no running
	// module provides, and a provider the running chat module uses, gone.
	// The second is chat's field, which the language model settings have not.
	do("PUT", "/api/modules/chat/settings", `"0"`, `{"profiles": [`+profile("Luna", "luna", "gemini")+`]}`, 200)
	do("PATCH", "/api/modules/chat", "", `{"enabled": true}`, 200)
	if r := refused("/api/modules/chat/settings", `"2"`, `{"profiles": [`+profile("Luna", "luna", "gemini")+`], "tools": ["todou"]}`); len(r.Errors) != 0 || !strings.Contains(r.Detail, "todou") {
		t.Errorf("tools no module provides: %+v", r)
	}
	if r := refused("/api/system/llm/settings", `"1"`, `{}`); len(r.Errors) != 0 || !strings.Contains(r.Detail, "rebuild chat") || !strings.Contains(r.Detail, "profiles[0].provider") ||
		strings.HasSuffix(r.Detail, ": invalid") {
		t.Errorf("a provider in use removed: %+v", r)
	}
}

func jsonInt(n int64) string {
	data, _ := json.Marshal(n)
	return string(data)
}

// The secret routes over the real runtime. A secret's value is in no
// response, no record, and no log line, whatever the request looked like.
func TestSecretsAPI(t *testing.T) {
	var log bytes.Buffer
	_, request := apiHarness(t, slog.New(slog.NewTextHandler(&log, &slog.HandlerOptions{Level: slog.LevelDebug})))
	var answers []string
	do := func(method, path, ifMatch, body string, wantCode int) string {
		t.Helper()
		_, answer := request(method, path, ifMatch, body, wantCode)
		answers = append(answers, method+" "+path+": "+answer)
		return answer
	}
	contains := func(what, body string, want ...string) {
		t.Helper()
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Fatalf("%s: %s lacks %s", what, body, w)
			}
		}
	}
	const settings = `{"jev": {"api_key": "${OTOGI_TEST_JEV_KEY}"}}`

	contains("settings referring to a secret set nowhere", do("PUT", "/api/modules/decision/settings", `"0"`, settings, 422), "OTOGI_TEST_JEV_KEY")
	contains("a secret nothing uses yet", do("PUT", "/api/secrets/OTOGI_TEST_JEV_KEY", "", `{"value": "value-first"}`, 200), `"restarted":[]`)
	do("PUT", "/api/modules/decision/settings", `"0"`, settings, 200)
	do("PATCH", "/api/modules/decision", "", `{"enabled": true}`, 200)
	contains("a secret a running module uses", do("PUT", "/api/secrets/OTOGI_TEST_JEV_KEY", "", `{"value": "value-second"}`, 200), `"restarted":["decision"]`)
	contains("secrets", do("GET", "/api/secrets", "", "", 200), `"name":"OTOGI_TEST_JEV_KEY","source":"panel","by":"management API","at":"`, `"references":["decision"]`)

	// Requests that are refused do not get their values back.
	for _, c := range []struct {
		path, body string
		code       int
	}{
		{"/api/secrets/OTOGI_TEST_JEV_KEY", `{"value": ""}`, 422},
		{"/api/secrets/OTOGI_TEST_JEV_KEY", `{"value": ["value-in-a-list"]}`, 422},
		{"/api/secrets/OTOGI_TEST_JEV_KEY", `{"value": "value-with-more", "note": "value-noted"}`, 422},
		{"/api/secrets/OTOGI_TEST_JEV_KEY", `{"value": "value-cut-short`, 400},
		{"/api/secrets/OTOGI_TEST_JEV_KEY", `{"secret": "value-misnamed"}`, 422},
		{"/api/secrets/not-a-name", `{"value": "value-misplaced"}`, 422},
	} {
		do("PUT", c.path, "", c.body, c.code)
	}
	contains("deleting a secret in use", do("DELETE", "/api/secrets/OTOGI_TEST_JEV_KEY", "", "", 409), "decision")
	do("DELETE", "/api/secrets/OTOGI_TEST_NEVER_SET", "", "", 404)
	t.Setenv("OTOGI_TEST_JEV_KEY", "value-from-environment")
	contains("deleting a secret the environment has too", do("DELETE", "/api/secrets/OTOGI_TEST_JEV_KEY", "", "", 200), `"restarted":["decision"]`)
	contains("secrets", do("GET", "/api/secrets", "", "", 200), `{"name":"OTOGI_TEST_JEV_KEY","source":"environment","references":["decision"]}`)

	records := do("GET", "/api/events?kind=secret.changed", "", "", 200)
	contains("records", records, `"component":"secret:OTOGI_TEST_JEV_KEY"`, `"action":"set"`, `"action":"replaced"`, `"action":"deleted"`, `"by":"management API"`, `"restarted":["decision"]`)
	do("GET", "/api/events", "", "", 200)
	do("GET", "/api/modules/decision/settings", "", "", 200)
	do("GET", "/api/modules", "", "", 200)
	contains("the log", log.String(), "OTOGI_TEST_JEV_KEY")
	for _, text := range append(answers, log.String()) {
		if strings.Contains(text, "value-") {
			t.Fatalf("a secret's value is in %s", text)
		}
	}
}

// The section routes over the real runtime: the sections, their settings
// with a schema, saved against their revision, their history, and an
// earlier revision restored.
func TestSystemAPI(t *testing.T) {
	_, do := apiHarness(t, slog.New(slog.DiscardHandler))
	contains := func(what, body string, want ...string) {
		t.Helper()
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Fatalf("%s: %s lacks %s", what, body, w)
			}
		}
	}

	_, body := do("GET", "/api/system", "", "", 200)
	contains("sections", body, `{"id":"general","revision":0,"applies":"now","running":0}`, `{"id":"llm","revision":0,"applies":"now","running":0}`,
		`{"id":"accounts","revision":0,"applies":"restart","running":0}`, `{"id":"process","revision":0,"applies":"restart","running":0}`)
	header, body := do("GET", "/api/system/general/settings", "", "", 200)
	contains("general settings", body, `"module":"system:general"`, `"revision":0`, `"settings":{}`, `"schema":{`, `"log_level"`, `"x-order":["operators","command_prefixes","eventlog_retention","log_level"]`)
	if header.Get("ETag") != `"0"` {
		t.Fatalf("ETag %q", header.Get("ETag"))
	}
	_, body = do("GET", "/api/system/llm/settings", "", "", 200)
	contains("language model settings", body, `"format":"secret"`, `"$defs"`)

	do("PUT", "/api/system/general/settings", "", `{}`, 422) // If-Match is required
	_, body = do("PUT", "/api/system/general/settings", `"0"`, `{"log_level": "loud", "eventlog_retention": "soon"}`, 422)
	contains("invalid general settings", body, `"location":"log_level"`, `"location":"eventlog_retention"`)
	_, body = do("PUT", "/api/system/llm/settings", `"0"`, `{"gemini": {"api_key": "value-pasted"}}`, 422)
	contains("a pasted key", body, `"location":"gemini.api_key"`)
	if strings.Contains(body, "value-") {
		t.Fatalf("a refused key is in the answer: %s", body)
	}
	header, body = do("PUT", "/api/system/general/settings", `"0"`, `{"log_level": "debug"}`, 200)
	contains("saved", body, `"module":"system:general"`, `"revision":1`, `"settings":{"log_level":"debug"}`, `"by":"management API"`)
	if header.Get("ETag") != `"1"` {
		t.Fatalf("ETag after saving %q", header.Get("ETag"))
	}
	do("PUT", "/api/system/general/settings", `"0"`, `{"log_level": "warn"}`, 412)
	do("PUT", "/api/system/general/settings", `"1"`, `{"log_level": "warn"}`, 200)
	_, body = do("GET", "/api/system/general/settings/revisions", "", "", 200)
	contains("history", body, `"revision":2`, `"revision":1`, `"more":false`)
	do("POST", "/api/system/general/settings/revisions/1/restore", `"1"`, "", 412)
	header, body = do("POST", "/api/system/general/settings/revisions/1/restore", `"2"`, "", 200)
	contains("restored", body, `"revision":3`, `"settings":{"log_level":"debug"}`)
	if header.Get("ETag") != `"3"` {
		t.Fatalf("ETag after restoring %q", header.Get("ETag"))
	}
	do("POST", "/api/system/general/settings/revisions/9/restore", `"3"`, "", 404)
	_, body = do("GET", "/api/system", "", "", 200)
	contains("sections", body, `{"id":"general","revision":3,"applies":"now","running":3}`)

	// The sections that apply on restart: saved, and pending until then.
	_, body = do("GET", "/api/system/accounts/settings", "", "", 200)
	contains("accounts settings", body, `"module":"system:accounts"`, `"format":"secret"`, `"x-order":["telegram","qq"]`)
	_, body = do("PUT", "/api/system/accounts/settings", `"0"`, `{"qq": [{"account": "bot", "app_id": "1", "app_secret": "value-pasted"}]}`, 422)
	contains("a pasted app secret", body, `"location":"qq[0].app_secret"`)
	if strings.Contains(body, "value-") {
		t.Fatalf("a refused secret is in the answer: %s", body)
	}
	do("PUT", "/api/system/process/settings", `"0"`, `{"workers": -1}`, 422)
	do("PUT", "/api/system/process/settings", `"0"`, `{"handler_timeout": "90s"}`, 200)
	_, body = do("GET", "/api/system", "", "", 200)
	contains("sections with a restart pending", body, `{"id":"process","revision":1,"applies":"restart","running":0}`)

	// What stays in the config file, without the token this API is used with.
	_, body = do("GET", "/api/system/startup", "", "", 200)
	contains("startup settings", body, `"config_file":"/etc/otogi/otogi.yaml"`, `"token_set":true`)
	if strings.Contains(body, `"secret"`) {
		t.Fatalf("the token is in the startup settings: %s", body)
	}

	// Sections are not modules, and modules are not sections.
	for _, path := range []string{"/api/system/startup/settings", "/api/system/echo/settings", "/api/modules/system:general/settings", "/api/system/echo/settings/revisions", "/api/modules/system:general/settings/revisions"} {
		do("GET", path, "", "", 404)
	}
	do("PUT", "/api/modules/system:general/settings", `"3"`, `{}`, 404)
	do("POST", "/api/modules/system:general/settings/revisions/1/restore", `"3"`, "", 404)
	_, body = do("GET", "/api/modules", "", "", 200)
	if strings.Contains(body, "system:") {
		t.Fatalf("a section is listed as a module: %s", body)
	}

	_, body = do("GET", "/api/events?kind=system.reconfigured", "", "", 200)
	contains("records", body, `"component":"system:general"`, `"revision":3`, `"by":"management API"`, `"restarted":[]`)
	_, body = do("GET", "/api/openapi.json", "", "", 200)
	contains("OpenAPI document", body, `"/api/system/{section}/settings/revisions/{revision}/restore"`)
}
