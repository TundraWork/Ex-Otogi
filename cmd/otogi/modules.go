package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/TundraWork/Ex-Otogi/modules/bilibili"
	"github.com/TundraWork/Ex-Otogi/modules/booth"
	"github.com/TundraWork/Ex-Otogi/modules/chat"
	"github.com/TundraWork/Ex-Otogi/modules/decision"
	"github.com/TundraWork/Ex-Otogi/modules/echo"
	"github.com/TundraWork/Ex-Otogi/modules/media"
	"github.com/TundraWork/Ex-Otogi/modules/notes"
	"github.com/TundraWork/Ex-Otogi/modules/reminders"
	"github.com/TundraWork/Ex-Otogi/modules/safety"
	"github.com/TundraWork/Ex-Otogi/modules/todou"
	"github.com/TundraWork/Ex-Otogi/modules/twitter"
	"github.com/TundraWork/Ex-Otogi/modules/utility"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// moduleEnv is what the composition root offers modules beyond their
// settings. Status and commands are only valid once the process runs.
type moduleEnv struct {
	operators func() []string                  // the operators now
	secret    func(name string) (string, bool) // see moduleSettings.environment
	llm       func(name string) (llm.Provider, error)
	status    func(context.Context) utility.Status
	commands  func() []utility.CommandInfo
}

// moduleKinds lists every module compiled into the binary, by module ID.
var moduleKinds = map[otogi.ModuleID]moduleKind{
	"bilibili": kind(func(c bilibili.Config, _ moduleEnv) (otogi.Module, error) { return built(bilibili.New(c)) }),
	"booth":    kind(func(c booth.Config, _ moduleEnv) (otogi.Module, error) { return built(booth.New(c)) }),
	"chat": kind(func(c chat.Config, env moduleEnv) (otogi.Module, error) {
		return built(chat.New(c, env.llm))
	}),
	"decision": kind(func(c decision.Config, env moduleEnv) (otogi.Module, error) {
		return built(decision.New(c, env.secret))
	}),
	"echo": kind(func(struct{}, moduleEnv) (otogi.Module, error) { return echo.New(), nil }),
	"media": kind(func(c media.Config, env moduleEnv) (otogi.Module, error) {
		return built(media.New(c, env.llm, env.operators))
	}),
	"notes":     kind(func(c notes.Config, _ moduleEnv) (otogi.Module, error) { return notes.New(c), nil }),
	"reminders": kind(func(c reminders.Config, _ moduleEnv) (otogi.Module, error) { return reminders.New(c), nil }),
	"safety":    kind(func(c safety.Config, _ moduleEnv) (otogi.Module, error) { return built(safety.New(c)) }),
	"todou": kind(func(c todou.Config, env moduleEnv) (otogi.Module, error) {
		return built(todou.New(c, env.operators, env.secret))
	}),
	"twitter": kind(func(c twitter.Config, env moduleEnv) (otogi.Module, error) { return built(twitter.New(c, env.llm)) }),
	"utility": kind(func(c utility.Config, env moduleEnv) (otogi.Module, error) {
		return utility.New(c, utility.Deps{Operators: env.operators, Status: env.status, Commands: env.commands}), nil
	}),
}

// built keeps a failed constructor's nil module from becoming a non-nil
// interface.
func built[M otogi.Module](m M, err error) (otogi.Module, error) {
	if err != nil {
		return nil, err
	}
	return m, nil
}

// settingsSchema is the JSON Schema of settings, generated from their Config
// type: a module's, or those of a section outside modules.
type settingsSchema struct {
	config   reflect.Type
	registry huma.Registry // the schema's $defs
	schema   *huma.Schema
	// secrets names the secrets settings refer to, from their Secret fields.
	secrets func(settings []byte) ([]string, error)
}

func schemaOf[C any]() settingsSchema {
	r := huma.NewMapRegistry("#/$defs/", huma.DefaultSchemaNamer)
	r.RegisterTypeAlias(reflect.TypeFor[otogi.Duration](), reflect.TypeFor[durationSchema]())
	r.RegisterTypeAlias(reflect.TypeFor[otogi.Secret](), reflect.TypeFor[secretSchema]())
	return settingsSchema{
		config: reflect.TypeFor[C](), registry: r, schema: huma.SchemaFromType(r, reflect.TypeFor[C]()),
		secrets: func(settings []byte) ([]string, error) {
			cfg, err := decodeSettings[C](settings)
			if err != nil {
				return nil, err
			}
			names := map[string]bool{}
			secretNames(reflect.ValueOf(cfg), names)
			return slices.Sorted(maps.Keys(names)), nil
		},
	}
}

// decodeSettings decodes settings into their Config type; a setting the
// type does not have is an error.
func decodeSettings[C any](settings []byte) (C, error) {
	var cfg C
	decoder := json.NewDecoder(bytes.NewReader(settings))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("decode settings: %w", err)
	}
	return cfg, nil
}

// moduleKind is a module compiled into the binary: the JSON Schema of its
// settings, generated from its Config type, and how to build it.
type moduleKind struct {
	settingsSchema
	build func(settings []byte, env moduleEnv) (otogi.Module, error)
}

