-- +goose Up
CREATE TABLE kv (
    module     TEXT    NOT NULL,
    key        TEXT    NOT NULL,
    value      BLOB    NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (module, key)
);
