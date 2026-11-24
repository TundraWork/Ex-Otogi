-- +goose Up
-- The pictures operators keep for agents to send: their bytes, what they
-- show, and an operator's note. Kind and summary are copied from the
-- description when the sticker is kept, so this table stands on its own and
-- pruning descriptions leaves it alone.
CREATE TABLE stickers (
    id         INTEGER PRIMARY KEY AUTOINCREMENT, -- never reused
    sha256     BLOB    NOT NULL UNIQUE,
    media_type TEXT    NOT NULL,                  -- image/png, image/jpeg, or image/gif
    bytes      BLOB    NOT NULL,
    kind       TEXT    NOT NULL DEFAULT '',
    summary    TEXT    NOT NULL DEFAULT '',
    note       TEXT    NOT NULL DEFAULT '',
    added_by   TEXT    NOT NULL,                  -- platform:actor
    added_at   INTEGER NOT NULL                   -- unix ms
);

-- The media of recent messages in chats on the describe list, so that
-- /sticker add replying to one finds it: a reply carries only the message's
-- ID. Rows older than recentMedia are ignored, and pruned daily.
CREATE TABLE recent_media (
    platform     TEXT    NOT NULL,
    account      TEXT    NOT NULL,
    conversation TEXT    NOT NULL,
    source_id    TEXT    NOT NULL,
    media        TEXT    NOT NULL, -- JSON []otogi.Media
    at           INTEGER NOT NULL, -- unix ms
    PRIMARY KEY (platform, account, conversation, source_id)
);
CREATE INDEX recent_media_at ON recent_media (at);
