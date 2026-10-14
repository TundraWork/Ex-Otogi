# Observability

## Principle

An operator must be able to answer "what did the bot receive, what did each module do, what did it send, and what went wrong?" without reading source code or attaching a debugger. Everything needed for that is recorded in full and is queryable. Nothing is redacted, sampled away, or silently dropped.

## Structured event log

The event log is the primary observability tool. It is an append-only SQLite table owned by one event log component and queried through the management plane.

Each record has:

- a monotonically increasing ID and a timestamp;
- a kind, for example `event.received`, `module.completed`, `module.failed`, `module.panicked`, `message.sent`, `message.send_failed`, `driver.state`, `driver.gap`, `process.state`;
- the component (`kernel`, `driver:<name>`, `module:<id>`, `process`);
- the correlated event ID when there is one, plus platform, account, and conversation;
- an outcome and a duration where applicable;
- the full payload as JSON: message content, error chain, panic stack, and so on.

What gets recorded:

- every inbound event with its full rich content;
- every module invocation outcome with duration, including errors and panics with stacks;
- every outbound action with its content and result;
- driver connection state changes, catch-up runs, and gaps;
- process start, ready, drain, stop, and component failures.

Records are written synchronously by the component that produces them. Bot traffic is low enough that a SQLite append per record is cheap, and this avoids a background queue that could drop records. If a write fails, the failure is printed to stdout and shown in status; event processing continues.

Retention is configured by age and/or database size. Pruning deletes only the oldest records, and the management plane reports the oldest record still available.

## Process logs

The process also writes structured logs (`log/slog`, JSON) to stdout. They are for developer diagnostics and for failures that happen before the event log is available, such as startup. The log level is configurable. Logs are not an API.

## Status

The management plane exposes current status: version, uptime, readiness, the state and last error of every component, kernel queue depth, durable inbox backlog per driver, recorded gaps, and event log health. Liveness and readiness checks are available for supervisors.

## Metrics

There are no metrics exporters for now; status and the event log cover operator needs. Prometheus or OpenTelemetry can be added at the management boundary later without changing contracts.
