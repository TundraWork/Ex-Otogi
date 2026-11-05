-- +goose Up
CREATE TABLE binding (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    account   TEXT    NOT NULL,
    app_id    TEXT    NOT NULL
);

-- The gateway session, kept so a restart can resume it.
CREATE TABLE session (
    singleton  INTEGER PRIMARY KEY CHECK (singleton = 1),
    session_id TEXT    NOT NULL,
    seq        INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- Raw dispatches, persisted before their sequence number is taken as seen.
CREATE TABLE inbox (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    dedup_key   TEXT    NOT NULL UNIQUE,
    payload     BLOB    NOT NULL,
    received_at INTEGER NOT NULL,
    state       TEXT    NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'done', 'failed')),
    error       TEXT,
    finished_at INTEGER
);
CREATE INDEX inbox_pending ON inbox (state, id);

-- A message's REFIDX (what quotes name) and its message ID.
CREATE TABLE refs (
    ref_idx      TEXT PRIMARY KEY,
    message_id   TEXT    NOT NULL,
    conversation TEXT    NOT NULL,
    created_at   INTEGER NOT NULL
);

CREATE TABLE gaps (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    kind        TEXT    NOT NULL,
    detail      TEXT    NOT NULL,
    detected_at INTEGER NOT NULL
);
