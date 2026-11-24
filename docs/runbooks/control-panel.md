# Control Panel

The control panel is the bot's own web app: Status, Modules, Chats, and the Event log. The bot serves it with its management API on `management.addr`; a TLS reverse proxy puts it on the web. Sign-in goes through an OpenID Connect provider.

## Set it up

1. Build the panel into the binary: `make web` before `go build` (`make deploy` does both). A binary built without it answers the panel's pages with 503.
2. Set where it is reached, the origin the identity provider sends browsers back to:

   ```yaml
   management:
     addr: 127.0.0.1:8383
     public_url: https://otogi.example.com
   ```

3. Proxy the whole site to `management.addr`, keeping `Host` and setting `X-Forwarded-For`. With nginx:

   ```nginx
   location / {
       proxy_pass http://127.0.0.1:8383;
       proxy_http_version 1.1;
       proxy_set_header Host              $host;
       proxy_set_header X-Forwarded-For   $remote_addr;
       proxy_set_header X-Forwarded-Proto $scheme;
       proxy_read_timeout 180s;
       gzip on;
       gzip_proxied any;
       gzip_types text/css text/javascript application/javascript application/json image/svg+xml;
   }
   ```

   A request through the proxy needs a panel session; the management token works only for requests made straight to `management.addr`.

## Sign-in

The panel signs in through an OpenID Connect provider; it has no accounts or passwords of its own.

1. Register a confidential client at the provider, with the redirect URI `<public_url>/api/auth/callback`, the post-logout redirect URI `<public_url>/sign-in`, and the scopes `openid` and `profile`. With Prohibitorum (opus.name):

   ```sh
   podman exec prohibitorum prohibitorum oidc-client create --client-id ex-otogi --display-name Ex-Otogi \
     --redirect-uri https://otogi.example.com/api/auth/callback \
     --post-logout-redirect-uri https://otogi.example.com/sign-in --scope openid --scope profile
   ```

   It prints the client secret once: put it in `otogi.env` as `OTOGI_OIDC_CLIENT_SECRET`.

2. Configure it, and name who may sign in by their username at the provider:

   ```yaml
   management:
     public_url: https://otogi.example.com
     oidc:
       issuer: https://opus.name
       client_id: ex-otogi
       client_secret: ${OTOGI_OIDC_CLIENT_SECRET}
       allow: [tundra]
   ```

Removing a username from `allow` (and restarting) ends its sessions. To end every session at once, stop the bot, remove `<data_dir>/panel.key`, and start it. Refused sign-ins are in the event log as `panel.session`, saying why.

## Set up modules

Modules come with no settings. In Modules, pick one, fill in its Settings (each field says what it does), Save, review the changes, and Apply; then turn it on. Saving applies the settings to the running bot at once; if they do not build or the module does not start, nothing changes and the panel says why. History lists every revision with who saved it, and Restore saves an old one again.

BOOTH cards and X cards draw with Content safety's settings: turn Content safety on first (its defaults need nothing filled in). Changing it applies to every card module at once.

Where a module answers: Add chats on its page, picking from the chats the bot has seen, or the module switches on a chat's page in Chats.

A field that names a person, such as a chat person's User or the operators in General, searches the people the bot has seen in any chat, by name or ID (`GET /api/people?q=`): each is shown with their ID and where they were last seen, and choosing one fills in the ID, with their name beside it. An ID can be typed too, for someone who has not written yet: it saves, marked Not seen in any chat. A chat person without a name of their own is listed by the name the bot last saw them with.

A chat profile's name cannot be changed once saved: it names the profile's allowlist, and its threads are stored under it, so another name would be another profile, with an empty allowlist and no threads. The panel shows a saved profile's name, like a saved account's, read-only; a new one's is typed in. To replace a profile, add the other in one save and remove the old one in the next. Restoring a revision from before such a replacement is refused for the same reason; `otogi settings revert` (below) is not.

## Settings outside modules

The settings outside modules are in four sections, saved, reviewed, and restored like a module's (Settings in the panel; `/api/system/general`, `/api/system/llm`, `/api/system/accounts`, and `/api/system/process` from the server). Two apply while the bot runs:

