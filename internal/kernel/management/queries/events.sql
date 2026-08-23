-- name: InsertEvent :one
INSERT INTO events (
  occurred_at, trace_id, parent_event_id, category, kind, level,
  module, component, tenant_id, platform, conversation_id, actor_id,
  subject, description, payload_json
) VALUES (
  ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
) RETURNING *;

-- name: GetEvent :one
SELECT * FROM events WHERE id = ?;

-- name: ListLatestEvents :many
SELECT * FROM events
WHERE (sqlc.arg(category) = '' OR category = sqlc.arg(category))
  AND (sqlc.arg(kind) = '' OR kind = sqlc.arg(kind))
  AND (sqlc.arg(trace_id) = '' OR trace_id = sqlc.arg(trace_id))
  AND (sqlc.arg(conversation_id) = '' OR conversation_id = sqlc.arg(conversation_id))
  AND (sqlc.arg(module) = '' OR module = sqlc.arg(module))
  AND ((sqlc.arg(level) = '' AND level != 'debug') OR level = sqlc.arg(level))
ORDER BY id DESC
LIMIT sqlc.arg(limit);

-- name: ListNewerEvents :many
SELECT * FROM events
WHERE (sqlc.arg(category) = '' OR category = sqlc.arg(category))
  AND (sqlc.arg(kind) = '' OR kind = sqlc.arg(kind))
  AND (sqlc.arg(trace_id) = '' OR trace_id = sqlc.arg(trace_id))
  AND (sqlc.arg(conversation_id) = '' OR conversation_id = sqlc.arg(conversation_id))
  AND (sqlc.arg(module) = '' OR module = sqlc.arg(module))
  AND ((sqlc.arg(level) = '' AND level != 'debug') OR level = sqlc.arg(level))
  AND id > sqlc.arg(after_id)
ORDER BY id ASC
LIMIT sqlc.arg(limit);

-- name: ListOlderEvents :many
SELECT * FROM events
WHERE (sqlc.arg(category) = '' OR category = sqlc.arg(category))
  AND (sqlc.arg(kind) = '' OR kind = sqlc.arg(kind))
  AND (sqlc.arg(trace_id) = '' OR trace_id = sqlc.arg(trace_id))
  AND (sqlc.arg(conversation_id) = '' OR conversation_id = sqlc.arg(conversation_id))
  AND (sqlc.arg(module) = '' OR module = sqlc.arg(module))
  AND ((sqlc.arg(level) = '' AND level != 'debug') OR level = sqlc.arg(level))
  AND id < sqlc.arg(before_id)
ORDER BY id DESC
LIMIT sqlc.arg(limit);

-- name: GetTraceEvents :many
SELECT * FROM events
WHERE trace_id = ?
ORDER BY id ASC;

-- name: CountEventsByCategory :many
SELECT category, COUNT(*) AS count FROM events GROUP BY category;

-- name: CountRecentErrors :one
SELECT COUNT(*) FROM events WHERE level = 'error';

-- name: CountDistinctTraceIDs :one
SELECT COUNT(DISTINCT trace_id) FROM events WHERE trace_id != '';

-- name: MinEventID :one
SELECT COALESCE(MIN(id), 0) FROM events;

-- name: MaxEventID :one
SELECT COALESCE(MAX(id), 0) FROM events;

-- name: CountEvents :one
SELECT COUNT(*) FROM events;
