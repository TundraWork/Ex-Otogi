# Experimental Telegram MTProto Operations

This runbook covers the opt-in Telegram MTProto milestone. It is not a production deployment, conformance, or release-verification procedure. Run commands from the repository root. Replace every `/srv/...` and `/secure/...` example with an approved absolute local path; do not paste credentials into shell arguments.

## Prepare private storage

Create one private directory for one account. The session and SQLite paths must be absolute, clean, distinct, local, and not symlinks.

```sh
install -d -m 0700 /srv/ex-otogi/telegram-account
export OTOGI_TELEGRAM_SESSION_PATH=/srv/ex-otogi/telegram-account/session.json
export OTOGI_TELEGRAM_DATABASE_PATH=/srv/ex-otogi/telegram-account/state.sqlite
export OTOGI_TELEGRAM_ACCOUNT_KEY=telegram-account
```

If files already exist, verify rather than silently changing ownership or accepting broader access:

```sh
stat -c '%F %a %U %n' "$OTOGI_TELEGRAM_SESSION_PATH" "$OTOGI_TELEGRAM_DATABASE_PATH"
stat -c '%F %a %U %n' "$(dirname "$OTOGI_TELEGRAM_SESSION_PATH")"
```

The directory must report `directory 700` and both files must report `regular file 600`; the database must be owned by the service user. The adapter creates a missing SQLite file as `0600`; the session parent must already be `0700`. Both paths must be on a filesystem supported as local by the adapter. Never place either resource on NFS, SMB, FUSE, or another shared/network filesystem. SQLite `-wal` and `-shm` files remain in this private directory.

The database is bound at creation to `OTOGI_TELEGRAM_ACCOUNT_KEY`, the authorized Telegram user ID, gotd version, and Telegram API layer. A mismatch is a startup failure; never edit the binding to force reuse.

## Authorization

The driver authorization boundary is an injected callback set for phone, login code, and Telegram 2FA password. An embedding operator must connect those callbacks to a protected interactive terminal or secret provider. Values are transient: never put the application hash, phone, code, password, or session bytes in command-line arguments, logs, tickets, or diagnostic bundles.

Existing valid session authorization is preferred. App, SMS, call, flash-call, and missed-call code challenges are supported. Signup, QR, passkey, payment-required/Fragment, email verification, OAuth, and unknown flows fail explicitly as unsupported. The live smoke does not prompt and requires a session already provisioned through an embedding that supplies callbacks securely.

## Paired backup and restore

Stop the Telegram adapter before these commands. The milestone has no online paired-snapshot coordinator, so an offline backup is required. The commands require `sqlite3`, `age`, and `tar`. Set a private staging directory and an `age` recipient controlled by the recovery operator:

```sh
export BACKUP_STAGE=/secure/staging/telegram-account-$(date -u +%Y%m%dT%H%M%SZ)
export BACKUP_RECIPIENT='age1...'
install -d -m 0700 "$BACKUP_STAGE"
install -m 0600 "$OTOGI_TELEGRAM_SESSION_PATH" "$BACKUP_STAGE/session.json"
sqlite3 "$OTOGI_TELEGRAM_DATABASE_PATH" ".backup '$BACKUP_STAGE/state.sqlite'"
chmod 0600 "$BACKUP_STAGE/state.sqlite"
sqlite3 "$BACKUP_STAGE/state.sqlite" 'PRAGMA integrity_check; SELECT account_key,authorized_user_id,gotd_version,tg_layer FROM binding WHERE singleton=1; SELECT version FROM schema_meta WHERE singleton=1;'
sqlite3 -separator '|' "$BACKUP_STAGE/state.sqlite" "SELECT account_key,authorized_user_id,gotd_version,tg_layer,(SELECT version FROM schema_meta WHERE singleton=1),strftime('%Y-%m-%dT%H:%M:%SZ','now') FROM binding WHERE singleton=1;" > "$BACKUP_STAGE/METADATA"
chmod 0600 "$BACKUP_STAGE/METADATA"
(cd "$BACKUP_STAGE" && sha256sum session.json state.sqlite METADATA > SHA256SUMS)
tar -C "$BACKUP_STAGE" -cf - session.json state.sqlite METADATA SHA256SUMS | age -r "$BACKUP_RECIPIENT" -o "$BACKUP_STAGE.tar.age"
sha256sum "$BACKUP_STAGE.tar.age"
```

The integrity result must be `ok`; record the displayed account binding, schema version, UTC creation time, and encrypted artifact checksum in the protected backup inventory. Store only `$BACKUP_STAGE.tar.age` outside the host's protected boundary. Publish an authenticated external checksum or signature for the encrypted artifact in the protected inventory; `SHA256SUMS` inside the archive is an integrity manifest, not provenance or authentication. Apply the approved encrypted-backup retention and access policy, then securely remove plaintext staging according to that policy.

Restore is offline into a new location and MUST use the deployment's vetted restore tool or procedure; do not pipe decrypted bytes to a general-purpose archive extractor and never overlay active state. The vetted implementation is required to:

