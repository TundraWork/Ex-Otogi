-- +goose Up
-- A resident watcher replaces polling, and each chat gets a digest at most
-- once per min_interval: entries queue per chat until then.
ALTER TABLE state DROP COLUMN poll_error;
ALTER TABLE state RENAME COLUMN polled_at TO batch_at; -- last batch from the watcher, unix ms

CREATE TABLE queue (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    platform     TEXT    NOT NULL,
    account      TEXT    NOT NULL,
    conversation TEXT    NOT NULL,
    project      TEXT    NOT NULL,
    entry        TEXT    NOT NULL, -- the watch record, as received
    created_at   INTEGER NOT NULL
);
CREATE INDEX queue_chat ON queue (platform, account, conversation, id);
CREATE INDEX outbox_chat ON outbox (platform, account, conversation, id);
