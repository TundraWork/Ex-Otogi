package management

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Settings is settings kept as revisions, of the modules or of the sections
// outside them (see System), each named by an ID.
type Settings interface {
	// Settings returns the current revision and the JSON Schema of the
	// settings. One never saved is revision 0, with empty settings.
	Settings(ctx context.Context, id string) (Revision, map[string]any, error)
	// SaveSettings applies and stores a new revision, based on the revision
	// base. Nil enabled or settings keep the current.
	SaveSettings(ctx context.Context, id string, base int64, enabled *bool, settings json.RawMessage, by string) (Revision, error)
	// Revisions returns up to limit revisions older than before, newest
	// first.
	Revisions(ctx context.Context, id string, before int64, limit int) ([]Revision, error)
	// Revision returns one revision, or an error wrapping ErrNotFound.
	Revision(ctx context.Context, id string, revision int64) (Revision, error)
}

// Modules is the module runtime as the API sees it; the composition root
// implements it.
type Modules interface {
	Settings
	// Modules lists every module compiled into the binary, by ID.
	Modules(ctx context.Context) ([]Module, error)
	// Allowlists returns every chat on every module's allowlists.
	Allowlists() []AllowedChat
	// SetAllowed adds a chat to a module's allowlist, or removes it.
	SetAllowed(ctx context.Context, module, list, chat string, allowed bool, by string) error
	// SetIgnoreBots turns a module's "ignore bots" switch on or off.
	SetIgnoreBots(ctx context.Context, module string, ignore bool, by string) error
}

// ErrNotFound is what Modules and System return for a module, section, or
// revision that does not exist.
var ErrNotFound = errors.New("not found")

// StaleRevisionError is a change based on a revision that is no longer the
// current one.
type StaleRevisionError struct{ Current int64 }

func (e *StaleRevisionError) Error() string {
	return fmt.Sprintf("the settings changed meanwhile: the current revision is %d", e.Current)
}

// Module is a module compiled into the binary.
type Module struct {
	ID         string       `json:"id"`
	Enabled    bool         `json:"enabled" doc:"Whether its current revision turns it on."`
	Running    bool         `json:"running"`
	Revision   int64        `json:"revision" doc:"Its current settings revision; 0 when never saved."`
	Problem    string       `json:"problem,omitempty" doc:"Why its current settings do not build, when they do not; its allowlists are then unknown."`
	Allowlists []Allowlist  `json:"allowlists" nullable:"false"`
	Commands   []Command    `json:"commands" nullable:"false"`
	Counts     ModuleCounts `json:"counts" doc:"Its deliveries since the process started."`
	IgnoreBots bool         `json:"ignore_bots" doc:"Whether it ignores messages from bot accounts; on unless turned off."`
	Handles    bool         `json:"handles" doc:"Whether it handles messages or commands at all; ignore_bots means something only then."`
}

// Allowlist is a module's list of the chats it answers in.
type Allowlist struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Chats       int    `json:"chats" doc:"How many chats are on it."`
	About       string `json:"about,omitempty" doc:"The module whose capability the list grants, when that is another module's, such as todou for the chat module's list of where agents may use todou's tools."`
}

// Command is a command a module handles.
type Command struct {
	Name        string `json:"name"`
	Usage       string `json:"usage,omitempty"`
	Description string `json:"description,omitempty"`
}

// ModuleCounts counts a module's deliveries by outcome.
type ModuleCounts struct {
	Completed int64 `json:"completed"`
	Slow      int64 `json:"slow" doc:"Completed, but past half the module's timeout."`
	Failed    int64 `json:"failed"`
	Panicked  int64 `json:"panicked"`
}

// Revision is one saved revision of a module's settings, or a section's.
type Revision struct {
	Module   string          `json:"module" doc:"The module's ID, or system:<section> for a section."`
	Revision int64           `json:"revision"`
	Enabled  bool            `json:"enabled"`
	Settings json.RawMessage `json:"settings"`
	By       string          `json:"by,omitempty"`
	At       time.Time       `json:"at,omitzero"`
}

// AllowedChat is a chat on a module's allowlist.
type AllowedChat struct {
	Module string    `json:"module"`
	List   string    `json:"list"`
	Chat   string    `json:"chat"`
	At     time.Time `json:"added_at"`
	By     string    `json:"added_by"`
}

