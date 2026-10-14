# Telegram MTProto Driver

The Telegram driver logs in as a **user account** through gotd (MTProto). `otogi run` starts one driver per account listed under `telegram:` in `otogi.yaml`. Run commands from the repository root.

## Storage

Each account uses two files, normally in one directory:

```text
/srv/ex-otogi/telegram/<account>/session.json  # gotd login session
/srv/ex-otogi/telegram/<account>/state.sqlite  # inbox, update state, peers, locators, sends, gaps
```

The driver creates missing directories with mode `0700`. Keep the directory readable only by the service user: the session is a logged-in account, and the database holds full message content.

On first use the database is bound to the configured account key and the logged-in Telegram user. Startup refuses a database bound to a different account or user. gotd upgrades do not affect the binding.

The schema is managed by goose migrations embedded in the binary and applied at startup. Databases created before the 2026-10-14 rewrite are not migrated: delete them and let Telegram re-sync.

## Login

Log in once per account, interactively:

```sh
otogi login main
```

It prompts for the phone number, the login code Telegram sends, and the 2FA password if one is set (not echoed), then saves the session and exits. It touches no database. `otogi run` never prompts: if a session is not logged in, startup fails with a message to run `otogi login`. Signing up new accounts is not supported; create the account with an official client first.

## How updates flow

1. gotd's update manager receives updates and calls the driver's inbox handler.
2. The handler writes new, edited, and deleted message updates to the `inbox` table. Writes are deduplicated by (box, pts), so gotd re-deliveries are harmless.
3. gotd saves its position only after the handler returns.
4. A dispatcher maps inbox rows to events in order and submits them to the kernel. A row is marked done when all its events finish.
5. Other update kinds (typing, read receipts, and so on) are not stored yet.

If an inbox write fails, the driver stops gotd from saving positions and fails. After the process restarts, gotd resumes from its last saved position, so nothing is skipped.

## Backup and restore

Follow [`../operations.md`](../operations.md#backup-and-restore).

- **Backup:** copy the account directory with the driver stopped, or, while it runs, run `sqlite3 state.sqlite ".backup <dest>"` and copy `session.json`.
- **Restore:** stop the bot, put both files back, and start it. Telegram catches up on updates newer than the restored state. Inbox rows that were pending in the backup are processed again; sends are not repeated.

## Gaps

When Telegram cannot provide missed updates, gotd reports it and the driver records a row in the `gaps` table and logs a warning. The bot keeps running. Gap kinds:

| Kind | Meaning |
| --- | --- |
| `too_long` | The account was offline too long; Telegram returned no full history. |
| `channel_too_long` | The same, for one channel. |
| `channel_inaccessible` | The account lost access to a channel. |
| `no_saved_state` | No saved update state, which is normal on first start. |
| `no_saved_channel_state` | A channel's saved state was incomplete. |

Inspect them with:

```sh
sqlite3 state.sqlite "SELECT kind, channel_id, datetime(detected_at/1000, 'unixepoch') FROM gaps"
```

## Failed rows

An inbox row that cannot be decoded or mapped is marked `failed` and keeps its error. This is a mapper bug: the row will not succeed on retry. Inspect failed rows with:

```sh
sqlite3 state.sqlite "SELECT id, error FROM inbox WHERE state = 'failed'"
```

## Retention

The driver prunes hourly:
- finished inbox rows after 7 days;
- message locators, which route deletions, after 30 days;
- sent-message records after 7 days.

Pending rows are never pruned.

## Live smoke

Checks that an existing, already-authorized session can connect and load its account. It does not exercise the database or the full driver.

```sh
export OTOGI_TELEGRAM_APP_ID='<app-id>'
export OTOGI_TELEGRAM_APP_HASH='<app-hash>'
export OTOGI_TELEGRAM_SESSION_PATH='/srv/ex-otogi/telegram/<account>/session.json'
make telegram-smoke
```

On success it prints `telegram smoke: session connectivity verified`. It is never part of `make quality`.

## gotd upgrade

```sh
go get github.com/gotd/td@<version>
go mod tidy
make quality
make telegram-smoke
```

`TestGotdSavesPositionOnlyAfterHandlerReturns` checks the gotd behavior the inbox relies on, and it fails if an upgrade changes it. Also review mapper changes for new or changed constructors.
