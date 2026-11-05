-- +goose Up
-- The bot's own user, from READY: a resumed session does not repeat it.
ALTER TABLE binding ADD COLUMN bot_id TEXT NOT NULL DEFAULT '';
ALTER TABLE binding ADD COLUMN bot_name TEXT NOT NULL DEFAULT '';
