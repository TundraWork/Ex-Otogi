# Management Plane

## Purpose

The management plane is how an operator checks, queries, and sets up a running instance. It is a JSON-over-HTTP API served by the bot process; the control panel is its client.

It is an adapter: it reads the event log and component status, and changes module settings, secrets, and allowlists, through their APIs, never their tables or internals.

## Access

`management.addr` defaults to `127.0.0.1:8090`; an empty value disables the API. On a non-loopback address, `management.token` is required and sent as `Authorization: Bearer <token>` on `/api/` routes; `/healthz` and `/readyz` stay open for supervisors.

With `management.public_url`, the control panel signs in through the OpenID Connect provider in `management.oidc` (see `docs/security.md`), and an `/api/` request is admitted with a session cookie, or with the token when it comes straight to the server rather than through the proxy. Changes are then recorded with the account's display name and username. The sign-in routes are open:

| Route | Purpose |
| --- | --- |
| `GET /api/auth/session` | Whether this browser is signed in, as whom, and the provider's issuer |
| `GET /api/auth/login?next=<path>` | Leave for the provider to sign in |
| `GET /api/auth/callback` | Where the provider sends the browser back: a session, then `next`; or the sign-in page saying why not |
| `POST /api/auth/logout` | Sign out; the response's `next` signs out at the provider too |

## Routes

