-- +goose Up
-- What made an agent answer (keyword, reply, mention, decision, follow), so
-- the follow-up share counts only what it said unasked. Turns from before
-- are left empty and count as asked.
ALTER TABLE turns ADD COLUMN cause TEXT NOT NULL DEFAULT '';
