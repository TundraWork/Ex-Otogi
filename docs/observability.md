# Observability

## Principle

An operator must be able to answer "what did the bot receive, what did each module do, what did it send, and what went wrong?" without reading source code or attaching a debugger. Everything needed for that is recorded in full and is queryable. Nothing is redacted, sampled away, or silently dropped.

## Structured event log

The event log is the primary observability tool. It is an append-only SQLite table owned by one event log component and queried through the management plane.

Each record has:

- a monotonically increasing ID and a timestamp;
- a kind: `event.received`, `event.dropped`, `module.completed`, `module.failed`, `module.panicked`, `effect.applied`, `effect.failed`, `driver.state`, `driver.gap`, `inbox.failed`, `llm.completed`, `llm.failed`, or `process.state`;
- the component (`kernel`, `module:<id>`, `middleware:<id>`, `driver:telegram/<account>`, `llm:<provider>`, `process`);
- the correlated event ID when there is one, plus platform, account, and conversation;
- an outcome and a duration where applicable;
- the full payload as JSON: message content, error chain, panic stack, and so on.

What gets recorded:

- every inbound event with its full rich content;
- every module invocation outcome with duration, including errors and panics with stacks;
- every outbound action with its content and result; a streamed message is one record when it closes, with its final content, the platform messages holding it, and how many updates it received and showed (each update is a prefix of the final content, so it is not recorded again);
- every language model call: the calling module and event, the model and settings, the system instruction and every turn sent, the reply's steps (thoughts included) and Markdown, how it ended, token usage, time to first output, and every HTTP request with its status and the full body of each error. Media in turns appears as its size and SHA-256: a conversation's images are resent on every call, and the bytes stay in the platform message and the module's history;
- driver connection state changes, catch-up runs, and gaps;
- process start, ready, drain, stop, and component failures.

Records are written synchronously by the component that produces them. Bot traffic is low enough that a SQLite append per record is cheap, and this avoids a background queue that could drop records. If a write fails, the failure is printed to stdout and shown in status; event processing continues.

The log is `<data_dir>/eventlog.sqlite`. Retention is `eventlog.retention` (default 7 days); an hourly prune deletes older records, and the management plane reports the oldest record still available. Writers use the `otogi.Recorder` contract; exposing it to modules through `Invocation` is part of the module SDK milestone.

## Process logs

The process also writes structured logs (`log/slog`, JSON) to stdout. They are for developer diagnostics and for failures that happen before the event log is available, such as startup. The log level is configurable. Logs are not an API.

## Status

The management plane exposes current status: version, uptime, readiness, the state and last error of every component, kernel queue depth, durable inbox backlog per driver, recorded gaps, and event log health. Liveness and readiness checks are available for supervisors.

## Metrics

There are no metrics exporters for now; status and the event log cover operator needs. Prometheus or OpenTelemetry can be added at the management boundary later without changing contracts.