// callerKey keys who is calling, for records of changes.
type callerKey struct{}

// caller is who made a request: until panel sign-in, the management token.
func caller(ctx context.Context) string {
	if by, ok := ctx.Value(callerKey{}).(string); ok {
		return by
	}
	return "management API"
}

// problem turns an error from the runtime into an RFC 9457 response.
func problem(err error) error {
	var stale *StaleRevisionError
	var upstream *UpstreamError
	switch {
	case errors.As(err, &stale):
		return huma.Error412PreconditionFailed(err.Error())
	case errors.Is(err, ErrNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, ErrInUse):
		return huma.Error409Conflict(err.Error())
	case errors.As(err, &upstream):
		return huma.Error502BadGateway(err.Error())
	case errors.Is(err, otogi.ErrInvalid):
		if details, fields := settingDetails(err); fields {
			return huma.Error422UnprocessableEntity(err.Error(), details...)
		}
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, otogi.ErrUnavailable):
		return huma.Error503ServiceUnavailable(err.Error())
	}
	return huma.Error500InternalServerError(err.Error())
}

// settingDetails lists a refusal of settings field by field, as the schema's
// checks list theirs: an otogi.SettingError at its path, with its reason,
// and each error errors.Join joins in turn. Any other error is one entry at
// no field, its whole text: one that wraps a SettingError in more words is
// about other settings, such as another module's, rebuilt with these.
// fields reports whether err has a SettingError of its own.
func settingDetails(err error) (details []error, fields bool) {
	switch e := err.(type) { //nolint:errorlint // the errors as returned and joined, not those wrapped in more words
	case *otogi.SettingError:
		return []error{&huma.ErrorDetail{Location: e.Path, Message: e.Err.Error()}}, true
	case interface{ Unwrap() []error }:
		for _, err := range e.Unwrap() {
			d, f := settingDetails(err)
			details, fields = append(details, d...), fields || f
		}
		return details, fields
	}
	return []error{&huma.ErrorDetail{Message: err.Error()}}, false
}

// etag and revisionOf carry a settings revision in ETag and If-Match.
func etag(revision int64) string { return strconv.Quote(strconv.FormatInt(revision, 10)) }

func revisionOf(ifMatch string) (int64, error) {
	n, err := strconv.ParseInt(strings.Trim(strings.TrimPrefix(ifMatch, "W/"), `"`), 10, 64)
	if err != nil || n < 0 {
		return 0, huma.Error400BadRequest(`If-Match must be the settings' ETag, such as "3"`)
	}
	return n, nil
}

// chatKey splits a chat as /whoami prints it: platform and conversation.
func chatKey(chat string) (platform, conversation string, err error) {
	platform, conversation, ok := strings.Cut(chat, ":")
	if !ok || platform == "" || conversation == "" {
		return "", "", huma.Error400BadRequest(fmt.Sprintf("chat %q: want <platform>:<conversation>, as /whoami prints it", chat))
	}
	return platform, conversation, nil
}

// ModulePath names a module in a route.
type ModulePath struct {
	Module string `path:"module" doc:"The module's ID."`
}

type settingsOutput struct {
	ETag string `header:"ETag"`
	Body struct {
		Revision
		Schema map[string]any `json:"schema" doc:"The JSON Schema of the settings."`
	}
}

type revisionOutput struct {
	ETag string `header:"ETag"`
	Body Revision
}

