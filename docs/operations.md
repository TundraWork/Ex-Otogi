# Operations

## Deployment

One `otogi` process plus its data directory on one host, run under a supervisor (systemd, Docker, or similar) that restarts it on non-zero exit. The data directory is on local disk; SQLite WAL does not work over network filesystems.

## Configuration

- One YAML file (`otogi.yaml`; see [`otogi.example.yaml`](../otogi.example.yaml)) holds all settings, with one section per Telegram account, language model provider, and module.
- `llm.gemini` configures the Gemini provider: `api_key` (use `${OTOGI_GEMINI_API_KEY}`) and an optional `base_url`, the API endpoint with or without its version (default `https://generativelanguage.googleapis.com/v1beta`; a proxy or gateway that speaks the Gemini Interactions API works too). Modules name the provider and pin their own model. Live tests against the API: `OTOGI_GEMINI_API_KEY=... go test -run Live -v ./internal/adapter/gemini`.
- `${VAR}` in any value is read from the environment, which is how secrets such as the app hash stay out of the file. An unset variable is an error. Use block style for such values; in flow style (`{...}`) quote them.
- `-config PATH` selects the file (default `otogi.yaml`); the first argument selects the command: `run` (default), `validate`, `login ACCOUNT`, or `smoke`.
- The whole config is validated at startup before anything starts. Errors name the field or the line. Unknown fields are errors, so typos surface.
- `operators` lists the platform-qualified actor IDs (`telegram:user:123`, as `/whoami` prints them) allowed to use operator commands. The bot's own messages never are: the account is the bot's. Operators are the admins: besides `/status` and `/todou link`, they choose where chat agents answer with `/chat allow <agent> [chat]` and `/chat deny <agent> [chat]` (the chat defaults to the current one; another is named as `/whoami` prints it). These changes are stored, outlast restarts, and take precedence over a profile's `chats` in the config. `/chat list [chat]` shows anyone which agents answer.
- `otogi validate` checks a config file without starting drivers or touching data.
- Changing configuration means restarting. Live reload is added only if a concrete need appears.

## Data directory

```text
<data>/
  eventlog.sqlite                 # event log
  modules.sqlite                  # module key/value state and timers
  modules/<id>.sqlite             # a module's private database, if it uses one
  telegram/<account>/session.json # gotd session (login)
  telegram/<account>/state.sqlite # inbox, update state, send records
```

Each Telegram account has its own session and database. The database records which account it belongs to, and startup refuses a database that belongs to another account, which prevents mixing up accounts.

## SQLite

- WAL mode, `synchronous=NORMAL`, a busy timeout, and foreign keys on, all set when opened.
- Each owning component embeds its migrations and applies them forward-only at startup. A database newer than the binary is refused.
- Never copy a live database file; use the backup methods below.

## Backup and restore

**Backup**
- Bot stopped: copy the data directory.
- Bot running: for each database, run `sqlite3 <db> ".backup <dest>"` (or `VACUUM INTO`), and copy the session files.

**Restore**
1. Stop the bot.
2. Put the files back in the data directory.
3. Start the bot.

Telegram fetches any updates newer than the restored state. Encryption and off-host storage of backups are up to the operator.

## Upgrades

1. Back up.
2. Replace the binary and restart. Migrations run at startup.
3. To roll back, restore the backup taken before the upgrade if the new binary migrated a schema; otherwise just swap the binary back.

Upgrading gotd also changes the Telegram API layer. Follow the gotd upgrade steps in the [Telegram runbook](runbooks/telegram-mtproto.md#gotd-upgrade).

## Signals and exit codes

- SIGINT or SIGTERM starts graceful shutdown (see `runtime.md`); a second signal forces exit.
- Exit 0 means a clean stop. Non-zero means a startup failure, a component failure, or a forced stop, and the reason is printed on stdout.

## Runbooks

- [`runbooks/core-runtime-smoke.md`](runbooks/core-runtime-smoke.md): build, quality gates, offline smoke.
- [`runbooks/telegram-mtproto.md`](runbooks/telegram-mtproto.md): Telegram storage, login, backup, recovery, live smoke, gotd upgrade.