The API is built with [huma](https://huma.rocks): `GET /api/openapi.json` (or `.yaml`) is its OpenAPI 3.1 document, from which clients generate their types.

| Route | Purpose |
| --- | --- |
| `GET /healthz` | Liveness |
| `GET /readyz` | 200 when every driver is ready, else 503 |
| `GET /api/status` | Version and start time; each driver's readiness, error, last received event, waiting and failed inbox rows, gaps, and full report; the kernel's state and load; the event log's health and retention; `running`, the deliveries modules are handling now, longest running first. Each module's runs since start are in `GET /api/modules`. |
| `GET /api/events` | Records, newest first. Filters: `kind`, `component`, `event`, `platform`, `account`, `conversation`, `outcome`, `q` (text in payload), `since`, `until` (RFC 3339). `before=<id>` pages older; `after=<id>` polls newer. `limit` is at most 500 (default 100). The response has `records`, `more`, and `oldest_retained`. |
| `GET /api/events/{id}/trace` | All records of one event, in order, and `running`: its deliveries modules are handling now; the event is finished when there are none |
| `GET /api/tasks` | Tasks, newest first: each event some module did something about (anything beyond its receipt and the plain finish of a delivery), with its records taken together: the message, the components that worked, when it started and ended, how many records, and its outcome (`running`, or the worst among its records). Filters: `platform`, `conversation`, `outcome`, `q` (text in any record's payload). `before=<cursor>` pages older; `limit` is at most 500 (default 50). A task running with no work recorded yet comes on the first page. |
| `POST /api/shutdown` | Graceful shutdown; the process exits with code 0 and stays stopped |
| `POST /api/restart` | The same graceful shutdown, after which the process exits with code 75 for its supervisor to start it again (see `docs/operations.md`); the settings that apply on restart take effect then. The `process.state` records of the stop name who asked, in `restart_requested_by`. Nothing in the API waits for the process to come back: poll `/readyz` |
| `GET /api/modules` | Every module compiled in: whether it is on and running, its settings revision, its allowlists with their chat counts (and `about`, the module a list grants another module's capability of, such as `todou` for the chat module's `tools:todou`), its commands, and its runs since start. A module whose saved settings do not build says why in `problem`. |
| `GET /api/modules/{module}` | One module, as above |
| `PATCH /api/modules/{module}` | Turn it on or off (`{"enabled": true}`): a settings revision like any other, applied at once. `{"ignore_bots": false}` turns its "ignore bots" switch off (it is on unless turned off), at once, recorded as `bots.changed`. Each module in `GET /api/modules` says `ignore_bots`. |
| `GET /api/modules/{module}/settings` | The current revision (number, on or off, settings, who saved it and when) and the settings' JSON Schema, whose `doc` texts describe each field. The `ETag` is the revision. |
| `PUT /api/modules/{module}/settings` | Save new settings and apply them. `If-Match` must carry the `ETag` they replace; a stale one is 412. Settings that break the schema are 422, field by field, and so are those the module refuses when it is built (a provider that is not set, two chat profiles of one name), at the fields it names; a refusal for no field of theirs, such as tools no running module provides, is 422 with the reason alone. The module is built from them and swapped in while it runs, with the modules using its services; nothing is stored if that fails. |
| `GET /api/modules/{module}/settings/revisions` | The settings' history, newest first: `before=<revision>`, `limit` |
| `POST /api/modules/{module}/settings/revisions/{revision}/restore` | Save an earlier revision's settings as a new revision (`If-Match` as for `PUT`); whether the module runs stays as it is |
| `GET /api/modules/{module}/allowlists/{list}` | The chats on an allowlist, with who added each and when, and what the chat directory knows of it |
| `PUT`, `DELETE /api/modules/{module}/allowlists/{list}/chats/{chat}` | Add a chat to an allowlist, or remove it; recorded as `allowlist.changed` |
| `GET /api/system` | The sections of settings outside modules, `general`, `llm`, `accounts`, and `process`, each with its current revision (0 when never saved), `applies` (`now`: a saved revision takes effect as it is saved; `restart`: when the process next starts), and `running`, the revision the process runs with. For a section that applies on restart, a restart is pending while `revision` is newer than `running` |
| `GET /api/system/startup` | The settings that stay in the config file, as the running process read them, read-only: the file's path, `data_dir`, and `management` (`addr`, `public_url`, `oidc.issuer`, `oidc.client_id`, `oidc.allow`), with the token and the client secret only as `token_set` and `client_secret_set` |
| `GET /api/system/{section}/settings` | A section's current revision and the settings' JSON Schema, as for a module; the revision's `module` is `system:<section>`. The `ETag` is the revision. |
| `PUT /api/system/{section}/settings` | Save new settings and apply them: at once for `general` and `llm`; for `accounts` and `process`, which the process reads when it starts, they are validated and stored, and apply at the next start. `If-Match`, 412, and 422 are as for a module's. For `llm`, every provider is built from them and the running modules built with a provider are rebuilt, with the modules using their services; if any step fails, the providers there were stay and nothing is stored. Recorded as `system.reconfigured`. |
| `GET /api/system/{section}/settings/revisions` | The section's history, newest first: `before=<revision>`, `limit` |
| `POST /api/system/{section}/settings/revisions/{revision}/restore` | Save an earlier revision's settings as a new revision and apply them (`If-Match` as for `PUT`) |
| `GET /api/system/llm/providers` | The language model providers the `llm` section sets now, by name, each with `kind` (`gemini`, or `openai` for an OpenAI-compatible service) and the models last fetched from it: `models` (each with its `id`, and when the provider's list gives them `display_name`, `input_tokens`, `output_tokens`, `context_tokens`, `input_modalities`, and `thinking_levels`), `fetched_at`, and `fetched_by`; `models` is empty and the others absent when never fetched. The lists live in the module store under `system:llm`, one key per provider: they survive restarts, and a provider removed from the section, or renamed, loses its list when the section is saved. |
| `POST /api/system/llm/providers/{name}/models` | Fetch a provider's models now, with its address and key: Gemini's `models` list, every page, keeping those that generate content, or an OpenAI-compatible `GET <base_url>/models`. Saves the list in place of the last one and answers the provider as listed above. 404 for a provider not set; 502 with the provider's HTTP status and message when its answer is a failure, and the last list stays. Recorded as `llm.models`. Lists are fetched only this way, never on a timer or at start. |
| `GET /api/secrets` | The secrets settings refer to as `${NAME}`, by name, never their values: each one set in the panel (`source` `panel`, with who set it and when), and every other name settings refer to, which an environment variable provides (`environment`) or nothing does (`missing`); `references` names whose current settings refer to it: module IDs, and `system:<section>` for a section, such as `system:llm` |
| `PUT /api/secrets/{name}` | Set or replace a secret (`{"value": "..."}`). The running modules that refer to it are rebuilt with the new value, and so are the language model providers that refer to it with the running modules that use a provider; the response names the modules in `restarted`; if one does not build, 422 with the reason and nothing is stored. It wins over an environment variable of the same name. A refused request does not get its values back. Recorded as `secret.changed`, without the value. |
| `DELETE /api/secrets/{name}` | Delete a secret set in the panel. While settings refer to it and no environment variable of the name exists, 409 naming the modules and sections; with such a variable, what refers to it is rebuilt to read it. Recorded as `secret.changed`. |
| `GET /api/chats` | Every chat the bot has seen, most recently seen first: kind, title, name, first and last seen, latest message, and the allowlists it is on |
| `GET /api/chats/{chat}` | One chat: its description and member count when the platform gives them, how many people were seen in it and the latest 100 of them with their names, and its new messages per day over the last 7 days |
| `PATCH /api/chats/{chat}` | Name it (`{"name": "..."}`); the name wins over the platform's title, and an empty one clears it |
| `GET /api/chats/{chat}/messages` | Its conversation, newest first (`before=<message id>`, `limit`): people's messages from the events received, and the bot's from what it sent, each naming its module and that module's records for the event it answered (`chat.reply` with the model, `module.completed` with the duration) |
| `GET /api/chats/{chat}/media/{message}/{part}` | An image or file in the conversation: what the bot sent comes from the event log; what people sent is fetched through the platform, so it is gone once the platform drops it |

A chat is named as `/whoami` prints it, `<platform>:<conversation>` (`telegram:channel:123`). Changes are recorded as made by the account that signed in, or by `management API` when the token was used.

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
- every record correlated with one event ID (received, each module outcome, each send) in order, and the deliveries still running for it;
- tasks: the events modules worked on, one row each, so a long task reads as one thing and shows while it runs.

**Lifecycle**
- request a graceful shutdown.

Configuration reload, backup triggers, and other commands are added when operators need them.

## API conventions

- Resources and HTTP verbs: `GET` reads, `PUT` and `PATCH` change, `DELETE` removes, `POST` runs a command.
- Writes that can collide carry the revision they change in `If-Match`.
- Cursors are record or revision IDs and stable: paging never skips or repeats records.
- Errors are RFC 9457 problem details: `title`, `status`, a human `detail`, and for invalid input `errors` naming each failing field.
- Times are RFC 3339; durations in records are milliseconds.
- Page size has a maximum, and larger requests are clamped.
- The API is unstable until the control panel exists. Breaking changes update in-tree clients in the same change.
