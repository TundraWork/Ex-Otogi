-- +goose Up
-- The QQ message behind each event the driver submitted, so a reply to it
-- can be a passive one, and how many passive replies it has had.
CREATE TABLE origins (
    event_id     TEXT PRIMARY KEY,
    message_id   TEXT    NOT NULL,
    conversation TEXT    NOT NULL,
    received_at  INTEGER NOT NULL,
    replies      INTEGER NOT NULL DEFAULT 0
);

-- Each send by idempotency key: claimed before it goes out, completed with
-- the message QQ created.
CREATE TABLE sends (
    key          TEXT PRIMARY KEY,
    conversation TEXT    NOT NULL,
    msg_id       TEXT    NOT NULL, -- the message replied to; '' for a proactive send
    msg_seq      INTEGER NOT NULL,
    message_id   TEXT    NOT NULL DEFAULT '',
    state        TEXT    NOT NULL CHECK (state IN ('pending', 'sent')),
    created_at   INTEGER NOT NULL
);

CREATE INDEX refs_message ON refs (message_id);
