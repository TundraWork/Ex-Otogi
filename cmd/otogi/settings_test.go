package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/capture"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/management"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/internal/kernel"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

type records struct {
	mu   sync.Mutex
	list []otogi.Record
}

func (r *records) Record(_ context.Context, rec otogi.Record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.list = append(r.list, rec)
}

type silentModel struct{}

func (silentModel) Generate(context.Context, llm.Request, func(otogi.Message) error) (llm.Response, error) {
	return llm.Response{}, nil
}

// settingsHarness runs a kernel built from the stored settings, as run does.
func settingsHarness(t *testing.T, storage *modulestore.Storage) (*moduleSettings, *records) {
	t.Helper()
	rec := &records{}
	s := &moduleSettings{store: storage, kinds: moduleKinds, recorder: rec, log: slog.New(slog.DiscardHandler), env: moduleEnv{
		llm: func(string) (llm.Provider, error) { return silentModel{}, nil },
	}}
	modules, err := s.enabled(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	k, err := kernel.New(kernel.Config{Storage: storage}, modules, nil, capture.New(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = k.Stop(context.Background()) })
	s.kernel = k
	return s, rec
}

func running(s *moduleSettings) []otogi.ModuleID {
	var ids []otogi.ModuleID
	for _, d := range s.kernel.Descriptors() {
		ids = append(ids, d.ID)
	}
	return ids
}

// Saving a revision applies it to the running kernel; a module holding
// another's services is rebuilt with it; and the next start runs what the
// revisions say.
func TestModuleSettingsRevisions(t *testing.T) {
	ctx := context.Background()
	storage, err := modulestore.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	s, rec := settingsHarness(t, storage)
	on, off := true, false

	if _, err := s.save(ctx, "notes", 0, &on, json.RawMessage(`{"max_notes": 5}`), "Tundra's iPhone"); err != nil {
		t.Fatal(err)
	}
	chat := json.RawMessage(`{"profiles": [{"name": "Luna", "keywords": ["luna"], "provider": "gemini", "model": "m"}], "tools": ["notes"]}`)
	if _, err := s.save(ctx, "chat", 0, &on, chat, "Tundra's iPhone"); err != nil {
		t.Fatal(err)
	}
	if got := running(s); !slices.Equal(got, []otogi.ModuleID{"chat", "notes"}) {
		t.Fatalf("running %v", got)
	}
	if _, err := s.save(ctx, "notes", 0, &on, json.RawMessage(`{}`), "x"); !errors.As(err, new(*management.StaleRevisionError)) {
		t.Fatalf("saving over a stale revision: %v", err)
	}
	r, err := s.save(ctx, "notes", 1, nil, nil, "x") // the same settings again
	if err != nil {
		t.Fatal(err)
	}
	if r.Revision != 2 || string(r.Settings) != `{"max_notes":5}` {
		t.Fatalf("revision %+v", r)
	}
	last := rec.list[len(rec.list)-1]
	if p := last.Payload.(map[string]any); last.Kind != "module.reconfigured" || !slices.Equal(p["restarted"].([]otogi.ModuleID), []otogi.ModuleID{"chat"}) {
		t.Fatalf("record %+v", last)
	}
	if _, err := s.save(ctx, "notes", 2, &off, nil, "x"); err == nil || !strings.Contains(err.Error(), "chat") {
		t.Fatalf("turning off notes, which chat uses: %v", err)
	}
	if _, err := s.save(ctx, "chat", 1, &off, nil, "x"); err != nil {
		t.Fatal(err)
	}
	if got := running(s); !slices.Equal(got, []otogi.ModuleID{"notes"}) {
		t.Fatalf("running %v", got)
	}

	modules, err := s.enabled(ctx)
	if err != nil || len(modules) != 1 || modules[0].Descriptor().ID != "notes" {
		t.Fatalf("enabled after restart: %v, %v", modules, err)
	}
	if revs, _ := storage.SettingsRevisions(ctx, "notes", 3, 10); len(revs) != 2 || revs[0].Revision != 2 || revs[0].By != "x" || time.Since(revs[0].At) > time.Minute {
		t.Fatalf("history %+v", revs)
	}
}

// Settings that break their schema are refused field by field, before the
// module is built; a secret must be a reference.
func TestModuleSettingsValidation(t *testing.T) {
	ctx := context.Background()
	storage, err := modulestore.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	s, _ := settingsHarness(t, storage)
	for module, c := range map[otogi.ModuleID]struct{ settings, want, secret string }{
		"chat":      {`{"profiles": [{"name": "Luna", "provider": "gemini", "model": "m", "temperature": 3}], "colour": "red"}`, "profiles[0].temperature;unexpected property", ""},
		"todou":     {`{"cli": "todou", "agents": {"neptune": {"server": "https://todou.test", "token": "todou_pat_x"}}}`, "agents.neptune.token", "todou_pat_x"},
		"reminders": {`{"max_delay": "soon"}`, "max_delay", ""},
		"decision":  {`{"jev": {"api_key": "ts_pasted_key"}}`, "jev.api_key", "ts_pasted_key"},
		"media":     {`{"steps": [{"provider": "gemini", "model": "", "thinking": "max"}]}`, "steps[0].model;steps[0].thinking", ""},
	} {
		_, err := s.save(ctx, module, 0, nil, json.RawMessage(c.settings), "x")
		if !errors.Is(err, otogi.ErrInvalid) {
			t.Fatalf("%s: %v, want invalid settings", module, err)
		}
		if got := strings.Join(refusedAt(err), ";"); !strings.Contains(got, strings.Split(c.want, ";")[0]) || c.secret != "" && strings.Contains(err.Error(), c.secret) {
			t.Errorf("%s: %v", module, err)
		}
	}
	// What the schema cannot say, the module's constructor refuses: these
	// are the settings' fault as well.
	twice := `{"profiles": [{"name": "Luna", "keywords": ["luna"], "provider": "gemini", "model": "m"}, {"name": "Luna", "keywords": ["moon"], "provider": "gemini", "model": "m"}]}`
	if _, err := s.save(ctx, "chat", 0, nil, json.RawMessage(twice), "x"); !errors.Is(err, otogi.ErrInvalid) || !strings.Contains(err.Error(), "used twice") {
		t.Fatalf("two profiles of one name: %v, want invalid", err)
	}
	if running(s) != nil {
		t.Fatal("a module with invalid settings runs")
	}
}

// A person's threshold factor out of its range is refused at its field.
func TestPeopleSettingsValidation(t *testing.T) {
	ctx := context.Background()
	storage, err := modulestore.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	s, _ := settingsHarness(t, storage)
	settings := `{"profiles": [{"name": "Luna", "keywords": ["luna"], "provider": "gemini", "model": "m"}], "people": [{"user": "qq:user:x", "threshold_factor": 20}]}`
	_, err = s.save(ctx, "chat", 0, nil, json.RawMessage(settings), "x")
	if where := refusedAt(err); !errors.Is(err, otogi.ErrInvalid) || !strings.Contains(strings.Join(where, ";"), "people[0].threshold_factor") {
		t.Fatalf("refused at %q", where)
	}
}

// refusedAt lists the fields of the settings errors.Join joins in err.
func refusedAt(err error) []string {
	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) {
		return nil
	}
	var where []string
	for _, e := range joined.Unwrap() {
		if field := (*otogi.SettingError)(nil); errors.As(e, &field) {
			where = append(where, field.Path)
		}
	}
	return where
}

