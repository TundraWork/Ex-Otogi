# Observability

## Principle

An operator must be able to answer "what did the bot receive, what did each module do, what did it send, and what went wrong?" without reading source code or attaching a debugger. Everything needed for that is recorded in full and is queryable. Nothing is redacted, sampled away, or silently dropped.

## Structured event log

The event log is the primary observability tool. It is an append-only SQLite table owned by one event log component and queried through the management plane.

Each record has:

- a monotonically increasing ID and a timestamp;
- a kind: `event.received`, `event.dropped`, `module.completed`, `module.failed`, `module.panicked`, `job.skipped`, `timer.failed`, `effect.applied`, `effect.failed`, `driver.state`, `driver.gap`, `inbox.failed`, `llm.completed`, `llm.failed`, `process.state` (the process running, stopping, and stopped, with the error that stopped it; a stop asked for with `POST /api/restart` carries `restart_requested_by`, who asked), `module.reconfigured` (a saved revision of a module's settings, applied: its number, whether the module is on, the settings, who saved it, and the modules restarted with it), `system.reconfigured` (a saved revision of a section of the settings outside modules, applied: its number, the settings, who saved it, `otogi.yaml` for one imported from the config file and `otogi settings revert` for one that command stored, and the modules restarted with it; for `system:accounts` and `system:process` the revision is stored and applies when the process next starts; component `system:<section>`), `driver.lookup` (a QQ group's details and member names looked up: ok, error, or skipped once when QQ refuses a bot that is not whitelisted), `panel.session` (a control panel sign-in, refused sign-in with why, or sign-out, with the account's username and subject), `allowlist.changed` (a chat added to or removed from a module's allowlist, with the list, the chat, and who changed it; component `module:<id>`), `bots.changed` (a module's "ignore bots" switch turned on or off, with who changed it; component `module:<id>`), `secret.changed` (a secret set, replaced, or deleted in the control panel: its name, the action, who made it, and the modules restarted with it, never the value; component `secret:<name>`), or a module's own kind (`chat.reply` (`trigger` says what made the agent answer: `keyword` (also an agent with `judge.wake` whose judgment failed, for a message starting with its keyword), `reply`, `mention`, or `decision`, with `judged`: the judgment's score, the threshold it passed (the agent's multiplied by the sender's `threshold_factor`), and `factor` when that is not 1), `chat.tool`, `chat.unjudged` (a judged agent not asked about a group message because the sender's `threshold_factor` takes its threshold past 1: the agent, its threshold multiplied by the factor, and the factor), `chat.media_skipped` (media an agent did not get: too large, could not be loaded, or, under `media.as_text`, not described, with the media service's error, or `no description kept` for a quoted message's media, which is not waited for), `media.described` (media described, with its duration: the digest it is kept under, its kind, description, and text, the step, provider, and model that described it, token usage, its size, and an animation's frame count), `media.failed` (a step that failed: its error, or how the model's turn ended, or its answer when out of format, or the media it did not get; the next step is tried), `media.skipped` (media not described: too large, a GIF too large to decode included, or not an image, animation, or video), `media.sticker` (a `/sticker` action on the command's event: add, note, or remove, with the sticker's number, SHA-256, kind, what it shows, who did it, and its size; skipped, with the reason: `too large`, `full`, `too much in all`, `gone` with the fetch error, `not png, jpeg, or gif` with the content's type, or `already kept`), `decision.completed` and `decision.failed` (a request to the decision service, joined to the asking module's event, with its duration: the asking module, the model that answered, the state and questions, the answers and token usage, TypeSafe's request ID and reply as it came, and on failure the HTTP status and error), `twitter.translation` (a post translated for its card: ok, or error with the model's reply, when the card goes out without it), `twitter.video` (a post's video or GIF sent after its card: ok with its size, skipped for a censored post, or one over `max_video_mb`, or error with the step that failed), `bilibili.card`, `booth.card`, and `twitter.card` (outcome ok, degraded when the card fell back to text, went out with its pictures covered after the platform refused it (`refused` holds why), or went out untranslated because its translation failed (`translation_error`), skipped for a missing or private item, or error; twitter's also names its source), `todou.write`, `todou.call_failed`, `todou.watch_warning`, `todou.watch_failed`, `todou.delivery_failed`);
- the component (`kernel`, `module:<id>`, `system:<section>`, `middleware:<id>`, `driver:telegram/<account>`, `llm:<provider>`, `process`);
- the correlated event ID when there is one, plus platform, account, and conversation: the event's, except that an outbound action names the conversation it changes;
- an outcome and a duration where applicable;
- the full payload as JSON: message content, error chain, panic stack, and so on.

What gets recorded:

- every inbound event with its full rich content;
- every module invocation outcome with duration, including errors and panics with stacks; a run that completed but took more than half its timeout has outcome `slow`. A record a module writes while handling an event joins that event's trace. The deliveries running now are not records: the management plane reports them from the kernel, so a long run shows while it lasts;
- every job run skipped because the previous one is still running, every failure to load or update a timer, and every timer given up after its last attempt failed (`timer.failed` with step `gave up`: the timer's key, when it was due, the attempts, the last error, and the payload it held, so a reminder that was never delivered can be read here);
- every outbound action with its content and result; media sent inline (a card's image, a video) appears as its size and SHA-256, as in language model calls, since the platform message keeps the bytes and a video would put tens of megabytes in the log; a streamed message is one record when it closes, with its final content, the platform messages holding it, and how many updates it received and showed (each update is a prefix of the final content, so it is not recorded again);
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

The log is `<data_dir>/eventlog.sqlite`. Retention is the general settings' `eventlog_retention` (default 30 days); an hourly prune, which reads it each time, deletes older records, and the management plane reports the oldest record still available.

Next to the records, the event log keeps a directory of every chat the bot has seen, updated in the same transaction as each `event.received`: platform, conversation, the account that saw it last, its kind, its title, description, and member count as the platform gives them, a name the operator may set, when it was first and last seen, its latest new message, its new messages per day (UTC), and the people seen sending messages in it, with their latest names. Chats and people outlive the retention; the daily counts and messages do not. Writers use the `otogi.Recorder` contract; exposing it to modules through `Invocation` is part of the module SDK milestone.

## Process logs

The process also writes structured logs (`log/slog`, JSON) to stdout. They are for developer diagnostics and for failures that happen before the event log is available, such as startup. The log level is the general settings' `log_level`, and changes when it is saved. Logs are not an API.

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
