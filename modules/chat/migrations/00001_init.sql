-- +goose Up
-- What was said in the chats agents serve, for context.
CREATE TABLE transcript (
    platform     TEXT    NOT NULL,
    account      TEXT    NOT NULL,
    conversation TEXT    NOT NULL,
    source_id    TEXT    NOT NULL,
    at           INTEGER NOT NULL, -- unix ms
    actor_id     TEXT    NOT NULL,
    actor_name   TEXT    NOT NULL,
    outgoing     INTEGER NOT NULL, -- sent from the account itself
    text         TEXT    NOT NULL, -- Markdown
    media        TEXT    NOT NULL, -- []otogi.Media as JSON
    reply_to     TEXT    NOT NULL, -- source ID of the message it replies to
    deleted      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (platform, account, conversation, source_id)
);
CREATE INDEX transcript_at ON transcript (platform, account, conversation, at);
CREATE INDEX transcript_source ON transcript (platform, account, source_id);

-- Threads: each turn points at the turn before it, so a thread is a tree.
CREATE TABLE turns (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    profile      TEXT    NOT NULL,
    platform     TEXT    NOT NULL,
    account      TEXT    NOT NULL,
    conversation TEXT    NOT NULL,
    parent       INTEGER,          -- NULL starts a thread
    turn         TEXT    NOT NULL, -- llm.Turn as JSON, media inline
    source_id    TEXT    NOT NULL, -- the user message a user turn came from
    created_at   INTEGER NOT NULL  -- unix ms
);
CREATE INDEX turns_created ON turns (created_at);

-- The messages agents sent, each answering from its turn.
CREATE TABLE sent (
    platform     TEXT    NOT NULL,
    account      TEXT    NOT NULL,
    conversation TEXT    NOT NULL,
    source_id    TEXT    NOT NULL,
    turn         INTEGER NOT NULL,
    PRIMARY KEY (platform, account, conversation, source_id)
);
CREATE INDEX sent_turn ON sent (turn);
