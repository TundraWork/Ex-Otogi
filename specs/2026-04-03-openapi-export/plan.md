# Execution Plan: CLI OpenAPI Export

## Milestone 1: Add `GenerateOpenAPIYAML` to `managementhttp` package

**Files**: `internal/driver/managementhttp/openapi.go`

**Tasks**:
1. Create `openapi.go` in `internal/driver/managementhttp/`
2. Implement `GenerateOpenAPIYAML() ([]byte, error)`:
   - Create a throwaway `http.ServeMux` and `huma.DefaultConfig` (same config as `newHandler`)
   - Create Huma API via `humago.New`
   - Call `registerRoutes(api, nil)` — handlers are never invoked, only type metadata matters
   - Call `api.OpenAPI().YAML()` and return the result
3. Add Godoc for the exported function

**Verification**: `go build ./internal/driver/managementhttp/...` compiles without error.

## Milestone 2: Add CLI subcommand dispatch in `cmd/bot`

**Files**: `cmd/bot/main.go`

**Tasks**:
1. Modify `main()` to check `os.Args` for an `openapi` subcommand before calling `run()`
2. When `openapi` is detected:
   - Call `managementhttp.GenerateOpenAPIYAML()`
   - Write the YAML bytes to stdout
   - Exit 0 on success, exit 1 on error
3. All other invocations fall through to existing `run()` behavior

**Verification**: `go build ./cmd/bot/...` compiles. Running `./bot openapi` (or `go run ./cmd/bot openapi`) outputs valid YAML to stdout.

## Milestone 3: Verify nil-query safety in `registerRoutes`

**Files**: `internal/driver/managementhttp/routes.go`

**Tasks**:
1. Add a comment to `registerRoutes` documenting that it is called with a nil query during OpenAPI generation (handlers are never invoked in that path)
2. Verify there are no route-registration-time dereferences of `query` — all usage is inside handler closures (confirmed from reading routes.go)

**Verification**: `go vet ./internal/driver/managementhttp/...` passes.

## Milestone 4: Quality checks

**Tasks**:
1. Run `make quality` to verify lint, vet, and tests pass
2. Ensure the generated OpenAPI YAML is valid (contains expected paths like `/panel/events`, `/panel/overview`, etc.)

**Verification**: `make quality` exits 0. Manual inspection of YAML output confirms all 6 routes are present.
