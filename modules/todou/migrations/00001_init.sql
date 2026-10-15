-- +goose Up
CREATE TABLE state (
    singleton  INTEGER PRIMARY KEY CHECK (singleton = 1),
    cursor     TEXT    NOT NULL,
    polled_at  INTEGER NOT NULL DEFAULT 0, -- last successful poll, unix ms
    poll_error TEXT    NOT NULL DEFAULT ''  -- last poll failure, cleared on success
);

CREATE TABLE links (
    platform     TEXT    NOT NULL,
    account      TEXT    NOT NULL,
    conversation TEXT    NOT NULL,
    project      TEXT    NOT NULL,
    created_by   TEXT    NOT NULL,
    created_at   INTEGER NOT NULL,
    PRIMARY KEY (platform, account, conversation, project)
);
CREATE INDEX links_project ON links (project);

-- Rendered notifications, committed with the cursor and delivered in id order.
CREATE TABLE outbox (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    platform     TEXT    NOT NULL,
    account      TEXT    NOT NULL,
    conversation TEXT    NOT NULL,
    message      TEXT    NOT NULL, -- otogi.Message as JSON
    created_at   INTEGER NOT NULL,
    sent_at      INTEGER,
    failed_at    INTEGER,
    attempts     INTEGER NOT NULL DEFAULT 0,
    last_error   TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX outbox_pending ON outbox (id) WHERE sent_at IS NULL AND failed_at IS NULL;