func registerModules(api huma.API, modules Modules) {
	find := func(ctx context.Context, id string) (Module, error) {
		all, err := modules.Modules(ctx)
		if err != nil {
			return Module{}, problem(err)
		}
		i := slices.IndexFunc(all, func(m Module) bool { return m.ID == id })
		if i < 0 {
			return Module{}, huma.Error404NotFound("no module " + id)
		}
		return all[i], nil
	}
	huma.Register(api, huma.Operation{OperationID: "list-modules", Method: http.MethodGet, Path: "/api/modules", Summary: "Every module compiled in", Tags: []string{"Modules"}},
		func(ctx context.Context, _ *struct{}) (*struct{ Body []Module }, error) {
			all, err := modules.Modules(ctx)
			if err != nil {
				return nil, problem(err)
			}
			return &struct{ Body []Module }{all}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "get-module", Method: http.MethodGet, Path: "/api/modules/{module}", Summary: "One module", Tags: []string{"Modules"}},
		func(ctx context.Context, in *ModulePath) (*struct{ Body Module }, error) {
			m, err := find(ctx, in.Module)
			return &struct{ Body Module }{m}, err
		})
	huma.Register(api, huma.Operation{OperationID: "switch-module", Method: http.MethodPatch, Path: "/api/modules/{module}", Summary: "Turn a module, or its bots switch, on or off",
		Description: "enabled saves a revision that changes only whether the module runs, and applies it. ignore_bots turns its \"ignore bots\" switch on or off at once, and is recorded as bots.changed.", Tags: []string{"Modules"}},
		func(ctx context.Context, in *struct {
			ModulePath
			IfMatch string `header:"If-Match" doc:"The ETag of the settings enabled changes; the current revision when absent."`
			Body    struct {
				Enabled    *bool `json:"enabled,omitempty" doc:"Whether the module runs."`
				IgnoreBots *bool `json:"ignore_bots,omitempty" doc:"Whether it ignores messages from bot accounts."`
			}
		},
		) (*struct{ Body Module }, error) {
			if in.Body.Enabled == nil && in.Body.IgnoreBots == nil {
				return nil, huma.Error422UnprocessableEntity("set enabled, ignore_bots, or both")
			}
			if _, err := find(ctx, in.Module); err != nil {
				return nil, err
			}
			if in.Body.IgnoreBots != nil {
				if err := modules.SetIgnoreBots(ctx, in.Module, *in.Body.IgnoreBots, caller(ctx)); err != nil {
					return nil, problem(err)
				}
			}
			if in.Body.Enabled != nil {
				base, err := baseRevision(ctx, modules, in.Module, in.IfMatch)
				if err != nil {
					return nil, err
				}
				if _, err := modules.SaveSettings(ctx, in.Module, base, in.Body.Enabled, nil, caller(ctx)); err != nil {
					return nil, problem(err)
				}
			}
			m, err := find(ctx, in.Module)
			return &struct{ Body Module }{m}, err
		})
	h := settingsHandlers{modules}
	huma.Register(api, huma.Operation{OperationID: "get-settings", Method: http.MethodGet, Path: "/api/modules/{module}/settings", Summary: "A module's current settings and their schema", Tags: []string{"Settings"}},
		func(ctx context.Context, in *ModulePath) (*settingsOutput, error) { return h.get(ctx, in.Module) })
	huma.Register(api, huma.Operation{OperationID: "put-settings", Method: http.MethodPut, Path: "/api/modules/{module}/settings", Summary: "Save and apply new settings",
		Description: "Validates the settings against their schema, builds the module from them, swaps it in while it runs, and stores the revision. Nothing is stored if any step fails.", Tags: []string{"Settings"}},
		func(ctx context.Context, in *struct {
			ModulePath
			IfMatch
			Body json.RawMessage
		},
		) (*revisionOutput, error) {
			return h.put(ctx, in.Module, in.IfMatch, in.Body)
		})
	huma.Register(api, huma.Operation{OperationID: "list-revisions", Method: http.MethodGet, Path: "/api/modules/{module}/settings/revisions", Summary: "A module's settings history, newest first", Tags: []string{"Settings"}},
		func(ctx context.Context, in *struct {
			ModulePath
			RevisionsQuery
		},
		) (*revisionsPage, error) {
			return h.list(ctx, in.Module, in.RevisionsQuery)
		})
	huma.Register(api, huma.Operation{OperationID: "restore-revision", Method: http.MethodPost, Path: "/api/modules/{module}/settings/revisions/{revision}/restore", Summary: "Restore earlier settings",
		Description: "Saves the settings of an earlier revision as a new revision and applies them. Whether the module runs is left as it is.", Tags: []string{"Settings"}},
		func(ctx context.Context, in *struct {
			ModulePath
			RevisionPath
			IfMatch
		},
		) (*revisionOutput, error) {
			return h.restore(ctx, in.Module, in.Revision, in.IfMatch)
		})
}

