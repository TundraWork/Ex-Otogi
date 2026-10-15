package main

import (
	"context"
	"fmt"
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/TundraWork/Ex-Otogi/modules/echo"
	"github.com/TundraWork/Ex-Otogi/modules/notes"
	"github.com/TundraWork/Ex-Otogi/modules/reminders"
	"github.com/TundraWork/Ex-Otogi/modules/todou"
	"github.com/TundraWork/Ex-Otogi/modules/utility"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// moduleEnv is what the composition root offers module factories beyond
// their own config section. Status is only valid once the process runs.
type moduleEnv struct {
	operators []string
	status    func(context.Context) utility.Status
	commands  func() []utility.CommandInfo
}

// moduleFactory builds a module from its config section, decoded strictly
// into the module's own config type with decodeSection.
type moduleFactory func(config *yaml.Node, env moduleEnv) (otogi.Module, error)

// configured adapts a module constructor taking only its typed config.
func configured[C any, M otogi.Module](build func(C) M) moduleFactory {
	return func(config *yaml.Node, _ moduleEnv) (otogi.Module, error) {
		var cfg C
		if err := decodeSection(config, &cfg); err != nil {
			return nil, err
		}
		return build(cfg), nil
	}
}

// moduleRegistry lists every module compiled into the binary, by the ID used
// under `modules:` in the config file.
var moduleRegistry = map[string]moduleFactory{
	"echo":      configured(func(struct{}) *echo.Module { return echo.New() }),
	"notes":     configured(notes.New),
	"reminders": configured(reminders.New),
	"todou": func(config *yaml.Node, env moduleEnv) (otogi.Module, error) {
		var cfg todou.Config
		if err := decodeSection(config, &cfg); err != nil {
			return nil, err
		}
		return todou.New(cfg, env.operators)
	},
	"utility": func(config *yaml.Node, env moduleEnv) (otogi.Module, error) {
		var cfg utility.Config
		if err := decodeSection(config, &cfg); err != nil {
			return nil, err
		}
		return utility.New(cfg, utility.Deps{Operators: env.operators, Status: env.status, Commands: env.commands}), nil
	},
}

// buildModules constructs the modules enabled in the config.
func buildModules(sections map[string]yaml.Node, env moduleEnv) ([]otogi.Module, error) {
	ids := make([]string, 0, len(sections))
	for id := range sections {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	modules := make([]otogi.Module, 0, len(ids))
	for _, id := range ids {
		factory, ok := moduleRegistry[id]
		if !ok {
			return nil, fmt.Errorf("modules.%s: unknown module", id)
		}
		section := sections[id]
		module, err := factory(&section, env)
		if err != nil {
			return nil, fmt.Errorf("modules.%s: %w", id, err)
		}
		modules = append(modules, module)
	}
	return modules, nil
}

// commandsOf lists the commands every module declares, for /help.
func commandsOf(modules []otogi.Module) []utility.CommandInfo {
	var out []utility.CommandInfo
	for _, m := range modules {
		d := m.Descriptor()
		for _, c := range d.Commands {
			out = append(out, utility.CommandInfo{Module: d.ID, Spec: c})
		}
	}
	return out
}

// decodeSection strictly decodes a module section; an empty or null section
// leaves v at its zero value.
func decodeSection(n *yaml.Node, v any) error {
	if n.Kind == 0 || n.Tag == "!!null" {
		return nil
	}
	return decodeStrict(n, v)
}
