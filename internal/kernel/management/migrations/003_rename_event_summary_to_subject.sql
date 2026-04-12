-- +goose Up
ALTER TABLE events RENAME COLUMN summary TO subject;

-- +goose Down
ALTER TABLE events RENAME COLUMN subject TO summary;
