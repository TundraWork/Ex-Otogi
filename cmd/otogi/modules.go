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
	}).keeping(profileNames),
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

// profileNames are chat's agent profiles, whose names cannot change.
var profileNames = keyedList{list: "profiles", key: "name", item: "a profile",
	why: "it names the profile's allowlist, and the profile's threads are stored under it, so another name is another profile, with an empty allowlist and no threads"}

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
	// keys are the lists whose items' keys cannot change.
	keys []keyedList
}

// keyedList is a list at the top of settings whose items have nothing but
// one field, their key, to tell them apart, and whose keys cannot change
// once saved, because what the bot keeps for an item is under its key. A
// save in which the list both loses a key and gains one is what a changed
// key looks like, and is refused; replacing an item takes two saves. The
// served schema marks the list's key as x-key, for the panel to show it
// read-only in the items already saved.
type keyedList struct {
	list, key string
	item      string // what one is, for the refusal: "an account"
	why       string // what is under the key, and so what another key is
}

// renamed refuses settings that change a key of a keyed list from what it
// was: see keyedList. The offline revert does not ask it, so that it can
// put back a revision already stored.
func (k settingsSchema) renamed(was, now []byte) error {
	var errs []error
	for _, l := range k.keys {
		before, after := l.keysOf(was), l.keysOf(now)
		var gone, come []string
		for _, key := range before {
			if !slices.Contains(after, key) {
				gone = append(gone, key)
			}
		}
		for _, key := range after {
			if !slices.Contains(before, key) {
				come = append(come, key)
			}
		}
		if len(gone) > 0 && len(come) > 0 {
			errs = append(errs, &otogi.SettingError{Path: l.list, Err: fmt.Errorf("%s's name cannot be changed, and this save has %s for %s: %s. To replace %s, add the other in one save and remove it in another",
				l.item, strings.Join(come, ", "), strings.Join(gone, ", "), l.why, l.item)})
		}
	}
	return errors.Join(errs...)
}

// keysOf lists the keys of the list's items in settings. Settings that do
// not decode are refused where they are checked.
func (l keyedList) keysOf(settings []byte) []string {
	var top map[string]json.RawMessage
	var items []map[string]any
	_ = json.Unmarshal(settings, &top)
	_ = json.Unmarshal(top[l.list], &items)
	keys := make([]string, 0, len(items))
	for _, item := range items {
		key, _ := item[l.key].(string)
		keys = append(keys, key)
	}
	return keys
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

// keeping gives a module's keyed lists: see keyedList.
func (k moduleKind) keeping(lists ...keyedList) moduleKind {
	k.keys = lists
	return k
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
	// Each is at its field, without the value: one may be a secret typed in
	// by mistake, and the caller has them.
	var errs []error
	for _, err := range res.Errors {
		if d := (*huma.ErrorDetail)(nil); errors.As(err, &d) {
			errs = append(errs, &otogi.SettingError{Path: d.Location, Err: errors.New(d.Message)})
		}
	}
	return errors.Join(errs...)
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
	for _, l := range k.keys {
		doc["properties"].(map[string]any)[l.list].(map[string]any)["x-key"] = l.key
	}
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
