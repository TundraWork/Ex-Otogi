-- +goose Up
CREATE TABLE records (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    at           INTEGER NOT NULL, -- unix milliseconds
    kind         TEXT    NOT NULL,
    component    TEXT    NOT NULL,
    event        TEXT    NOT NULL DEFAULT '',
    platform     TEXT    NOT NULL DEFAULT '',
    account      TEXT    NOT NULL DEFAULT '',
    conversation TEXT    NOT NULL DEFAULT '',
    outcome      TEXT    NOT NULL DEFAULT '',
    duration_ms  INTEGER NOT NULL DEFAULT 0,
    payload      TEXT    NOT NULL DEFAULT 'null'
);
CREATE INDEX records_event ON records (event, id);
CREATE INDEX records_kind ON records (kind, id);
CREATE INDEX records_conversation ON records (conversation, id);
CREATE INDEX records_at ON records (at);
