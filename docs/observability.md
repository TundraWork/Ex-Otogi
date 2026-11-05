# Observability

## Principle

An operator must be able to answer "what did the bot receive, what did each module do, what did it send, and what went wrong?" without reading source code or attaching a debugger. Everything needed for that is recorded in full and is queryable. Nothing is redacted, sampled away, or silently dropped.

## Structured event log

The event log is the primary observability tool. It is an append-only SQLite table owned by one event log component and queried through the management plane.

Each record has:

- a monotonically increasing ID and a timestamp;
- a kind: `event.received`, `event.dropped`, `module.completed`, `module.failed`, `module.panicked`, `job.skipped`, `timer.failed`, `effect.applied`, `effect.failed`, `driver.state`, `driver.gap`, `inbox.failed`, `llm.completed`, `llm.failed`, `process.state`, or a module's own kind (`chat.reply`, `chat.tool`, `chat.media_skipped`, `chat.access`, `todou.write`, `todou.call_failed`, `todou.watch_warning`, `todou.watch_failed`, `todou.delivery_failed`);
- the component (`kernel`, `module:<id>`, `middleware:<id>`, `driver:telegram/<account>`, `llm:<provider>`, `process`);
- the correlated event ID when there is one, plus platform, account, and conversation;
- an outcome and a duration where applicable;
- the full payload as JSON: message content, error chain, panic stack, and so on.

What gets recorded:

- every inbound event with its full rich content;
- every module invocation outcome with duration, including errors and panics with stacks; a run that completed but took more than half its timeout has outcome `slow`. A record a module writes while handling an event joins that event's trace;
- every job run skipped because the previous one is still running, and every failure to load or update a timer;
- every outbound action with its content and result; a streamed message is one record when it closes, with its final content, the platform messages holding it, and how many updates it received and showed (each update is a prefix of the final content, so it is not recorded again);
- every language model call: the calling module and event, the model and settings, the system instruction and every turn sent, the reply's steps (thoughts included) and Markdown, how it ended, token usage, time to first output, and every HTTP request with its status and the full body of each error. To tell a slow provider from a slow network, the record also has:
  - each HTTP request's wall-clock start;
  - DNS, connect, TLS and request-sent times;
  - whether a pooled connection was reused, and how long it had idled;
  - protocol and server address;
  - time to response headers, and to the end of the body, with its size;
  - every response header (request headers carry the API key and are left out);
  - the stream's timeline: each event's arrival, type, step and size;
  - the provider's interaction IDs;
  - the longest silence and what preceded it.

  The same summary is one `gemini: call` line in the process log. Media data and the model's thought signatures appear as their size and SHA-256: a conversation's images and signatures are resent on every call, and the bytes stay in the platform message and the module's history;
- every failed call to an external system a module depends on, with what was sent and what came back: a todou CLI call's arguments, input, exit code, and full stderr; problems the todou watcher reports and retries on its own; media the chat module could not show the model;
- driver connection state changes, catch-up runs, and gaps;
- process start, ready, drain, stop, and component failures.

Records are written synchronously by the component that produces them. Bot traffic is low enough that a SQLite append per record is cheap, and this avoids a background queue that could drop records. If a write fails, the failure is printed to stdout and shown in status; event processing continues.

The log is `<data_dir>/eventlog.sqlite`. Retention is `eventlog.retention` (default 7 days); an hourly prune deletes older records, and the management plane reports the oldest record still available. Writers use the `otogi.Recorder` contract; exposing it to modules through `Invocation` is part of the module SDK milestone.

## Process logs

The process also writes structured logs (`log/slog`, JSON) to stdout. They are for developer diagnostics and for failures that happen before the event log is available, such as startup. The log level is configurable. Logs are not an API.

Levels:

- **ERROR**: something failed and its work was lost or must be redone: a module error or panic, a failed model call, a driver failure, an event log write that failed.
- **WARN**: something abnormal that was handled: a retried or slow model call (over 60 s, or silent for 30 s), a slow module run, a skipped job, a failed todou call or watcher retry, a notification sent without its title, media left out.
- **INFO**: lifecycle and state changes: start, ready, listening, shutdown, a driver switching how it streams.
- **DEBUG**: routine work and expected misses: each completed module run, each model call that went normally, the todou watcher's heartbeats, deletions of messages the bot never saw, typing actions that failed.

The default level is INFO, so a healthy bot's log holds its lifecycle and nothing else. What is logged at WARN or ERROR about the bot's work is also in the event log, with its full detail. Only housekeeping failures (pruning, caching peers and sent-message locators) and failures before or of the event log itself are in the process log alone.

## Status

The management plane exposes current status: version, uptime, readiness, the state and last error of every component, kernel queue depth, each module's runs since start (completed, slow, failed, panicked), durable inbox backlog per driver, recorded gaps, and event log health. Liveness and readiness checks are available for supervisors.

## Metrics

There are no metrics exporters for now; status and the event log cover operator needs. Prometheus or OpenTelemetry can be added at the management boundary later without changing contracts.
