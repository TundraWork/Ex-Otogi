package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"testing"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// revertHarness saves the given settings of id, one revision each, as a
// running process would have, and returns what reverts and reads them.
func revertHarness(t *testing.T, id otogi.ModuleID, settings ...string) (revert func() (string, error), history func() string, rec *records) {
	t.Helper()
	ctx := context.Background()
	storage := openStore(t)
	rec = &records{}
	s := &moduleSettings{store: storage, recorder: rec, log: slog.New(slog.DiscardHandler)}
	for i, text := range settings {
		if err := storage.AddSettings(ctx, modulestore.SettingsRevision{Module: id, Revision: int64(i + 1), Enabled: i%2 == 0, Settings: json.RawMessage(text), By: "Tundra"}); err != nil {
			t.Fatal(err)
		}
	}
	revert = func() (string, error) {
		var out bytes.Buffer
		err := s.revert(ctx, id, &out)
		return out.String(), err
	}
	history = func() string {
		t.Helper()
		revisions, err := storage.SettingsRevisions(ctx, id, math.MaxInt64, 99)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range revisions {
			out = append(out, fmt.Sprintf("%d %t %s by %s", r.Revision, r.Enabled, r.Settings, r.By))
		}
		return strings.Join(out, "\n")
	}
	return revert, history, rec
}

// Reverting stores the revision before the current one again, as the
// latest; again, the one before that; and after the first, the settings
// there were before anything was saved. History is kept.
func TestSettingsRevert(t *testing.T) {
	revert, history, rec := revertHarness(t, accountsSection, `{"qq":[]}`, `{"telegram":[]}`, `{"qq":[{"account":"bad"}]}`)

	out, err := revert()
	if err != nil || !strings.Contains(out, "system:accounts") || !strings.Contains(out, "revision 3") || !strings.Contains(out, "revision 4") || !strings.Contains(out, "revision 2") {
		t.Fatalf("revert: %q, %v", out, err)
	}
	want := `4 false {"telegram":[]} by otogi settings revert
3 true {"qq":[{"account":"bad"}]} by Tundra
2 false {"telegram":[]} by Tundra
1 true {"qq":[]} by Tundra`
	if got := history(); got != want {
		t.Fatalf("after one revert:\n%s", got)
	}
	if len(rec.list) != 1 || rec.list[0].Kind != "system.reconfigured" || rec.list[0].Component != "system:accounts" {
		t.Fatalf("records %+v", rec.list)
	}
	if payload := rec.list[0].Payload.(map[string]any); payload["by"] != "otogi settings revert" || payload["revision"] != int64(4) {
		t.Fatalf("the record: %+v", payload)
	}

	if out, err = revert(); err != nil || !strings.Contains(out, "revision 5") || !strings.Contains(out, "revision 1") {
		t.Fatalf("second revert: %q, %v", out, err)
	}
	if got := history(); !strings.HasPrefix(got, `5 true {"qq":[]} by otogi settings revert`+"\n4 ") {
		t.Fatalf("after two reverts:\n%s", got)
	}
	if out, err = revert(); err != nil || !strings.Contains(out, "revision 6") || !strings.Contains(out, "empty") {
		t.Fatalf("third revert: %q, %v", out, err)
	}
	if got := history(); !strings.HasPrefix(got, `6 false {} by otogi settings revert`+"\n5 ") {
		t.Fatalf("after three reverts:\n%s", got)
	}
	before := history()
	if out, err = revert(); err == nil || !strings.Contains(err.Error(), "nothing earlier") {
		t.Fatalf("fourth revert: %q, %v", out, err)
	}
	if history() != before {
		t.Fatalf("a refused revert changed the history:\n%s", history())
	}
}

// With one saved revision there is none before it: reverting stores empty
// settings, which is what a section or module never saved has, and says so.
func TestSettingsRevertOfTheOnlyRevision(t *testing.T) {
	revert, history, _ := revertHarness(t, accountsSection, `{"qq":[{"account":"bad"}]}`)
	out, err := revert()
	if err != nil || !strings.Contains(out, "revision 1") || !strings.Contains(out, "revision 2") || !strings.Contains(out, "empty") {
		t.Fatalf("revert: %q, %v", out, err)
	}
	if got := history(); got != `2 false {} by otogi settings revert`+"\n"+`1 true {"qq":[{"account":"bad"}]} by Tundra` {
		t.Fatalf("after the revert:\n%s", got)
	}
}

// A module's settings revert too, recorded as a module's; an ID with
// nothing saved is refused.
func TestSettingsRevertOfAModule(t *testing.T) {
	revert, history, rec := revertHarness(t, "utility", `{}`, `{"a":1}`)
	if _, err := revert(); err != nil {
		t.Fatal(err)
	}
	if got := history(); !strings.HasPrefix(got, "3 true {} by otogi settings revert\n") {
		t.Fatalf("after the revert:\n%s", got)
	}
	if len(rec.list) != 1 || rec.list[0].Kind != "module.reconfigured" {
		t.Fatalf("records %+v", rec.list)
	}

	revert, history, _ = revertHarness(t, "system:acounts")
	if _, err := revert(); err == nil || !strings.Contains(err.Error(), "system:acounts") || history() != "" {
		t.Fatalf("revert of an ID with no settings: %v, history %q", err, history())
	}
}

// The offline revert stores the revision it goes back to as it is, even one
// a save would refuse, such as a chat persona under its old name: it is how
// a revision already stored is put right.
func TestSettingsRevertOfARenamedPersona(t *testing.T) {
	revert, history, _ := revertHarness(t, "chat", `{"profiles":[{"name":"Luna"}]}`, `{"profiles":[{"name":"Mizuki"}]}`)
	if _, err := revert(); err != nil {
		t.Fatal(err)
	}
	if got := history(); !strings.HasPrefix(got, `3 true {"profiles":[{"name":"Luna"}]} by otogi settings revert`+"\n") {
		t.Fatalf("after the revert:\n%s", got)
	}
}
