package main

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/management"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// settingsAPI is settings kept as revisions as the management API sees
// them: the modules', or the sections'. id finds the ID they are kept under
// from the name in a route, or fails with management.ErrNotFound.
type settingsAPI struct {
	settings *moduleSettings
	id       func(name string) (otogi.ModuleID, error)
}

// moduleAPI is the module runtime as the management API sees it.
type moduleAPI struct{ settingsAPI }

// systemAPI is the sections as the management API sees them: system:general
// is the section general.
type systemAPI struct{ settingsAPI }

var (
	_ management.Modules = moduleAPI{}
	_ management.Secrets = moduleAPI{}
	_ management.System  = systemAPI{}
)

func newModuleAPI(s *moduleSettings) moduleAPI {
	return moduleAPI{settingsAPI{settings: s, id: func(name string) (otogi.ModuleID, error) {
		if _, ok := s.kinds[otogi.ModuleID(name)]; !ok {
			return "", fmt.Errorf("module %q: %w", name, management.ErrNotFound)
		}
		return otogi.ModuleID(name), nil
	}}}
}

func newSystemAPI(s *moduleSettings) systemAPI {
	return systemAPI{settingsAPI{settings: s, id: func(name string) (otogi.ModuleID, error) {
		id := otogi.ModuleID(sectionPrefix + name)
		if _, ok := sections[id]; !ok {
			return "", fmt.Errorf("section %q: %w", name, management.ErrNotFound)
		}
		return id, nil
	}}}
}

func (a systemAPI) Sections(ctx context.Context) ([]management.Section, error) {
	var out []management.Section
	for _, id := range slices.Sorted(maps.Keys(sections)) {
		r, err := a.settings.current(ctx, id)
		if err != nil {
			return nil, err
		}
		section := management.Section{ID: strings.TrimPrefix(string(id), sectionPrefix), Revision: r.Revision, Applies: "now", Running: r.Revision}
		if sections[id].onRestart {
			section.Applies, section.Running = "restart", a.settings.started[id]
		}
		out = append(out, section)
	}
	return out, nil
}

func (a moduleAPI) Modules(ctx context.Context) ([]management.Module, error) {
	s := a.settings
	revisions, err := s.store.CurrentSettings(ctx)
	if err != nil {
		return nil, err
	}
	current := map[otogi.ModuleID]modulestore.SettingsRevision{}
	for _, r := range revisions {
		current[r.Module] = r
	}
	running := map[otogi.ModuleID]otogi.ModuleDescriptor{}
	for _, d := range s.kernel.Descriptors() {
		running[d.ID] = d
	}
	chats := map[[2]string]int{}
	for _, c := range s.kernel.Allowlists() {
		chats[[2]string{string(c.Module), c.List}]++
	}
	counts := s.kernel.Stats().Modules
	env, err := s.environment(ctx)
	if err != nil {
		return nil, err
	}
	var out []management.Module
	for _, id := range slices.Sorted(maps.Keys(s.kinds)) {
		r, saved := current[id]
		m := management.Module{ID: string(id), Enabled: r.Enabled, Revision: r.Revision, IgnoreBots: s.kernel.IgnoresBots(id), Allowlists: []management.Allowlist{}, Commands: []management.Command{}, Provides: []string{}}
		d, ok := running[id]
		m.Running = ok
		if !ok { // describe it as its current settings would build it
			settings := json.RawMessage("{}")
			if saved {
				settings = r.Settings
			}
			if instance, err := s.kinds[id].instance(settings, env); err != nil {
				if saved {
					m.Problem = err.Error()
				}
			} else {
				d = instance.Descriptor()
			}
		}
		for _, l := range d.Allowlists {
			m.Allowlists = append(m.Allowlists, management.Allowlist{Name: l.Name, Title: l.Title, Description: l.Description, About: string(l.About), Chats: chats[[2]string{string(id), l.Name}]})
		}
		m.Handles = len(d.Subscribes) > 0 || len(d.Commands) > 0
		m.Provides = append(m.Provides, d.Provides...)
		for _, c := range d.Commands {
			m.Commands = append(m.Commands, management.Command{Name: c.Name, Usage: c.Usage, Description: c.Description})
		}
		c := counts[id]
		m.Counts = management.ModuleCounts{Completed: c.Completed, Slow: c.Slow, Failed: c.Failed, Panicked: c.Panicked}
		out = append(out, m)
	}
	return out, nil
}

