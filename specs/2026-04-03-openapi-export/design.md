# Design: CLI OpenAPI Export

## Overview

Add a CLI subcommand to export the management API's OpenAPI specification as YAML, without starting the HTTP server or requiring a config file.

## Problem

Currently, the management API's OpenAPI spec is only available at runtime through Huma's built-in generation. There is no way to obtain the spec offline for:
- Frontend code generation
- API documentation
- CI integration and validation

## Approach

### Subcommand Design

The current `cmd/bot/main.go` uses a plain `run()` entrypoint with no CLI framework. We add a simple `os.Args` check:

```
./bot openapi   → outputs OpenAPI YAML to stdout, exits 0
./bot           → runs the bot (existing behavior)
```

No CLI framework is needed for a single subcommand.

### OpenAPI Spec Construction

Huma v2's `API.OpenAPI()` returns `*huma.OpenAPI` which has a `.YAML()` method. The management routes registered via `registerRoutes(api, query)` in `managementhttp/routes.go` produce the schema.

To generate the spec without a running server:
1. The `managementhttp` package exposes a new `GenerateOpenAPIYAML()` function
2. This function creates a throwaway `http.ServeMux` + Huma API, registers routes with a nil query (routes are only used for schema extraction, not invocation), and calls `api.OpenAPI().YAML()`

**Key insight**: Huma generates the OpenAPI schema from route registration metadata (input/output structs, path patterns). It does not need a live query implementation. However, `registerRoutes` currently takes a `panel.Query` and creates closures around it. We need to handle this by either:
- (A) Extracting route registration into a schema-only function that registers routes with dummy handlers
- (B) Passing a nil query — the handlers are never called, only their type signatures matter for schema generation

Option B is simpler and sufficient since the handlers are never invoked during spec generation. But `registerRoutes` uses `query` in closures, so passing nil is safe only because we never invoke the handlers. We'll add a comment making this explicit.

### Package-Level API

```go
// in internal/driver/managementhttp/openapi.go

// GenerateOpenAPIYAML produces the management API OpenAPI specification as YAML.
// The returned bytes are a standalone OpenAPI 3.1 document suitable for code
// generation or documentation tooling.
func GenerateOpenAPIYAML() ([]byte, error)
```

### Dependency Flow

- `cmd/bot` imports `internal/driver/managementhttp` (already does)
- `managementhttp.GenerateOpenAPIYAML()` reuses `newHandler`-adjacent logic but skips auth/server
- No changes to `pkg/otogi` or `internal/kernel`

This preserves the architecture dependency flow.

## Non-Goals

- No JSON output format (YAML is the requested format; JSON can be added later)
- No config file parsing needed for this subcommand
- No flag-based customization (server URL, title, etc.) — these can be added later