- authenticate the encrypted artifact against its external checksum or signature before decryption;
- decrypt only into a new `0700` temporary directory on the destination local filesystem;
- parse the archive before extraction and enforce the exact unique flat regular-file allowlist `session.json`, `state.sqlite`, `METADATA`, and `SHA256SUMS`;
- reject symbolic links, hard links, devices, duplicate names, and path traversal, including absolute paths and any path component other than the allowlisted flat names;
- extract with no-follow filesystem operations, exclusive file creation, and `0600` modes, without invoking `tar -x` on untrusted input;
- verify `SHA256SUMS`, SQLite `PRAGMA integrity_check`, schema compatibility, and the account/user/gotd/layer binding against independently supplied expected values; and
- perform atomic publish only after every check succeeds, renaming the complete new directory into an absent final path on the same filesystem.

Invoke only the reviewed deployment tool according to its own documented interface, for example:

```sh
approved-telegram-restore --artifact /secure/backups/telegram-account.tar.age \
  --authenticated-digest-file /secure/inventory/telegram-account.tar.age.sha256 \
  --expected-account telegram-account --destination /srv/ex-otogi/telegram-account-restored
```

`approved-telegram-restore` is a required vetted deployment prerequisite, not a command supplied by this repository. If no tool or procedure implementing every requirement above is available, restoration is prohibited. After publication, independently inspect the final directory and regular files as `0700`/`0600`, then start only with both restored paths selected together. A restore drill succeeds only when the expected account authorizes and the full driver reconciles update state; a degraded result requires the process below.

## Degraded update gaps

A durable recovery record identifies the affected common/channel box, last position, state, bounded cause, and acknowledgement. A permanent gap means events may be missing. Readiness remains false until reconciliation succeeds or the exact permanent gap is explicitly acknowledged.

This experimental composition exposes no operator CLI or API for `RequiredRecoveries` or `AcknowledgeRecovery`. Full `Driver`/`NewComposition` startup checks SQLite `RecoveryReady` and refuses readiness while any degraded record remains unacknowledged. The separate `telegram-smoke` command checks preauthorized session connectivity only; it cannot validate SQLite, account binding, recovery state, or full-driver readiness. Do not update SQLite directly. Capture the bounded startup cause, keep the service unready, preserve the paired state, and either restore a known-good pair or use an audited embedding of those internal capabilities. Any acknowledgement record must identify account, box, position, cause, assessed missing-history effect, operator, and time. It permits readiness but does not erase the marker or claim that history was recovered.

## Retention and erasure

The implemented compaction contract removes normalized transport-journal rows after 24 hours; strips payloads from terminal/failed inbox rows, quarantine rows, and locators after 7 days; and removes those records after 30 days. Successful and permanently failed send intents are removed after 30 days; ambiguous intents are retained for operator resolution. A deployment MUST schedule the adapter-owned compaction capabilities and may shorten content-bearing retention when operational requirements allow, but must not retain payloads indefinitely by accident.

The milestone has no operator erasure command. Do not issue ad hoc SQL: adapter-owned account erasure must delete scoped SQLite state transactionally, and complete account erasure must also remove SQLite sidecars, the session, expired paired backups, and plaintext restore/staging material. Until audited operator tooling exists, take the adapter offline and follow the deployment's storage-level erasure procedure for the entire account pair. Record the result without participant content or credentials; retain independent audit evidence only under its governing policy.

## Opt-in live smoke

Use a disposable Telegram test account with an already-authorized session. Set secrets through the environment or a protected secret-injection mechanism, never arguments:
```sh
export OTOGI_TELEGRAM_APP_ID='<telegram-app-id>'
export OTOGI_TELEGRAM_APP_HASH='<telegram-app-hash>'
export OTOGI_TELEGRAM_SESSION_PATH='/srv/ex-otogi/telegram-smoke/session.json'
make telegram-smoke
```

The target rejects missing configuration before network access. It requires only the application ID, application hash, and existing session path. On success it prints `telegram smoke: session connectivity verified`. This means only that the session connected, was authorized, and loaded its Telegram account; it does not validate SQLite, account binding, or recovery readiness, and it never claims that the full driver is ready. It is deliberately excluded from `make quality`; default quality is offline and must never consume these values or contact Telegram. Unset the environment after the run:

```sh
unset OTOGI_TELEGRAM_APP_ID OTOGI_TELEGRAM_APP_HASH OTOGI_TELEGRAM_SESSION_PATH
```

A passing live smoke proves only session connectivity for that disposable account. It is not production-readiness or release-verification evidence.

## Gotd upgrade

Begin from a copied paired state and a disposable account. Record the old module version and generated layer, choose the reviewed version, then update dependency declarations:

```sh
./scripts/quality/go.sh list -m github.com/gotd/td
./scripts/quality/go.sh get github.com/gotd/td@<reviewed-version>
./scripts/quality/go.sh mod tidy
```

Update the asserted version and `tg.Layer` in `internal/adapter/telegram/store/version_test.go`. Review generated constructor/API changes; update bounded mapper/codec fixtures from values produced by the pinned public `tg` package, never by editing or copying gotd generated sources. Review authorization variants, update differences, send/refetch RPCs, schema and account binding, and restore compatibility.

Run the scoped and milestone gates, followed by the opt-in smoke configured above:

```sh
./scripts/quality/go.sh test ./internal/adapter/telegram/...
./scripts/quality/go.sh test -race ./internal/adapter/telegram/...
make quality
make telegram-smoke
```

Preserve dependency diff, old/new layer, fixture provenance, paired restore result, and smoke result. If compatibility fails, restore the pre-upgrade encrypted pair before running the prior binary; never force the old binary onto state whose binding or schema it rejects.
