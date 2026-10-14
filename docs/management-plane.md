# Management Plane

## Purpose

The management plane is how an operator checks and queries a running instance. It is a JSON-over-HTTP API served by the bot process. A future control panel is a client of this API. Control-panel UX needs its own design.

It is an adapter: it reads the event log and component status through their APIs, never their tables or internals.

## Access

The server binds to `127.0.0.1` by default. On any other address it requires a bearer token (see `security.md`).

## Capabilities

**Health**
- liveness: the process is running;
- readiness: required components are ready.

Both are cheap, need no auth on loopback, and are meant for supervisors.

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