// System is the settings outside modules as the API sees them, in
// sections; the composition root implements it.
type System interface {
	Settings
	// Sections lists the sections, in the order the panel shows them.
	Sections(ctx context.Context) ([]Section, error)
	// Providers lists the language model providers the llm section sets,
	// by name, each with the models last fetched from it.
	Providers(ctx context.Context) ([]LLMProvider, error)
	// FetchModels asks a provider for its models now, saves the list in
	// place of the last one, and returns the provider with it; by is who
	// asked. The fetch is recorded as llm.models, failed or not. The error
	// wraps ErrNotFound for a provider not set, and is an *UpstreamError
	// when the provider's own answer was a failure, which leaves the last
	// list.
	FetchModels(ctx context.Context, name, by string) (LLMProvider, error)
}

// UpstreamError is a failure of a service the runtime called for the
// request, such as a language model provider: the API answers 502 with
// its text.
type UpstreamError struct{ Err error }

func (e *UpstreamError) Error() string { return e.Err.Error() }

func (e *UpstreamError) Unwrap() error { return e.Err }

// LLMProvider is a language model provider the llm section sets, with the
// models last fetched from it.
type LLMProvider struct {
	Name      string      `json:"name" doc:"The name modules use for it."`
	Kind      string      `json:"kind" enum:"gemini,openai" doc:"gemini, or openai for a service that speaks the OpenAI Chat Completions API."`
	FetchedAt time.Time   `json:"fetched_at,omitzero" doc:"When its models were last fetched; absent when never."`
	FetchedBy string      `json:"fetched_by,omitempty" doc:"Who fetched them."`
	Models    []llm.Model `json:"models" nullable:"false" doc:"The models last fetched, in the provider's order: only the ID is always known. Empty when never fetched."`
}

// Section is a group of settings outside modules, such as the language
// model providers. Its settings are kept as revisions, like a module's.
type Section struct {
	ID       string `json:"id" doc:"The section, as its routes name it. Its revisions and records carry system:<id>."`
	Revision int64  `json:"revision" doc:"Its current revision; 0 when it was never saved and runs on the defaults."`
	Applies  string `json:"applies" enum:"now,restart" doc:"When a saved revision takes effect: now, as it is saved, or restart, when the process next starts."`
	Running  int64  `json:"running" doc:"The revision the process runs with. For a section that applies on restart it is the one the process started with: while revision is newer, a restart is pending."`
}

// SectionPath names a section in a route.
type SectionPath struct {
	Section string `path:"section" doc:"The section's ID, such as general."`
}

func registerSystem(api huma.API, system System) {
	huma.Register(api, huma.Operation{OperationID: "list-sections", Method: http.MethodGet, Path: "/api/system", Summary: "The sections of settings outside modules", Tags: []string{"System"}},
		func(ctx context.Context, _ *struct{}) (*struct{ Body []Section }, error) {
			all, err := system.Sections(ctx)
			if err != nil {
				return nil, problem(err)
			}
			return &struct{ Body []Section }{all}, nil
		})
	h := settingsHandlers{system}
	huma.Register(api, huma.Operation{OperationID: "get-section-settings", Method: http.MethodGet, Path: "/api/system/{section}/settings", Summary: "A section's current settings and their schema", Tags: []string{"System"}},
		func(ctx context.Context, in *SectionPath) (*settingsOutput, error) { return h.get(ctx, in.Section) })
	huma.Register(api, huma.Operation{OperationID: "put-section-settings", Method: http.MethodPut, Path: "/api/system/{section}/settings", Summary: "Save and apply a section's new settings",
		Description: "Validates the settings against their schema, puts them into effect, rebuilding the running modules that use what changed, and stores the revision. Nothing is stored, and nothing changes, if any step fails. A section that applies on restart (see GET /api/system) is validated and stored only: the process runs with it once restarted. Recorded as system.reconfigured.", Tags: []string{"System"}},
		func(ctx context.Context, in *struct {
			SectionPath
			IfMatch
			Body json.RawMessage
		},
		) (*revisionOutput, error) {
			return h.put(ctx, in.Section, in.IfMatch, in.Body)
		})
	huma.Register(api, huma.Operation{OperationID: "list-section-revisions", Method: http.MethodGet, Path: "/api/system/{section}/settings/revisions", Summary: "A section's settings history, newest first", Tags: []string{"System"}},
		func(ctx context.Context, in *struct {
			SectionPath
			RevisionsQuery
		},
		) (*revisionsPage, error) {
			return h.list(ctx, in.Section, in.RevisionsQuery)
		})
	huma.Register(api, huma.Operation{OperationID: "restore-section-revision", Method: http.MethodPost, Path: "/api/system/{section}/settings/revisions/{revision}/restore", Summary: "Restore a section's earlier settings",
		Description: "Saves the settings of an earlier revision as a new revision and applies them.", Tags: []string{"System"}},
		func(ctx context.Context, in *struct {
			SectionPath
			RevisionPath
			IfMatch
		},
		) (*revisionOutput, error) {
			return h.restore(ctx, in.Section, in.Revision, in.IfMatch)
		})
	huma.Register(api, huma.Operation{OperationID: "list-llm-providers", Method: http.MethodGet, Path: "/api/system/llm/providers", Summary: "The language model providers, with the models last fetched from each",
		Description: "The providers the llm section sets now. Lists are fetched only by POST …/models; a provider removed from the section, or renamed, loses its list.", Tags: []string{"System"}},
		func(ctx context.Context, _ *struct{}) (*struct{ Body []LLMProvider }, error) {
			all, err := system.Providers(ctx)
			if err != nil {
				return nil, problem(err)
			}
			return &struct{ Body []LLMProvider }{append([]LLMProvider{}, all...)}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "fetch-llm-models", Method: http.MethodPost, Path: "/api/system/llm/providers/{name}/models", Summary: "Fetch a provider's models now",
		Description: "Asks the provider for its models that generate content, saves the list in place of the last one, and returns it. When the provider's answer is a failure, the answer is 502 with its reason, and the last list stays. Recorded as llm.models, with the list or the failure, never the key.", Tags: []string{"System"}},
		func(ctx context.Context, in *struct {
			Name string `path:"name" doc:"The provider's name, such as gemini."`
		},
		) (*struct{ Body LLMProvider }, error) {
			p, err := system.FetchModels(ctx, in.Name, caller(ctx))
			if err != nil {
				return nil, problem(err)
			}
			return &struct{ Body LLMProvider }{p}, nil
		})
}

