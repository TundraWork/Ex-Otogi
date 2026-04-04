# API Design: Management Observability Foundation

## Overview

Phase 1 exposes a read-only polling API for the management frontend. All endpoints require:

- `Authorization: Bearer <token>`

Default bind address:

- `127.0.0.1`

The API is intentionally query-only in phase 1.

## Endpoints

### `GET /panel/events`

Incrementally fetch timeline events newer than one cursor.

Query parameters:

- `after_id int64`: return events with `id > after_id`
- `limit int`: optional page size
- `category string`: optional filter
- `kind string`: optional filter
- `trace_id string`: optional filter
- `conversation_id string`: optional filter
- `module string`: optional filter
- `level string`: optional filter

Response body:

```json
{
  "items": [
    {
      "id": 101,
      "occurred_at": "2026-04-01T11:22:33Z",
      "trace_id": "trc_abc123",
      "parent_event_id": null,
      "category": "llm",
      "kind": "llm.call.started",
      "level": "debug",
      "module": "llmchat",
      "component": "openai-provider",
      "tenant_id": "",
      "platform": "telegram",
      "conversation_id": "12345",
      "actor_id": "user-1",
      "summary": "started LLM generation",
      "payload_type": "LLMCallStartedPayload",
      "payload": {
        "provider": "openai",
        "model": "gpt-5.4",
        "message_count": 6,
        "tool_count": 2
      },
      "artifact_ids": ["art_001"]
    }
  ],
  "last_id": 101,
  "window_start_id": 80,
  "window_end_id": 101,
  "cursor_reset_required": false,
  "has_more": false
}
```

Behavior:

- results are ordered by ascending event ID
- `after_id=0` starts from the current retained window start
- if `after_id` is older than the retained window, `cursor_reset_required` must be `true`

### `GET /panel/events/{id}`

Fetch one event envelope by ID.

Response:

- `404` if the event has been evicted or never existed in the current process lifetime

### `GET /panel/traces/{trace_id}`

Fetch the current retained event timeline for one trace.

Query parameters:

- `limit int`: optional cap

Response body:

```json
{
  "trace_id": "trc_abc123",
  "items": []
}
```

### `GET /panel/artifacts/{id}`

Fetch one full debug artifact.

Response body:

```json
{
  "id": "art_001",
  "event_id": 101,
  "kind": "prompt.full",
  "created_at": "2026-04-01T11:22:33Z",
  "content": "..."
}
```

### `GET /panel/snapshots`

List current snapshot records.

Query parameters:

- `namespace string`
- `key string`
- `module string`

Response body:

```json
{
  "items": []
}
```

### `GET /panel/overview`

Return a lightweight dashboard summary for polling.

Suggested fields:

- current retained event window
- event counts by category in the current window
- recent error count
- active provider inflight counts
- recent trace count

## Common DTO Shapes

### Event Envelope

```json
{
  "id": 0,
  "occurred_at": "2026-04-01T11:22:33Z",
  "trace_id": "trc_xxx",
  "parent_event_id": null,
  "category": "memory",
  "kind": "memory.retrieve.completed",
  "level": "debug",
  "module": "llmchat",
  "component": "semantic-memory",
  "tenant_id": "",
  "platform": "telegram",
  "conversation_id": "12345",
  "actor_id": "user-1",
  "summary": "semantic memory retrieval completed",
  "payload_type": "MemoryRetrieveCompletedPayload",
  "payload": {},
  "artifact_ids": []
}
```

### Example Payload DTOs

`MemoryRetrievePlanPayload`

```json
{
  "queries": ["project deadline", "deployment issue"],
  "time_filter": "recent",
  "depth": "deep",
  "planner_used": true
}
```

`EmbeddingCallCompletedPayload`

```json
{
  "provider": "openai",
  "model": "text-embedding-3-large",
  "task_type": "query",
  "input_count": 3,
  "dimensions": 3072,
  "elapsed_ms": 85
}
```

`LLMCallFailedPayload`

```json
{
  "provider": "openai",
  "model": "gpt-5.4",
  "elapsed_ms": 4210,
  "error": "context deadline exceeded"
}
```

## Error Semantics

Suggested status mapping:

- `401 Unauthorized`: missing or invalid bearer token
- `400 Bad Request`: malformed query parameters
- `404 Not Found`: event/artifact not found in retained process memory
- `500 Internal Server Error`: unexpected query failure

## Backward Compatibility

Because the frontend will consume typed payloads, the API should treat the following fields as stable from the first implementation:

- `id`
- `occurred_at`
- `trace_id`
- `category`
- `kind`
- `level`
- `payload_type`
- `payload`
- `artifact_ids`
