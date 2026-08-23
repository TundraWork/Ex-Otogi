# Plan: Event Description Field

## Milestone 1: Add Description to panel.Event and SQLite schema

Add the `Description` field to the Go type, the SQLite migration, and the sqlc queries.

**Steps:**
1. Add `Description string` field to `panel.Event` struct in `pkg/otogi/management/types.go`
2. Add `TruncateDescription(s string) string` helper in a new file `pkg/otogi/management/description.go`
3. Add unit test for `TruncateDescription` in `pkg/otogi/management/description_test.go`
4. Create migration `internal/kernel/management/migrations/002_add_description.sql` adding `description TEXT NOT NULL DEFAULT ''`
5. Update `internal/kernel/management/queries/events.sql` InsertEvent to include `description` column
6. Run `sqlc generate` to regenerate `internal/sqlc/management/`

**Verification:** `make quality` passes. `go build ./...` compiles. Existing tests pass.

## Milestone 2: Wire Description through SQLite store

Update the SQLite store to read and write the new field.

**Steps:**
1. Update `RecordEvent` in `internal/kernel/management/sqlite_store.go` to pass `Description` to `InsertEventParams`
2. Update `scanEvent` to map `Description` from the sqlc row
3. Run `make quality`

**Verification:** `make quality` passes. Existing tests pass.

## Milestone 3: Enrich platform.event.received with article text

Update the kernel's `recordInboundEvent` to derive a Description from the platform event's article payload.

**Steps:**
1. In `internal/kernel/command_sink.go`, `recordInboundEvent` method: when the platform event has an `Article` with non-empty `Text`, set `Description` to `TruncateDescription(event.Article.Text)`
2. Run `make quality`

**Verification:** `make quality` passes.

## Milestone 4: Enrich LLM provider events with response/error text

Update the LLM observability layers in both OpenAI and Gemini providers.

**Steps:**
1. In `pkg/llm/providers/openai/observability.go`:
   - `finishSuccess`: set Description to `TruncateDescription(responseText)` from the buffered response
   - `finishFailure`: set Description to `TruncateDescription(fmt.Sprintf("%s: %s", s.provider, streamErr.Error()))`
   - `recordLLMStart`: leave empty (no output yet)
   - `recordEmbeddingStart/Completed/Failed`: set Description on failed to `TruncateDescription(callErr.Error())`
2. In `pkg/llm/providers/gemini/observability.go`: same pattern as OpenAI
3. Run `make quality`

**Verification:** `make quality` passes.

## Milestone 5: Enrich llmchat module events

Update all `emitManagementEvent` calls in the llmchat module.

**Steps:**
1. Update `emitManagementEvent` signature in `modules/llmchat/management.go` to accept a `description string` parameter
2. For `llm.tool.detected`: Description = `TruncateDescription(strings.Join(toolNames, ", "))`
3. For `llm.tool.executed`: Description = `TruncateDescription(fmt.Sprintf("%s (%s)", toolName, successLabel))`
4. For `memory.retrieve.planned`: Description = `TruncateDescription(strings.Join(plan.Queries, ", "))`
5. For other llmchat events (`memory.retrieve.started/searched/completed`): leave empty
7. Update all call sites of `emitManagementEvent` in the module to pass the new parameter
8. Run `make quality`

**Verification:** `make quality` passes.

## Milestone 6: Enrich llmmemory and naturalmemory module events

Update all `emitManagementEvent` calls in both memory modules.

**Steps:**
1. In `modules/llmmemory/management.go`:
   - Update `emitManagementEvent` signature to accept `description string`
   - `memory.store.persistence_loaded`: Description = `TruncateDescription(fmt.Sprintf("loaded %d records", count))`
   - `memory.store.persistence_saved`: Description = `TruncateDescription(fmt.Sprintf("saved %d records", count))`
   - `memory.store.upserted`: leave empty
   - `memory.store.searched/updated/deleted`: leave empty
   - Update all call sites
2. In `modules/naturalmemory/management.go`:
   - Update `emitManagementEvent` signature to accept `description string`
   - `memory.window.flushed`: Description = `TruncateDescription(fmt.Sprintf("%s (%d articles)", reason, articleCount))`
   - `memory.consolidation.pruned`: Description = `TruncateDescription(fmt.Sprintf("%d expired, %d pruned", expiredCount, prunedCount))`
   - `memory.consolidation.capped`: Description = `TruncateDescription(fmt.Sprintf("%d removed (cap %d)", removedCount, maxAllowed))`
   - `memory.extract.started/completed`: leave empty
   - Update all call sites
3. Run `make quality`

**Verification:** `make quality` passes.

## Milestone 7: Enrich runtime async error and platform.event.published

Update the remaining kernel-level event recordings.

**Steps:**
1. In `cmd/bot/app.go`:
   - `runtime.async_error` handler: set Description to `TruncateDescription(fmt.Sprintf("%s: %s", scope, err.Error()))`
   - `platform.event.published` observer: leave empty (no content to excerpt)
2. Run `make quality`

**Verification:** `make quality` passes.

## Milestone 8: Update web UI to display Description

Update the EventsView to show the new description field.

**Steps:**
1. Regenerate the OpenAPI client types (the API schema auto-includes the new field from the Go struct)
2. In `web/src/views/EventsView.vue`, below the Summary `<h2>`, add a conditional paragraph showing `event.Description` when non-empty, styled as secondary text
3. In the event detail drawer, add a Description section
4. Run `make quality`

**Verification:** Web UI shows description text in the event list and detail drawer.
