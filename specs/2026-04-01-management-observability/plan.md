# Execution Plan: Management Observability Foundation

## Milestone 1: Define Public Management Contracts
**Files:** `pkg/otogi/management/*`, `pkg/otogi/doc.go` if package index needs updates
**Success criterion:** A new contract package defines stable recorder/query interfaces, unified event envelope DTOs, typed payload DTOs, artifact DTOs, query filters, and service registry keys without introducing runtime implementation details.

Steps:
1. Create `pkg/otogi/management` with package docs that explain its contract-layer role.
2. Define stable service keys for recorder/query access through the existing service registry.
3. Define the unified `Event` envelope with global monotonic ID, trace metadata, routing metadata, summary, `payload_type`, `payload`, and artifact references.
4. Define event categories, levels, artifact kinds, and query filter DTOs.
5. Define typed payload DTOs for the first-phase high-value event kinds:
   - business event ingress/publish
   - memory retrieval
   - memory extraction/store
   - embedding calls
   - LLM calls
   - tool calls
   - runtime async errors
6. Define write-side and read-side interfaces with context-first method signatures and clear Godoc semantics.

Verification:
1. Run package tests for the new contract package.
2. Confirm all exported symbols and fields have Godoc.
3. Confirm `pkg/otogi/management` imports no `internal/*` packages and contains no goroutine or transport logic.

## Milestone 2: Implement In-Memory Sliding-Window Store
**Files:** `internal/kernel/management/*`
**Success criterion:** The kernel owns a concurrency-safe in-memory implementation that assigns global monotonic event IDs, stores artifacts and snapshots, enforces sliding-window retention, and supports incremental polling with cursor reset detection.

Steps:
1. Implement a recorder/query service backed by in-memory storage.
2. Add global event ID assignment using atomic monotonic increments.
3. Implement ordered event retention with bounded window semantics.
4. Implement artifact storage and retention accounting.
5. Implement optional keyed snapshot storage for current-state views.
6. Implement event querying by `after_id`, `trace_id`, `conversation_id`, `category`, `kind`, `module`, and `level`.
7. Return polling metadata including:
   - `last_id`
   - `window_start_id`
   - `window_end_id`
   - `cursor_reset_required`
   - `has_more`

Verification:
1. Add table-driven tests for:
   - monotonic ID assignment
   - ordered event reads
   - retention eviction
   - cursor reset detection when `after_id` falls behind the retained window
   - artifact lookup and eviction
2. Run `go test ./internal/kernel/...`.
3. Run `go test -race ./internal/kernel/...` and confirm there are no race failures or goroutine leaks.

## Milestone 3: Add Trace Propagation Helpers and Runtime Wiring
**Files:** `internal/kernel/*`, `cmd/bot/app.go`, and any required `pkg/otogi/management` context helpers
**Success criterion:** Kernel runtime wiring registers the management services, and inbound handling establishes a reusable trace ID so related module/provider events can be grouped by one trace.

Steps:
1. Decide whether trace context helpers live in `pkg/otogi/management` or remain internal while preserving contract cleanliness.
2. Generate one trace ID at the start of each inbound platform-event handling path.
3. Ensure module handlers and provider calls receive contexts carrying the same trace metadata.
4. Register recorder/query services in the kernel service registry during runtime construction.
5. Introduce management configuration in `config/bot.json` parsing:
   - enable flag
   - listen address
   - bearer token
   - event window limits
   - artifact limits

Verification:
1. Add tests proving the same trace ID is observable across multiple recorded events in one processing chain.
2. Add config parsing tests for enabled/disabled states and required token behavior.
3. Confirm architecture boundaries remain intact: `pkg/otogi -> internal/kernel -> internal/driver`.

## Milestone 4: Instrument Kernel and High-Value Modules
**Files:** `internal/kernel/*`, `modules/llmchat/*`, `modules/naturalmemory/*`, `modules/llmmemory/*`
**Success criterion:** High-value business and memory flows publish structured management events in addition to existing debug logs.

Steps:
1. Add kernel-level events for inbound publish lifecycle and async errors.
2. Instrument `modules/llmchat` for:
   - semantic retrieval start
   - retrieval plan
   - search/rank result
   - tool-call detection/execution
3. Instrument `modules/naturalmemory` for:
   - article extraction start/result
   - synthesis decisions
   - consolidation activity
4. Instrument `modules/llmmemory` for:
   - store/search/update/delete
   - persistence load/save
5. Preserve current `slog` diagnostics; management events are additive, not a replacement.

Verification:
1. Add focused tests or integration-style tests that assert events are emitted with expected `kind`, `payload_type`, and trace metadata.
2. Confirm nil recorder or disabled management does not break existing module behavior.
3. Re-run relevant package tests with race detection where practical.

## Milestone 5: Instrument LLM and Embedding Providers
**Files:** `pkg/llm/providers/openai/*`, `pkg/llm/providers/gemini/*`, and any shared provider helpers
**Success criterion:** Provider implementations become the canonical source for embedding and LLM lifecycle events, including prompt artifacts and failure metadata.

Steps:
1. Resolve management recorder access in provider construction or request context without violating existing boundaries.
2. Emit LLM provider events:
   - started
   - completed
   - failed
   - optional stream-first timing markers if useful
3. Record prompt and response preview artifacts linked to the provider event IDs.
4. Emit embedding provider events:
   - started
   - completed
   - failed
   - model/provider metadata
   - dimensions/input-count metadata
5. Ensure provider instrumentation is best-effort and does not change request success semantics when observability fails.

Verification:
1. Add table-driven tests for successful and failed provider calls that assert management event emission.
2. Confirm prompt artifacts preserve original content as requested.
3. Re-run provider package tests and relevant race tests.

## Milestone 6: Expose Query API Through Huma
**Files:** new HTTP adapter package under `internal/driver` or equivalent wiring under `cmd/bot`, plus any new config/example files
**Success criterion:** A Huma-based read-only HTTP API exposes event, trace, artifact, snapshot, and overview queries with bearer-token authentication and configurable listen address.

Steps:
1. Introduce an internal HTTP adapter package for the management API, keeping Huma isolated from contract and kernel packages.
2. Implement bearer-token middleware using constant-time comparison.
3. Add endpoints:
   - `GET /panel/events`
   - `GET /panel/events/{id}`
   - `GET /panel/traces/{trace_id}`
   - `GET /panel/artifacts/{id}`
   - `GET /panel/snapshots`
   - `GET /panel/overview`
4. Implement incremental polling response shape with window metadata and cursor reset signal.
5. Wire the server lifecycle into application startup and shutdown with explicit context cancellation and timeout handling.
6. Update example config documentation for management API settings.

Verification:
1. Add HTTP tests for:
   - missing token
   - invalid token
   - successful authorized query
   - cursor reset behavior
   - missing artifact or event
2. Confirm the server defaults to `127.0.0.1` when enabled unless configured otherwise.
3. Confirm shutdown is clean and bounded by context deadlines.

## Milestone 7: Quality Gate and Final Validation
**Success criterion:** The management foundation passes repository quality gates and the implemented scope matches the proposal/design/api documents.

Steps:
1. Run formatting and generation steps if any new generated code or docs are introduced.
2. Run targeted tests for all touched packages.
3. Run `go test -race ./...`.
4. Run `make quality`.
5. Manually verify one end-to-end flow:
   - process inbound event
   - emit trace-linked module/provider events
   - query them via `/panel/events?after_id=...`
   - fetch prompt artifact by ID

Verification:
1. `go test -race ./...` passes.
2. `make quality` passes.
3. End-to-end polling and artifact lookup behave as designed.
