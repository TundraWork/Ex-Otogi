-- +goose Up
CREATE TABLE binding (
    singleton  INTEGER PRIMARY KEY CHECK (singleton = 1),
    account    TEXT    NOT NULL,
    user_id    INTEGER NOT NULL
);

-- gotd updates.StateStorage.
CREATE TABLE update_state (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    pts INTEGER NOT NULL, qts INTEGER NOT NULL, date INTEGER NOT NULL, seq INTEGER NOT NULL
);
CREATE TABLE channel_state (
    channel_id INTEGER PRIMARY KEY,
    pts        INTEGER NOT NULL
);

-- gotd peers.Storage; also backs the update manager's access hashers.
CREATE TABLE peers (
    prefix      TEXT    NOT NULL,
    id          INTEGER NOT NULL,
    access_hash INTEGER NOT NULL,
    PRIMARY KEY (prefix, id)
);
CREATE TABLE peer_phones (
    phone  TEXT PRIMARY KEY,
    prefix TEXT    NOT NULL,
    id     INTEGER NOT NULL
);
CREATE TABLE peer_meta (
    singleton     INTEGER PRIMARY KEY CHECK (singleton = 1),
    contacts_hash INTEGER NOT NULL
);

-- Raw routed updates, persisted before gotd saves its position.
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

CREATE TABLE locators (
    box          TEXT    NOT NULL,
    message_id   INTEGER NOT NULL,
    conversation TEXT    NOT NULL,
    created_at   INTEGER NOT NULL,
    PRIMARY KEY (box, message_id)
);

CREATE TABLE sends (
    key          TEXT PRIMARY KEY,
    conversation TEXT    NOT NULL,
    message_id   INTEGER NOT NULL,
    sent_at      INTEGER NOT NULL
);

CREATE TABLE gaps (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    kind        TEXT    NOT NULL,
    channel_id  INTEGER NOT NULL,
    detected_at INTEGER NOT NULL
);
