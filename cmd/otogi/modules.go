package main

import (
	"fmt"
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/TundraWork/Ex-Otogi/modules/echo"
	"github.com/TundraWork/Ex-Otogi/modules/notes"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// moduleFactory builds a module from its config section. Factories decode the
// section strictly into their own config type with decodeStrict.
type moduleFactory func(config *yaml.Node) (otogi.Module, error)

// moduleRegistry lists every module compiled into the binary, by the ID used
// under `modules:` in the config file.
var moduleRegistry = map[string]moduleFactory{
	"echo": func(config *yaml.Node) (otogi.Module, error) {
		var none struct{}
		if err := decodeSection(config, &none); err != nil {
			return nil, err
		}
		return echo.New(), nil
	},
	"notes": func(config *yaml.Node) (otogi.Module, error) {
		var cfg notes.Config
		if err := decodeSection(config, &cfg); err != nil {
			return nil, err
		}
		return notes.New(cfg), nil
	},
}

// buildModules constructs the modules enabled in the config.
func buildModules(sections map[string]yaml.Node) ([]otogi.Module, error) {
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
		module, err := factory(&section)
		if err != nil {
			return nil, fmt.Errorf("modules.%s: %w", id, err)
		}
		modules = append(modules, module)
	}
	return modules, nil
}

// decodeSection strictly decodes a module section; an empty or null section
// leaves v at its zero value.
func decodeSection(n *yaml.Node, v any) error {
	if n.Kind == 0 || n.Tag == "!!null" {
		return nil
	}
	return decodeStrict(n, v)
}
