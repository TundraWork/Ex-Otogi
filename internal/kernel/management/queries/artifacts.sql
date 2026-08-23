-- name: InsertArtifact :one
INSERT INTO artifacts (id, event_id, kind, created_at, content)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetArtifact :one
SELECT * FROM artifacts WHERE id = ?;

-- name: ListArtifactIDsByEventID :many
SELECT id FROM artifacts WHERE event_id = ? ORDER BY id;
