# Telegram MTProto Driver

The Telegram driver logs in as a **user account** through gotd (MTProto). There is no operator entrypoint yet: `NewComposition` in `internal/adapter/telegram` builds the full driver, and `cmd/otogi` will wire it in the runnable-bot milestone. Run commands from the repository root.

## Storage

Each account uses two files in its own directory:

```sh
install -d -m 0700 /srv/ex-otogi/telegram/<account>
# session: /srv/ex-otogi/telegram/<account>/session.json  (gotd login session)
# state:   /srv/ex-otogi/telegram/<account>/state.sqlite  (inbox, update state, send records)
```

The database is bound on first use to the configured account key, the authorized Telegram user ID, the gotd version, and the Telegram layer. Startup refuses a database bound to a different account.

Current code is stricter than `docs/security.md` requires. It insists on absolute, distinct, local paths, a `0700` parent owned by the service user, `0600` single-link files, and Linux. These checks are scheduled for simplification.

## Login

Startup reuses a valid session. Otherwise the driver calls an injected authenticator for the phone number, login code (app, SMS, call, flash-call, or missed-call), and 2FA password. Signup, QR login, and other flows are not supported. Until `cmd/otogi` provides an interactive login, a session must be created by code that supplies the authenticator.

## Backup and restore

Follow [`../operations.md`](../operations.md#backup-and-restore): copy the account directory with the driver stopped, or run `sqlite3 state.sqlite ".backup <dest>"` and copy `session.json` while it runs. To restore, stop, put both files back, and start. Telegram catches up on updates newer than the restored state.

## Update gaps

When Telegram cannot provide missed updates, the driver records a degraded recovery entry (box, position, cause) in `state.sqlite`.

Current code still differs from the design in [`../reliability.md`](../reliability.md). A degraded entry blocks readiness until it is acknowledged, and there is no operator command to acknowledge it. Moving to "record the gap and continue" is scheduled for the code cleanup.

## Retention

`Store.Compact` removes journal rows after 24 hours, strips payloads from finished inbox, quarantine, and locator rows after 7 days, and removes them after 30 days. Nothing schedules it automatically yet.

## Live smoke

Checks that an existing, already-authorized session can connect and load its account. It does not exercise SQLite, recovery, or the full driver.

```sh
export OTOGI_TELEGRAM_APP_ID='<app-id>'
export OTOGI_TELEGRAM_APP_HASH='<app-hash>'
export OTOGI_TELEGRAM_SESSION_PATH='/srv/ex-otogi/telegram/<account>/session.json'
make telegram-smoke
```

On success it prints `telegram smoke: session connectivity verified`. It is never part of `make quality`.

## gotd upgrade

gotd is pinned at `v0.161.0` with generated `tg.Layer == 228`; `internal/adapter/telegram/store/version_test.go` asserts both.

```sh
./scripts/quality/go.sh get github.com/gotd/td@<version>
./scripts/quality/go.sh mod tidy
# update the version and layer in internal/adapter/telegram/store/version_test.go
make quality
make telegram-smoke
```

Review mapper and codec changes for new or changed constructors.

Current code binds `state.sqlite` to the exact gotd version and layer. After an upgrade, startup refuses the existing database, so an upgrade currently needs a fresh state database: Telegram re-syncs from scratch, and the old inbox and send records are lost. Removing the version and layer from the binding is scheduled for the code cleanup.
