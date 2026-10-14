# Management Plane

## Purpose

The management plane is how an operator checks and queries a running instance. It is a JSON-over-HTTP API served by the bot process. A future control panel is a client of this API. Control-panel UX needs its own design.

It is an adapter: it reads the event log and component status through their APIs, never their tables or internals.

## Access

`management.addr` defaults to `127.0.0.1:8090`; an empty value disables the API. On a non-loopback address, `management.token` is required and sent as `Authorization: Bearer <token>` on `/api/` routes; `/healthz` and `/readyz` stay open for supervisors.

## Routes

| Route | Purpose |
| --- | --- |
| `GET /healthz` | Liveness |
| `GET /readyz` | 200 when every driver is ready, else 503 |
| `GET /api/status` | Version, uptime, kernel stats, driver states with pending/failed inbox rows and gaps, event log health |
| `GET /api/events` | Records, newest first. Filters: `kind`, `component`, `event`, `platform`, `account`, `conversation`, `outcome`, `q` (text in payload), `since`, `until` (RFC 3339). `before=<id>` pages older; `after=<id>` polls newer. `limit` is at most 500 (default 100). The response has `records`, `more`, and `oldest_retained`. |
| `GET /api/events/{id}/trace` | All records of one event, in order |
| `POST /api/shutdown` | Graceful shutdown |

## Capabilities

**Health**
- liveness: the process is running;
- readiness: required components are ready.

Both are cheap, need no auth, and are meant for supervisors.

**Status**
- version, build, config file path, uptime;
- each component's lifecycle state, last error, and when it changed;
- kernel queue depth, durable inbox backlog per driver, recorded gaps;
- event log health and the oldest record still retained.

**Event log query**
- newest-first by default, with cursors to page older or poll newer;
- filters: time range, kind, component, outcome, platform, account, conversation, event ID, and text search over payloads;
- responses contain full records, including content.

**Event trace**
- every record correlated with one event ID (received, each module outcome, each send) in order.

**Lifecycle**
- request a graceful shutdown.

Configuration reload, backup triggers, and other commands are added when operators need them.

## API conventions

- `GET` for reads and `POST` for commands.
- Cursors are opaque and stable: paging never skips or repeats records. An expired cursor returns an error with the oldest cursor still available.
- Errors return a JSON body with a machine-readable code and a human message.
- Page size has a maximum, and larger requests are clamped.
- The API is unstable until the control panel exists. Breaking changes update in-tree clients in the same change.