// IfMatch is the revision a change of settings is based on.
type IfMatch struct {
	IfMatch string `header:"If-Match" required:"true" doc:"The ETag of the settings this replaces."`
}

// RevisionPath names one revision of settings in a route.
type RevisionPath struct {
	Revision int64 `path:"revision"`
}

// RevisionsQuery pages through a settings history.
type RevisionsQuery struct {
	Before int64 `query:"before" minimum:"1" doc:"Revisions older than this one."`
	Limit  int   `query:"limit" minimum:"1" maximum:"100" default:"20"`
}

type revisionsPage struct {
	Body struct {
		Revisions []Revision `json:"revisions"`
		More      bool       `json:"more"`
	}
}

// settingsHandlers serves settings kept as revisions, a module's or a
// section's, named by id.
type settingsHandlers struct{ settings Settings }

func (h settingsHandlers) get(ctx context.Context, id string) (*settingsOutput, error) {
	r, schema, err := h.settings.Settings(ctx, id)
	if err != nil {
		return nil, problem(err)
	}
	out := &settingsOutput{ETag: etag(r.Revision)}
	out.Body.Revision, out.Body.Schema = r, schema
	return out, nil
}

func (h settingsHandlers) put(ctx context.Context, id string, ifMatch IfMatch, settings json.RawMessage) (*revisionOutput, error) {
	base, err := revisionOf(ifMatch.IfMatch)
	if err != nil {
		return nil, err
	}
	r, err := h.settings.SaveSettings(ctx, id, base, nil, settings, caller(ctx))
	if err != nil {
		return nil, problem(err)
	}
	return &revisionOutput{ETag: etag(r.Revision), Body: r}, nil
}

func (h settingsHandlers) list(ctx context.Context, id string, page RevisionsQuery) (*revisionsPage, error) {
	before := page.Before
	if before == 0 {
		before = 1<<63 - 1
	}
	revisions, err := h.settings.Revisions(ctx, id, before, page.Limit+1)
	if err != nil {
		return nil, problem(err)
	}
	out := &revisionsPage{}
	out.Body.More = len(revisions) > page.Limit
	out.Body.Revisions = revisions[:min(len(revisions), page.Limit)]
	return out, nil
}

func (h settingsHandlers) restore(ctx context.Context, id string, revision int64, ifMatch IfMatch) (*revisionOutput, error) {
	old, err := h.settings.Revision(ctx, id, revision)
	if err != nil {
		return nil, problem(err)
	}
	return h.put(ctx, id, ifMatch, old.Settings)
}

