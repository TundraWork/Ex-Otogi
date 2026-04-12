-- name: UpsertSnapshot :one
INSERT INTO snapshots (namespace, key, module, updated_at, summary, payload_type, payload_json)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (namespace, key) DO UPDATE SET
  module = excluded.module,
  updated_at = excluded.updated_at,
  summary = excluded.summary,
  payload_type = excluded.payload_type,
  payload_json = excluded.payload_json
RETURNING *;

-- name: ListSnapshots :many
SELECT * FROM snapshots
WHERE (sqlc.arg(namespace) = '' OR namespace = sqlc.arg(namespace))
  AND (sqlc.arg(key) = '' OR key = sqlc.arg(key))
  AND (sqlc.arg(module) = '' OR module = sqlc.arg(module))
ORDER BY namespace, key;