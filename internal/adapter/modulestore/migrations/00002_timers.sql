-- +goose Up
CREATE TABLE timers (
    module     TEXT    NOT NULL,
    key        TEXT    NOT NULL,
    at         INTEGER NOT NULL, -- when it was due (unix ms); identifies the run
    attempt_at INTEGER NOT NULL, -- when to (re)try
    payload    BLOB    NOT NULL,
    PRIMARY KEY (module, key)
);
CREATE INDEX timers_attempt ON timers (attempt_at);