// baseRevision is the revision a change is based on: If-Match, or else the
// current one.
func baseRevision(ctx context.Context, modules Modules, module, ifMatch string) (int64, error) {
	if ifMatch != "" {
		return revisionOf(ifMatch)
	}
	r, _, err := modules.Settings(ctx, module)
	if err != nil {
		return 0, problem(err)
	}
	return r.Revision, nil
}

// ListedChat is a chat on an allowlist, with what the directory knows of it.
type ListedChat struct {
	AllowedChat
	Directory *Chat `json:"directory,omitempty" doc:"The chat as the bot has seen it; absent for a chat it has not."`
}

// ListPath names a module's allowlist in a route.
type ListPath struct {
	ModulePath
	List string `path:"list" doc:"The allowlist's name."`
}

// ListedChatPath names a chat on an allowlist in a route.
type ListedChatPath struct {
	ListPath
	Chat string `path:"chat" doc:"The chat as /whoami prints it, such as telegram:channel:123."`
}

func registerAllowlists(api huma.API, modules Modules, events *EventLog) {
	// list checks that the module declares the list.
	list := func(ctx context.Context, in ListPath) error {
		all, err := modules.Modules(ctx)
		if err != nil {
			return problem(err)
		}
		for _, m := range all {
			if m.ID == in.Module && slices.ContainsFunc(m.Allowlists, func(l Allowlist) bool { return l.Name == in.List }) {
				return nil
			}
		}
		return huma.Error404NotFound(fmt.Sprintf("module %s has no allowlist %s", in.Module, in.List))
	}
	huma.Register(api, huma.Operation{OperationID: "get-allowlist", Method: http.MethodGet, Path: "/api/modules/{module}/allowlists/{list}", Summary: "The chats on an allowlist", Tags: []string{"Allowlists"}},
		func(ctx context.Context, in *ListPath) (*struct {
			Body struct {
				Chats []ListedChat `json:"chats" nullable:"false"`
			}
		}, error,
		) {
			if err := list(ctx, *in); err != nil {
				return nil, err
			}
			chats, err := events.Chats(ctx)
			if err != nil {
				return nil, problem(err)
			}
			out := &struct {
				Body struct {
					Chats []ListedChat `json:"chats" nullable:"false"`
				}
			}{}
			out.Body.Chats = []ListedChat{}
			for _, a := range modules.Allowlists() {
				if a.Module != in.Module || a.List != in.List {
					continue
				}
				listed := ListedChat{AllowedChat: a}
				if i := slices.IndexFunc(chats, func(c Chat) bool { return c.Key() == a.Chat }); i >= 0 {
					c := chats[i]
					c.LastMessage = nil
					listed.Directory = &c
				}
				out.Body.Chats = append(out.Body.Chats, listed)
			}
			return out, nil
		})
	set := func(allowed bool) func(context.Context, *ListedChatPath) (*struct{}, error) {
		return func(ctx context.Context, in *ListedChatPath) (*struct{}, error) {
			if err := list(ctx, in.ListPath); err != nil {
				return nil, err
			}
			if _, _, err := chatKey(in.Chat); err != nil {
				return nil, err
			}
			if err := modules.SetAllowed(ctx, in.Module, in.List, in.Chat, allowed, caller(ctx)); err != nil {
				return nil, problem(err)
			}
			return nil, nil
		}
	}
	huma.Register(api, huma.Operation{OperationID: "allow-chat", Method: http.MethodPut, Path: "/api/modules/{module}/allowlists/{list}/chats/{chat}", Summary: "Add a chat to an allowlist",
		DefaultStatus: http.StatusNoContent, Tags: []string{"Allowlists"}}, set(true))
	huma.Register(api, huma.Operation{OperationID: "disallow-chat", Method: http.MethodDelete, Path: "/api/modules/{module}/allowlists/{list}/chats/{chat}", Summary: "Remove a chat from an allowlist",
		DefaultStatus: http.StatusNoContent, Tags: []string{"Allowlists"}}, set(false))
}

// SeenChat is a chat the bot has seen, with the allowlists it is on.
type SeenChat struct {
	Chat
	ID    string        `json:"id" doc:"The chat as /whoami prints it."`
	Lists []AllowedChat `json:"lists" nullable:"false" doc:"The allowlists it is on."`
}

