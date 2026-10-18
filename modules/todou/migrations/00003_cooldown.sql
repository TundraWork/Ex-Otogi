-- +goose Up
-- A chat's cooldown between digests, set with /todou cooldown; chats
-- without a row use min_interval from the config.
CREATE TABLE cooldowns (
    platform     TEXT    NOT NULL,
    account      TEXT    NOT NULL,
    conversation TEXT    NOT NULL,
    cooldown_ms  INTEGER NOT NULL,
    set_by       TEXT    NOT NULL,
    set_at       INTEGER NOT NULL,
    PRIMARY KEY (platform, account, conversation)
);