- General: the operators, the command prefixes, how long the event log is kept, and the log level. Each applies the moment it is saved; no module restarts.
- Language models: the Gemini provider and the OpenAI-compatible services such as DeepSeek, each with its address and its API key as a secret. Saving rebuilds the providers and restarts the modules that use one, which the answer's record (`system.reconfigured`) names; their next request goes to the new address with the new key. If a provider or one of those modules does not build with the new settings (a module names a provider that is gone, a key is set nowhere), nothing changes and the answer says why.

Under the language model settings, Models lists each provider's models, fetched only when Fetch models is pressed: the bot asks the provider (Gemini's model list, or `<base_url>/models` for an OpenAI-compatible service) with the provider's own address and key, and keeps the list, with when and by whom it was fetched, until the next fetch. A fetch that fails says why (the provider's HTTP status and message, such as a wrong key or a proxy with no model list) and keeps the list from before. Each fetch is in the event log as `llm.models`, with the list or the failure. Removing a provider, or renaming it, deletes its list. From the server: `curl http://127.0.0.1:8090/api/system/llm/providers`, and `curl -X POST http://127.0.0.1:8090/api/system/llm/providers/deepseek/models` to fetch.

In a module's settings, a Provider field chooses among the providers set here, each with its kind and how many models were fetched; a saved name no longer set is still shown, flagged Not configured, and the module cannot be built with it. The Model field beside it offers the models last fetched from that provider, searchable by ID or display name, with their token limits and, when the list says so, that a model takes no images; any other ID can be typed, for a model newer than the list or a proxy without one. A saved model not in the list is flagged with the date of the list and still saves: an ID the provider does not take fails on the first call, as before. With no list fetched, the field says where to fetch one. Thinking marks the levels the chosen model's list does not give (DeepSeek's list gives them; Gemini's does not).

Two are read when the process starts, so saving one stores it and a restart applies it:

- Accounts: the Telegram accounts and the QQ bots, each credential as a secret.
- Process: the kernel's workers, `max_in_flight`, and `handler_timeout`, the log format, and the shutdown timeout.

`GET /api/system` tells a pending restart: for these two, `running` is the revision the process started with, and `revision` the saved one. `POST /api/restart` stops the bot as `POST /api/shutdown` does, with exit code 75, and the supervisor starts it again; wait for `/readyz`. If it does not come back, it has to be started on the server: look at the service's log for why.

To add a Telegram account, in this order: save the account, log it in on the server with `otogi login <account>`, then restart. Restarted before the login, the account's driver fails to start and the bot stays down (see below).

`GET /api/system/startup` shows what stays in `otogi.yaml` (the data directory, the management API's address, the panel's URL and its sign-in), with the file's path; those are changed in the file on the server.

From the server, with the `ETag` of the settings being replaced:

```sh
curl -i http://127.0.0.1:8090/api/system/llm/settings        # the settings, their schema, and the ETag
curl -X PUT http://127.0.0.1:8090/api/system/llm/settings -H 'If-Match: "1"' \
  -H 'Content-Type: application/json' \
  -d '{"gemini": {"api_key": "${OTOGI_GEMINI_API_KEY}"}, "openai": {"deepseek": {"base_url": "https://api.deepseek.com", "api_key": "${OTOGI_DEEPSEEK_API_KEY}"}}}'
```

All four sections used to be keys of `otogi.yaml`. After an upgrade the first start saves the file's keys as each section's first revision, by `otogi.yaml`; later starts ignore them and say so in one warning. Remove them from the file then (see `docs/operations.md`).

## The bot does not start after a settings change

Saving checks what can be checked without the platform. A wrong `app_hash` or `app_secret`, or a Telegram account not logged in, shows only when the driver starts: it fails, the process exits with an error, the supervisor starts it again, and it fails again. The panel is down with it. A module's settings that built when saved can fail the same way at a start, when a secret they refer to is gone from the environment.

The service's log names what failed, such as `system:accounts, settings revision 4` or the driver's error. With the bot stopped, put back the settings there were before the last change:

```sh
systemctl stop otogi                           # or however it is supervised
sudo -u <service user> otogi -config otogi.yaml settings revert system:accounts
systemctl reset-failed otogi                   # systemd: forget the failed starts
systemctl start otogi
```