// ChatPath names a chat in a route.
type ChatPath struct {
	Chat string `path:"chat" doc:"The chat as /whoami prints it, such as telegram:channel:123."`
}

func registerChats(api huma.API, modules Modules, events *EventLog, media func(context.Context, otogi.Destination, otogi.MediaSource) ([]byte, error)) {
	seen := func(ctx context.Context) ([]SeenChat, error) {
		chats, err := events.Chats(ctx)
		if err != nil {
			return nil, problem(err)
		}
		lists := modules.Allowlists()
		out := make([]SeenChat, len(chats))
		for i, c := range chats {
			out[i] = SeenChat{Chat: c, ID: c.Key(), Lists: []AllowedChat{}}
			for _, a := range lists {
				if a.Chat == c.Key() {
					out[i].Lists = append(out[i].Lists, a)
				}
			}
		}
		return out, nil
	}
	huma.Register(api, huma.Operation{OperationID: "list-chats", Method: http.MethodGet, Path: "/api/chats", Summary: "Every chat the bot has seen, most recently seen first", Tags: []string{"Chats"}},
		func(ctx context.Context, _ *struct{}) (*struct{ Body []SeenChat }, error) {
			chats, err := seen(ctx)
			return &struct{ Body []SeenChat }{chats}, err
		})
	one := func(ctx context.Context, chat string) (SeenChat, error) {
		if _, _, err := chatKey(chat); err != nil {
			return SeenChat{}, err
		}
		chats, err := seen(ctx)
		if err != nil {
			return SeenChat{}, err
		}
		i := slices.IndexFunc(chats, func(c SeenChat) bool { return c.ID == chat })
		if i < 0 {
			return SeenChat{}, huma.Error404NotFound("the bot has not seen chat " + chat)
		}
		return chats[i], nil
	}
	type chatOutput struct {
		Body struct {
			SeenChat
			Days       []ChatDay `json:"days" doc:"New messages per day over the last 7 days, days without any left out."`
			PeopleSeen []Person  `json:"people_seen" nullable:"false" doc:"The people seen sending messages in it, most recently seen first, up to 100."`
		}
	}
	withDays := func(ctx context.Context, c SeenChat) (*chatOutput, error) {
		days, err := events.ChatDays(ctx, c.Platform, c.Conversation, time.Now().AddDate(0, 0, -6))
		if err != nil {
			return nil, problem(err)
		}
		people, err := events.ChatPeople(ctx, c.Platform, c.Conversation, 100)
		if err != nil {
			return nil, problem(err)
		}
		out := &chatOutput{}
		out.Body.SeenChat, out.Body.Days, out.Body.PeopleSeen = c, days, people
		return out, nil
	}
	huma.Register(api, huma.Operation{OperationID: "get-chat", Method: http.MethodGet, Path: "/api/chats/{chat}", Summary: "One chat", Tags: []string{"Chats"}},
		func(ctx context.Context, in *ChatPath) (*chatOutput, error) {
			c, err := one(ctx, in.Chat)
			if err != nil {
				return nil, err
			}
			return withDays(ctx, c)
		})
	huma.Register(api, huma.Operation{OperationID: "name-chat", Method: http.MethodPatch, Path: "/api/chats/{chat}", Summary: "Name a chat",
		Description: "The name wins over the platform's title; an empty name clears it.", Tags: []string{"Chats"}},
		func(ctx context.Context, in *struct {
			ChatPath
			Body struct {
				Name string `json:"name" maxLength:"200"`
			}
		},
		) (*chatOutput, error) {
			platform, conversation, err := chatKey(in.Chat)
			if err != nil {
				return nil, err
			}
			found, err := events.NameChat(ctx, platform, conversation, strings.TrimSpace(in.Body.Name))
			if err != nil {
				return nil, problem(err)
			}
			if !found {
				return nil, huma.Error404NotFound("the bot has not seen chat " + in.Chat)
			}
			c, err := one(ctx, in.Chat)
			if err != nil {
				return nil, err
			}
			return withDays(ctx, c)
		})
	huma.Register(api, huma.Operation{OperationID: "list-chat-messages", Method: http.MethodGet, Path: "/api/chats/{chat}/messages", Summary: "A chat's conversation, newest first",
		Description: "People's messages and the bot's, as long as the event log keeps them. Each of the bot's names the module that sent it and that module's records for the event it answered.", Tags: []string{"Chats"}},
		func(ctx context.Context, in *struct {
			ChatPath
			Before int64 `query:"before" minimum:"1" doc:"Messages older than this message ID."`
			Limit  int   `query:"limit" minimum:"1" maximum:"500" default:"50"`
		},
		) (*struct {
			Body struct {
				Messages []ChatMessage `json:"messages"`
				More     bool          `json:"more"`
			}
		}, error,
		) {
			platform, conversation, err := chatKey(in.Chat)
			if err != nil {
				return nil, err
			}
			messages, more, err := events.ChatMessages(ctx, platform, conversation, in.Before, in.Limit)
			if err != nil {
				return nil, problem(err)
			}
			out := &struct {
				Body struct {
					Messages []ChatMessage `json:"messages"`
					More     bool          `json:"more"`
				}
			}{}
			out.Body.Messages, out.Body.More = messages, more
			return out, nil
		})
	huma.Register(api, huma.Operation{OperationID: "get-chat-media", Method: http.MethodGet, Path: "/api/chats/{chat}/media/{message}/{part}", Summary: "An image or file in a chat's conversation",
		Description: "What the bot sent is served from the event log; what people sent is fetched through the platform, so it is gone once the platform drops it.", Tags: []string{"Chats"}},
		func(ctx context.Context, in *struct {
			ChatPath
			Message int64  `path:"message" doc:"The message ID."`
			Part    string `path:"part" doc:"The media part's ID."`
		},
		) (*huma.StreamResponse, error) {
			platform, conversation, err := chatKey(in.Chat)
			if err != nil {
				return nil, err
			}
			from, m, found, err := events.ChatMedia(ctx, platform, conversation, in.Message, otogi.PartID(in.Part))
			if err != nil {
				return nil, problem(err)
			}
			if !found {
				return nil, huma.Error404NotFound("no such media in chat " + in.Chat)
			}
			data := m.Source.Inline
			if data == nil {
				if data, err = media(ctx, from, m.Source); err != nil {
					return nil, huma.Error502BadGateway("fetch the media from the platform: " + err.Error())
				}
			}
			return &huma.StreamResponse{Body: func(hc huma.Context) {
				hc.SetHeader("Content-Type", cmp.Or(m.MediaType, "application/octet-stream"))
				hc.SetHeader("Cache-Control", "private, max-age=86400")
				_, _ = hc.BodyWriter().Write(data) // the client is gone if this fails
			}}, nil
		})
}