// Every module's settings have a schema document with the types it uses.
func TestSettingsSchemas(t *testing.T) {
	for id, k := range moduleKinds {
		doc, err := k.schemaDocument()
		if err != nil || doc["type"] != "object" {
			t.Fatalf("%s: %v %v", id, doc, err)
		}
	}
	doc, _ := moduleKinds["todou"].schemaDocument()
	data, _ := json.Marshal(doc)
	if !strings.Contains(string(data), `"format":"secret"`) || !strings.Contains(string(data), `"format":"duration"`) || !strings.Contains(string(data), `"$defs"`) {
		t.Fatalf("todou schema %s", data)
	}
	// The fields that name people, which the panel looks up among the people seen.
	doc, _ = sections[generalSection].schemaDocument()
	if items := doc["properties"].(map[string]any)["operators"].(map[string]any)["items"].(map[string]any); items["format"] != "actor" {
		t.Errorf("operators' items %v", items)
	}
	doc, _ = moduleKinds["chat"].schemaDocument()
	if user := doc["$defs"].(map[string]any)["PersonConfig"].(map[string]any)["properties"].(map[string]any)["user"].(map[string]any); user["format"] != "actor" {
		t.Errorf("chat's people's user %v", user)
	}
}

// The panel offers a provider's models for a field of format llm-model, and
// marks its thinking levels for one of llm-thinking, by the provider the
// field named provider beside it names: every such field has one.
func TestModelFieldsHaveTheirProviderBeside(t *testing.T) {
	var models int
	for id, k := range moduleKinds {
		doc, err := k.schemaDocument()
		if err != nil {
			t.Fatal(err)
		}
		objects := []map[string]any{doc}
		defs, _ := doc["$defs"].(map[string]any)
		for _, def := range defs {
			objects = append(objects, def.(map[string]any))
		}
		for _, o := range objects {
			props, _ := o["properties"].(map[string]any)
			for name, p := range props {
				format := p.(map[string]any)["format"]
				if format != "llm-model" && format != "llm-thinking" {
					continue
				}
				models++
				if provider, _ := props["provider"].(map[string]any); provider["format"] != "llm-provider" {
					t.Errorf("%s: %s, of format %s, has no provider field of format llm-provider beside it", id, name, format)
				}
			}
		}
	}
	if models != 6 { // chat, media, twitter: a model and its thinking each
		t.Errorf("%d fields of format llm-model or llm-thinking, want 6", models)
	}
}

// The card modules draw with the content safety module's settings: they need it
// running, it cannot be turned off under them, and changing it rebuilds
// them with it.
func TestCardsCensorThroughTheSafetyModule(t *testing.T) {
	ctx := context.Background()
	storage, err := modulestore.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	s, rec := settingsHarness(t, storage)
	chrome := filepath.Join(t.TempDir(), "chrome-headless-shell")
	if err := os.WriteFile(chrome, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	on, off := true, false
	booth := json.RawMessage(`{"chrome": "` + chrome + `"}`)
	if _, err := s.save(ctx, "booth", 0, &on, booth, "x"); err == nil || !strings.Contains(err.Error(), "cardimage.pictures") {
		t.Fatalf("BOOTH cards without the content safety module: %v", err)
	}
	if _, err := s.save(ctx, "safety", 0, &on, json.RawMessage(`{}`), "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.save(ctx, "booth", 0, &on, booth, "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.save(ctx, "safety", 1, &off, nil, "x"); err == nil || !strings.Contains(err.Error(), "booth") {
		t.Fatalf("turning off the content safety module under BOOTH cards: %v", err)
	}
	if _, err := s.save(ctx, "safety", 1, nil, json.RawMessage(`{"blur": 4}`), "x"); err != nil {
		t.Fatal(err)
	}
	last := rec.list[len(rec.list)-1]
	if p := last.Payload.(map[string]any); last.Kind != "module.reconfigured" || !slices.Equal(p["restarted"].([]otogi.ModuleID), []otogi.ModuleID{"booth"}) {
		t.Fatalf("record %+v", last)
	}
}