Run the command as the user the service runs as, with the service's environment where the config file refers to it: run as root, it leaves files in the data directory that the service cannot write.

The ID is a section (`system:accounts`, `system:process`, `system:general`, `system:llm`) or a module's ID (`chat`). The command stores the revision before the current one again, as the latest, which is what restoring it in the panel does: the history is kept, the new revision is saved by `otogi settings revert` and recorded as `system.reconfigured` or `module.reconfigured`, and the command prints the revision it was at and the one it stored. Run again, it goes one revision further back each time. When there is no earlier revision (the change was the first ever saved), it stores empty settings, the state before anything was saved: no accounts for `system:accounts`, and for a module, off. The bot then starts, the panel opens, and the change can be made again there.

Run it only while the bot is stopped: the running process would keep what it started with, and the command does not check.

An account's name is where the bot keeps it: its login and state are under `<data_dir>/telegram/<account>/` or `qq/<account>/`, its driver's database is bound to the name, and its events, chats, and what modules keep for it carry the name. So the name of a saved account cannot be changed: a save in which a platform's accounts lose one name and gain another is refused. To replace an account, remove it in one save and add the other in the next; the new one starts with no login and none of the old one's data.

For a new Telegram account the cause is usually the order: `otogi login <account>` works while the bot is down, so logging in and starting again fixes it without reverting.

## Secrets

A setting that is a secret, such as a todou token, holds only a reference to it, `${OTOGI_TODOU_TOKEN}`. The secret itself is set in the panel, or stays in the environment (`otogi.env`) under that name; one set in the panel wins.

To set or replace one, give its name and value in the panel (Settings, then Secrets; or New secret… where a setting asks for one), or from the server (with `Authorization: Bearer <token>` when `management.token` is set):

```sh
curl -X PUT http://127.0.0.1:8090/api/secrets/OTOGI_TODOU_TOKEN \
  -H 'Content-Type: application/json' -d '{"value": "todou_pat_..."}'
```

The modules whose settings refer to it are restarted with the new value at once, and the answer names them; a language model provider that refers to it is rebuilt too, with the modules that use a provider. If one does not build with it, nothing changes and the answer says why. The value is not shown again anywhere: `GET /api/secrets` lists each secret's name, whether it comes from the panel or the environment, who set it and when, and what refers to it: modules by their ID, and the language model settings as `system:llm`. A name listed as `missing` is one some settings refer to that is set nowhere: that module does not start until it is set.

To move a secret out of `otogi.env`, set one of the same name in the panel, then delete the line from the file.

To remove one, delete it in the panel (`DELETE /api/secrets/<name>`). That is refused while settings still refer to it, unless the environment has a variable of that name to fall back to: change or remove those settings first.

Secrets set in the panel are stored as given in `<data_dir>/modules.sqlite`, so backups contain them (see `docs/operations.md`). Every change is in the event log as `secret.changed`, with who made it and without the value.

## Follow what the bot does

- Status, Working now: what modules are handling at this moment, and for how long.
- Event log, Tasks: one row for each event a module did something about, newest first; a running one has a pulsing dot and a ticking time. Open it for its message and its trace: every record of it in order, with the step still running at the end. While it runs, the trace follows it, and says how long ago the last record came: a model round in a chat reply often takes 10 to 45 seconds.
- Event log, Records: every record as written, for what is not a task (driver states, process events, watcher problems). A record's Event opens its task.

## From a script

On the server, the same API answers without the panel, at `management.addr` (with `Authorization: Bearer <token>` when `management.token` is set):

```sh
curl -s 127.0.0.1:8383/api/modules
curl -s -X PUT -H 'Content-Type: application/json' -H 'If-Match: "3"' \
  -d @settings.json 127.0.0.1:8383/api/modules/chat/settings
curl -s -X PUT 127.0.0.1:8383/api/modules/chat/allowlists/Luna/chats/telegram:channel:123
```

`docs/management-plane.md` lists every route; `otogi openapi` prints the OpenAPI document.

## Back up

`panel.key` seals session cookies (losing it only signs everyone out); `modules.sqlite` holds module settings, their revisions, and allowlists; `eventlog.sqlite` holds the chat directory. Back them up like every database here: with SQLite's backup, never by copying the live file (see `docs/operations.md`).
