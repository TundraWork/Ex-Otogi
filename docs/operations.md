# Operations

## Deployment

One `otogi` process plus its data directory on one host, run under a supervisor (systemd, Docker, or similar) that restarts it on non-zero exit. The data directory is on local disk; SQLite WAL does not work over network filesystems.

## Configuration

- One config file holds all settings, with one section per driver and module. The format is chosen with the runnable-bot milestone.
- Environment variables may supply secrets and simple deploy-specific overrides. Command-line flags select the config file and the mode.
- The whole config is validated at startup before anything starts. Errors name the field path. Unknown fields are errors, so typos surface.
- `otogi validate` checks a config file without starting drivers or touching data.
- Changing configuration means restarting. Live reload is added only if a concrete need appears.

## Data directory

```text
<data>/
  eventlog.sqlite                 # event log
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
