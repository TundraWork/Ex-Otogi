# Design: Management Observability Foundation

## Overview

This change introduces a first-class management observability plane for Ex-Otogi. The first phase focuses on read-only debugging and query workflows:

- business event timeline
- semantic memory retrieval and recall
- memory extraction and storage
- embedding call status
- LLM request lifecycle, including prompt artifacts

The design must preserve the repository architecture:

1. `pkg/otogi` defines stable contracts only
2. `internal/kernel` owns runtime implementation and in-memory storage
3. transport and HTTP adaptation live outside `pkg/otogi`

The management stack is therefore split into three layers:

- `pkg/otogi/management`: public contracts, DTOs, service names, and query interfaces
- `internal/kernel/management`: in-memory recorder/query implementation with sliding-window retention
- HTTP server layer: Huma-based query API adapted from the management service and protected by bearer token authentication

## Goals

- Provide one stable programming API for modules, kernel code, drivers, and providers to emit structured observability records
- Support incremental polling by the frontend using a global monotonic event ID
- Preserve full prompt and debug artifact content in memory for local debugging
- Allow provider-level tracing to become the canonical source for embedding and LLM call statistics
- Keep write-path overhead bounded and isolated from business logic

## Non-Goals

- No durable storage in phase 1
- No command or mutation endpoints in phase 1
- No websocket or SSE streaming in phase 1
- No generic log ingestion pipeline that replaces `slog`

## Architectural Placement

### 1. `pkg/otogi/management`

This package is a public contract layer and must stay implementation-agnostic.

It should define:

- stable service registry keys
- the write-side recording interface
- the read-side query interface
- event envelope DTOs
- event payload DTOs
- artifact DTOs
- query filter DTOs
- trace propagation helpers if they are contract-level and implementation-agnostic

It must not contain:

- goroutine lifecycle management
- HTTP handlers or Huma setup
- in-memory or persistent storage implementation
- startup wiring or runtime configuration parsing

### 2. `internal/kernel/management`

This package owns the concrete runtime implementation:

- monotonic event ID generation
- in-memory event ring buffer
- artifact storage
- optional snapshots/current-state storage
- event filtering and pagination
- cursor reset detection when frontend polling falls behind the sliding window
- dependency injection into the existing service registry

This keeps the write/read semantics inside the kernel while exposing only stable contracts upward.

### 3. HTTP/Huma Adapter

The HTTP layer should be implemented outside `pkg/otogi`, either as:

- a new internal HTTP driver package, or
- a command-side adapter assembled from `cmd/bot`

Its job is limited to:

- bearer-token authentication
- request validation
- mapping HTTP DTOs to management query DTOs
- serializing response payloads for the frontend

## Data Model

### Event Envelope

The system uses one unified event envelope so the frontend can render a single chronological timeline while still understanding each event's typed payload.

Recommended fields:

- `id int64`: global monotonic event ID
- `occurred_at time.Time`
- `trace_id string`
- `parent_event_id *int64`
- `category EventCategory`
- `kind string`
- `level EventLevel`
- `module string`
- `component string`
- `tenant_id string`
- `platform string`
- `conversation_id string`
- `actor_id string`
- `summary string`
- `payload_type string`
- `payload any`
- `artifact_ids []string`

### Category and Kind

`category` is a coarse-grained frontend grouping dimension. `kind` is the stable machine-readable identifier for the specific event.

Initial categories:

- `runtime`
- `business_event`
- `memory`
- `embedding`
- `llm`
- `tool`
- `driver`
- `http_api`

Initial high-value kinds:

- `platform.event.received`
- `platform.event.published`
- `memory.retrieve.started`
- `memory.retrieve.planned`
- `memory.retrieve.searched`
- `memory.retrieve.completed`
- `memory.extract.started`
- `memory.extract.completed`
- `memory.store.upserted`
- `embedding.call.started`
- `embedding.call.completed`
- `embedding.call.failed`
- `llm.call.started`
- `llm.call.completed`
- `llm.call.failed`
- `llm.tool.detected`
- `llm.tool.executed`
- `runtime.async_error`

### Typed Payload DTOs

