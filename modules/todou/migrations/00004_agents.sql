-- +goose Up
-- Several todou agent accounts. A link names the agent it goes through and
-- whether the chat hears the project's activity (silent links only grant
-- access); queued entries keep the agent whose watcher saw them; each agent
-- has its own watch cursor. Rows from before name no agent ('') until the
-- module assigns them to its only agent at start.
ALTER TABLE links ADD COLUMN agent TEXT NOT NULL DEFAULT '';
ALTER TABLE links ADD COLUMN silent INTEGER NOT NULL DEFAULT 0;
ALTER TABLE queue ADD COLUMN agent TEXT NOT NULL DEFAULT '';

CREATE TABLE cursors (
    agent    TEXT    PRIMARY KEY,
    cursor   TEXT    NOT NULL,
    batch_at INTEGER NOT NULL -- last batch from the agent's watcher, unix ms
);
INSERT INTO cursors (agent, cursor, batch_at) SELECT '', cursor, batch_at FROM state;
DROP TABLE state;