func kind[C any](build func(C, moduleEnv) (otogi.Module, error)) moduleKind {
	return moduleKind{
		settingsSchema: schemaOf[C](),
		build: func(settings []byte, env moduleEnv) (otogi.Module, error) {
			cfg, err := decodeSettings[C](settings)
			if err != nil {
				return nil, err
			}
			return build(cfg, env)
		},
	}
}

// secretNames collects the names of the secrets a decoded Config refers to.
func secretNames(v reflect.Value, names map[string]bool) {
	if v.Type() == reflect.TypeFor[otogi.Secret]() {
		if m := otogi.SecretReference.FindStringSubmatch(v.String()); m != nil {
			names[m[1]] = true
		}
		return
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			secretNames(v.Elem(), names)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			secretNames(v.Field(i), names)
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			secretNames(v.Index(i), names)
		}
	case reflect.Map:
		for _, value := range v.Seq2() {
			secretNames(value, names)
		}
	}
}

// check validates settings against the schema, field by field.
func (k settingsSchema) check(settings []byte) error {
	var v any
	if err := json.Unmarshal(settings, &v); err != nil {
		return fmt.Errorf("settings are not JSON: %w: %w", otogi.ErrInvalid, err)
	}
	res := &huma.ValidateResult{}
	huma.Validate(k.registry, k.schema, huma.NewPathBuffer(nil, 0), huma.ModeWriteToServer, v, res)
	if len(res.Errors) > 0 {
		// The values stay out: one may be a secret typed in by mistake, and
		// the caller has them.
		for _, err := range res.Errors {
			if d := (*huma.ErrorDetail)(nil); errors.As(err, &d) {
				d.Value = nil
			}
		}
		return &invalidSettings{details: res.Errors}
	}
	return nil
}

// instance validates settings against the schema and builds the module from
// them. Building starts nothing, which is the kernel's, so a constructor
// that fails has refused the settings: the error is otogi.ErrInvalid.
func (k moduleKind) instance(settings []byte, env moduleEnv) (otogi.Module, error) {
	if err := k.check(settings); err != nil {
		return nil, err
	}
	return refused(k.build(settings, env))
}

// refused makes an error from building with settings the settings' fault.
func refused[T any](built T, err error) (T, error) {
	if err != nil && !errors.Is(err, otogi.ErrInvalid) {
		err = fmt.Errorf("%w: %w", err, otogi.ErrInvalid)
	}
	return built, err
}

// schemaDocument is the settings' JSON Schema with the types it refers to
// under $defs, as one document.
func (k settingsSchema) schemaDocument() (map[string]any, error) {
	data, err := json.Marshal(k.schema)
	if err != nil {
		return nil, fmt.Errorf("encode schema: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}
	doc["x-order"] = fieldOrder(k.config)
	if defs := k.registry.Map(); len(defs) > 0 {
		ordered := map[string]any{}
		for name, def := range defs {
			data, err := json.Marshal(def)
			if err != nil {
				return nil, fmt.Errorf("encode schema of %s: %w", name, err)
			}
			var d map[string]any
			if err := json.Unmarshal(data, &d); err != nil {
				return nil, fmt.Errorf("decode schema of %s: %w", name, err)
			}
			if t := k.registry.TypeFromRef("#/$defs/" + name); t != nil {
				d["x-order"] = fieldOrder(t)
			}
			ordered[name] = d
		}
		doc["$defs"] = ordered
	}
	return doc, nil
}

// fieldOrder lists a struct's JSON field names in declaration order: the
// schema's properties are a map, and forms follow the type.
func fieldOrder(t reflect.Type) []string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	names := []string{}
	if t.Kind() != reflect.Struct {
		return names
	}
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if f.IsExported() && name != "-" {
			names = append(names, cmp.Or(name, f.Name))
		}
	}
	return names
}

// invalidSettings is settings that break their schema, field by field
// (huma.ErrorDetail).
type invalidSettings struct{ details []error }

func (e *invalidSettings) Error() string {
	var msgs []string
	for _, err := range e.details {
		if d := (*huma.ErrorDetail)(nil); errors.As(err, &d) {
			msgs = append(msgs, d.Location+": "+d.Message)
		}
	}
	return "invalid settings: " + strings.Join(msgs, "; ")
}

func (e *invalidSettings) Unwrap() error { return otogi.ErrInvalid }

// ErrorDetails lists the problems by field, for the API.
func (e *invalidSettings) ErrorDetails() []error { return e.details }

// durationSchema describes an otogi.Duration.
type durationSchema struct{}

func (durationSchema) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeString, Format: "duration", Pattern: `^([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$`, PatternDescription: "a duration such as 90s, 5m, or 720h"}
}

// secretSchema describes an otogi.Secret.
type secretSchema struct{}

func (secretSchema) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeString, Format: "secret", Pattern: otogi.SecretReference.String(), PatternDescription: "a reference to a secret, ${NAME}"}
}

// commandsOf lists the commands the modules declare, for /help.
func commandsOf(descriptors []otogi.ModuleDescriptor) []utility.CommandInfo {
	var out []utility.CommandInfo
	for _, d := range descriptors {
		for _, c := range d.Commands {
			out = append(out, utility.CommandInfo{Module: d.ID, Spec: c})
		}
	}
	return out
}
