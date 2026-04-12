# Design: Event Description Field

## Overview

Add a `Description` field to `panel.Event` that carries a brief human-readable excerpt derived from the event payload. This field is distinct from `Summary` (which is a fixed label like "received inbound platform event") and `Payload` (which is a structured DTO). Description provides the "what" of the event — a snippet of the actual content.

## Data Model

### panel.Event (Go struct)

New field:
```go
// Description carries a brief human-readable excerpt derived from the event
// payload. Maximum 140 Unicode characters. Empty when no relevant content
// is available.
Description string
```

### SQLite Schema

New column on `events` table:
```sql
description TEXT NOT NULL DEFAULT ''
```

New migration file: `002_add_description.sql`

### SQLC Generated Code

After re-running `sqlc generate`, the `Event` struct, `InsertEventParams`, and all scan sites will include `Description`.

## Description Source by Event Kind

| Event Kind | Description Source | Logic |
|---|---|---|
| `platform.event.received` | `event.Article.Text` | First 140 runes of article text (if article is present) |
| `platform.event.published` | — | Empty (no meaningful content) |
| `llm.call.started` | — | Empty (no output yet) |
| `llm.call.completed` | Response text (from artifact) | First 140 runes of the LLM response text |
| `llm.call.failed` | Error message | First 140 runes of the error string |
| `llm.tool.detected` | Tool names joined | Comma-separated tool names, first 140 runes |
| `llm.tool.executed` | Tool name + status | `"<toolName> (success/failure)"` |
| `embedding.call.started` | — | Empty |
| `embedding.call.completed` | — | Empty |
| `embedding.call.failed` | Error message | First 140 runes |
| `memory.retrieve.started` | — | Empty |
| `memory.retrieve.planned` | Queries joined | Comma-separated queries, first 140 runes |
| `memory.retrieve.searched` | — | Empty |
| `memory.retrieve.completed` | — | Empty |
| `memory.extract.started` | — | Empty |
| `memory.extract.completed` | — | Empty |
| `memory.window.flushed` | Reason | `"<reason> (<count> articles)"` |
| `memory.consolidation.pruned` | Summary | `"<expiredCount> expired, <prunedCount> pruned"` |
| `memory.consolidation.capped` | Summary | `"<removedCount> removed (cap <maxAllowed>)"` |
| `memory.store.upserted` | — | Empty |
| `memory.store.persistence_loaded` | — | Empty |
| `memory.store.persistence_saved` | — | Empty |
| `memory.store.searched` | — | Empty |
| `memory.store.updated` | — | Empty |
| `memory.store.deleted` | — | Empty |
| `runtime.async_error` | Error message | First 140 runes of operation + error |

## Truncation Utility

A shared helper `TruncateDescription(s string) string` in `pkg/otogi/management` will:
1. Normalize whitespace (join with single spaces)
2. Truncate to 140 runes with ellipsis suffix (`...`) if needed
3. Return empty string for blank input

This is similar to the existing `trimRunesWithEllipsis` in the llmchat module but placed in the shared management package.

## Web UI Impact

The `EventsView.vue` currently displays `event.Summary` as the event title. After this change, the UI should show `event.Description` as a secondary line below Summary when it's non-empty, providing at-a-glance content preview without opening the drawer.

The OpenAPI schema is auto-generated from the Go struct, so adding `Description` to `panel.Event` automatically includes it in the API response. The frontend TypeScript types are generated from the OpenAPI spec, so they'll pick it up on regeneration.
