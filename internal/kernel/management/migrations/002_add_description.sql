-- +goose Up
ALTER TABLE events ADD COLUMN description TEXT NOT NULL DEFAULT '';

-- +goose Down
-- SQLite does not support DROP COLUMN before version 3.35.0.
-- For older SQLite, the down migration would require recreating the table.
ALTER TABLE events DROP COLUMN description;
