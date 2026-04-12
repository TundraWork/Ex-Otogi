-- +goose Up
CREATE TABLE events (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  occurred_at     DATETIME NOT NULL,
  trace_id        TEXT NOT NULL DEFAULT '',
  parent_event_id INTEGER,
  category        TEXT NOT NULL,
  kind            TEXT NOT NULL,
  level           TEXT NOT NULL,
  module          TEXT NOT NULL DEFAULT '',
  component       TEXT NOT NULL DEFAULT '',
  tenant_id       TEXT NOT NULL DEFAULT '',
  platform        TEXT NOT NULL DEFAULT '',
  conversation_id TEXT NOT NULL DEFAULT '',
  actor_id        TEXT NOT NULL DEFAULT '',
  summary         TEXT NOT NULL DEFAULT '',
  payload_type    TEXT NOT NULL DEFAULT '',
  payload_json    TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE artifacts (
  id         TEXT PRIMARY KEY,
  event_id   INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  kind       TEXT NOT NULL,
  created_at DATETIME NOT NULL,
  content    TEXT NOT NULL DEFAULT ''
);

CREATE TABLE snapshots (
  namespace    TEXT NOT NULL,
  key          TEXT NOT NULL,
  module       TEXT NOT NULL DEFAULT '',
  updated_at   DATETIME NOT NULL,
  summary      TEXT NOT NULL DEFAULT '',
  payload_type TEXT NOT NULL DEFAULT '',
  payload_json TEXT NOT NULL DEFAULT '{}',
  PRIMARY KEY (namespace, key)
);

CREATE INDEX idx_events_occurred_at ON events(occurred_at);
CREATE INDEX idx_events_kind ON events(kind);
CREATE INDEX idx_events_trace_id ON events(trace_id);
CREATE INDEX idx_events_category ON events(category);
CREATE INDEX idx_events_conversation_id ON events(conversation_id);
CREATE INDEX idx_events_module ON events(module);
CREATE INDEX idx_artifacts_event_id ON artifacts(event_id);
CREATE INDEX idx_snapshots_namespace ON snapshots(namespace);
CREATE INDEX idx_snapshots_module ON snapshots(module);

-- +goose Down
DROP TABLE IF EXISTS artifacts;
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS snapshots;