Each event kind carries its own payload DTO rather than collapsing all event data into untyped maps.

Examples:

- `PlatformEventReceivedPayload`
- `MemoryRetrieveStartedPayload`
- `MemoryRetrievePlanPayload`
- `MemoryRetrieveCompletedPayload`
- `MemoryStoreUpsertedPayload`
- `EmbeddingCallStartedPayload`
- `EmbeddingCallCompletedPayload`
- `EmbeddingCallFailedPayload`
- `LLMCallStartedPayload`
- `LLMCallCompletedPayload`
- `LLMCallFailedPayload`
- `LLMToolDetectedPayload`
- `LLMToolExecutedPayload`

This model keeps the list view uniform while allowing details panels to render kind-specific data safely.

### Artifacts

Large or debugging-heavy text blobs should be stored separately from timeline events.

Examples:

- system prompt
- user prompt
- full assembled prompt
- retrieval plan
- retrieval result serialization
- embedding input preview
- LLM response preview

Recommended fields:

- `id string`
- `event_id int64`
- `kind ArtifactKind`
- `created_at time.Time`
- `content string`

Because the user explicitly prefers debugging fidelity, phase 1 keeps original content and does not truncate artifacts. This increases memory pressure and must be addressed through bounded sliding-window retention.

### Snapshots

Although the first phase is timeline-heavy, a lightweight snapshot model is still useful for current-state panels.

Examples:

- current inflight request counts by provider
- recent module health state
- recent backlog depth

Snapshots should be keyed, replace-in-place records instead of append-only timeline events.

## Trace Propagation

The management view becomes significantly more useful once all related work for one inbound event can be grouped by `trace_id`.

Recommended approach:

- kernel generates a trace ID when handling one inbound platform event
- trace ID is attached to `context.Context`
- modules and providers reuse the same context
- recorder extracts trace metadata from context automatically when possible

This allows the frontend to inspect one full chain:

`platform event -> semantic retrieval -> embedding call -> LLM call -> tool execution -> outbound reply`

## Sliding Window and Retention

Phase 1 is intentionally memory-only. Retention must therefore be bounded.

Recommended limits:

- max event count
- max artifact count
- max artifact total bytes
- max snapshot count

Retention policy:

- events are evicted from oldest to newest by event ID
- artifact records are evicted in age order once they fall outside retained event references or storage limits
- snapshots are overwritten by key

## Polling Model

The frontend polls incrementally using `after_id`.

Required query semantics:

- fetch events strictly newer than `after_id`
- return `last_id` for the newest event in the response window
- return window bounds so the client can detect whether its cursor is too old
- return `cursor_reset_required=true` when the client has fallen behind retention and missed data

This is necessary because a sliding window can evict old events between polls.

## Authentication and Exposure

Phase 1 security model:

- management API listen address is configurable
- default listen address is `127.0.0.1`
- all HTTP endpoints require bearer token authentication
- if the management API is enabled, token configuration is mandatory

The design assumes debug-oriented access, but it still treats prompt and memory artifacts as sensitive data.

## Producer Responsibilities

### Kernel Producers

Kernel should emit:

- event-bus publish lifecycle
- async error notifications
- module lifecycle events

### Module Producers

High-value module integration points:

- `modules/llmchat`: semantic retrieval planning, search, ranking, tool execution
- `modules/naturalmemory`: extraction, synthesis, consolidation, linking
- `modules/llmmemory`: store/search/update/delete and persistence actions

### Provider Producers

Providers are the canonical source for call-level observability.

LLM providers should emit:

- request started
- request completed
- request failed
- model/provider metadata
- prompt artifacts
- response preview artifacts

Embedding providers should emit:

- call started
- call completed
- call failed
- provider/model metadata
- input count and dimensions
- optional input artifacts

## Dependency and Library Considerations

The design does not require any new algorithmic dependency for the in-memory store; a manual bounded ring buffer implementation is sufficient and preferable for tight control over memory and cursor semantics.

The HTTP adapter is expected to use Huma as requested by the user. Huma should remain confined to the transport layer and must not leak into `pkg/otogi/management`.