func (a settingsAPI) Settings(ctx context.Context, name string) (management.Revision, map[string]any, error) {
	id, err := a.id(name)
	if err != nil {
		return management.Revision{}, nil, err
	}
	r, err := a.settings.current(ctx, id)
	if err != nil {
		return management.Revision{}, nil, err
	}
	kind, _ := a.settings.schema(id)
	schema, err := kind.schemaDocument()
	return revisionOut(r), schema, err
}

func (a settingsAPI) SaveSettings(ctx context.Context, name string, base int64, enabled *bool, settings json.RawMessage, by string) (management.Revision, error) {
	id, err := a.id(name)
	if err != nil {
		return management.Revision{}, err
	}
	r, err := a.settings.save(ctx, id, base, enabled, settings, by)
	return revisionOut(r), err
}

func (a settingsAPI) Revisions(ctx context.Context, name string, before int64, limit int) ([]management.Revision, error) {
	id, err := a.id(name)
	if err != nil {
		return nil, err
	}
	revisions, err := a.settings.store.SettingsRevisions(ctx, id, before, limit)
	out := make([]management.Revision, len(revisions))
	for i, r := range revisions {
		out[i] = revisionOut(r)
	}
	return out, err
}

func (a settingsAPI) Revision(ctx context.Context, name string, revision int64) (management.Revision, error) {
	id, err := a.id(name)
	if err != nil {
		return management.Revision{}, err
	}
	r, found, err := a.settings.store.SettingsRevision(ctx, id, revision)
	if err == nil && !found {
		err = fmt.Errorf("%s has no settings revision %d: %w", id, revision, management.ErrNotFound)
	}
	return revisionOut(r), err
}

func (a moduleAPI) Allowlists() []management.AllowedChat {
	lists := a.settings.kernel.Allowlists()
	out := make([]management.AllowedChat, len(lists))
	for i, c := range lists {
		out[i] = management.AllowedChat{Module: string(c.Module), List: c.List, Chat: c.Chat, At: c.At, By: c.By}
	}
	return out
}

func (a moduleAPI) SetAllowed(ctx context.Context, module, list, chat string, allowed bool, by string) error {
	return a.settings.kernel.SetAllowed(ctx, otogi.ModuleID(module), list, chat, allowed, by)
}

func (a moduleAPI) SetIgnoreBots(ctx context.Context, module string, ignore bool, by string) error {
	return a.settings.kernel.SetIgnoreBots(ctx, otogi.ModuleID(module), ignore, by)
}

func (a moduleAPI) Secrets(ctx context.Context) ([]management.Secret, error) {
	return a.settings.secrets(ctx)
}

func (a moduleAPI) SetSecret(ctx context.Context, name, value, by string) ([]string, error) {
	restarted, err := a.settings.setSecret(ctx, name, value, by)
	return ids(restarted), err
}

func (a moduleAPI) DeleteSecret(ctx context.Context, name, by string) ([]string, error) {
	restarted, err := a.settings.deleteSecret(ctx, name, by)
	return ids(restarted), err
}

func ids(modules []otogi.ModuleID) []string {
	out := make([]string, len(modules))
	for i, id := range modules {
		out[i] = string(id)
	}
	return out
}

func revisionOut(r modulestore.SettingsRevision) management.Revision {
	return management.Revision{Module: string(r.Module), Revision: r.Revision, Enabled: r.Enabled, Settings: r.Settings, By: r.By, At: r.At}
}
