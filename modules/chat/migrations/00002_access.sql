-- +goose Up
-- Where operators allowed or denied an agent with /chat, on top of the
-- config's chats.
CREATE TABLE access (
    profile TEXT    NOT NULL,
    chat    TEXT    NOT NULL,
    allowed INTEGER NOT NULL,
    by      TEXT    NOT NULL,
    at      INTEGER NOT NULL,
    PRIMARY KEY (profile, chat)
);