// Status is the process's health.
type Status struct {
	Version   string         `json:"version"`
	StartedAt time.Time      `json:"started_at"`
	Ready     bool           `json:"ready" doc:"Whether every driver is ready."`
	Drivers   []DriverStatus `json:"drivers" nullable:"false"`
	Kernel    KernelStatus   `json:"kernel"`
	EventLog  EventLogStatus `json:"event_log"`
	Running   []Running      `json:"running" nullable:"false" doc:"Deliveries modules are handling now, longest running first."`
}

// DriverStatus is one platform account's driver.
type DriverStatus struct {
	Platform     string    `json:"platform"`
	Account      string    `json:"account"`
	Ready        bool      `json:"ready"`
	Error        string    `json:"error,omitempty"`
	LastEvent    time.Time `json:"last_event,omitzero" doc:"When the event log last received an event from it."`
	PendingInbox int       `json:"pending_inbox" doc:"Updates stored and not yet handled."`
	FailedInbox  int       `json:"failed_inbox" doc:"Updates that could not be handled."`
	Gaps         int       `json:"gaps" doc:"Times the platform said updates were missed."`
	Details      any       `json:"details" doc:"Everything the driver reports, as it reports it."`
}

// KernelStatus is the kernel's load.
type KernelStatus struct {
	State       string `json:"state"`
	InFlight    int    `json:"in_flight" doc:"Events admitted and not yet finished."`
	MaxInFlight int    `json:"max_in_flight"`
	Workers     int    `json:"workers"`
	// HandlerTimeoutMS is negative when deliveries are not bounded.
	HandlerTimeoutMS int64 `json:"handler_timeout_ms" doc:"How long one delivery may take, for a module that does not set its own."`
}

// EventLogStatus is the event log's health and retention.
type EventLogStatus struct {
	Health
	RetentionMS int64 `json:"retention_ms"`
}
