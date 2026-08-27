# Task 3 Report

Status: complete.

## Delivered

- Pinned `github.com/gotd/td` v0.161.0 and `modernc.org/sqlite` v1.57.0; asserted `tg.Layer == 228` and build module versions.
- Added the Telegram SQLite store with current-only schema setup, WAL, foreign keys, busy timeout, bounded connections, local-filesystem validation, and account/user/version/layer binding.
- Implemented all gotd `updates.StateStorage`, `ChannelAccessHasher`, and `UserAccessHasher` methods with account-scoped data and missing-state errors.
- Added peer records, raw transport journal/startup IDs, atomic normalized capture, inbox/quarantine/locators, stable source deduplication, ordered claims and lease recovery, retry scheduling, consistency/degraded markers, retention/compaction, and account erasure.
- Added persistent send intents with digest conflict detection, stable random IDs, strict transitions, restart recovery, correlation, and terminal retention.
- Added validated top-level gotd `session.FileStorage` construction requiring an absolute clean path, private modes, regular files, and local filesystems.
- No Telegram networking, mapper, manager, driver lifecycle, or send RPC was added.

## TDD Evidence

Tests were introduced before their implementations. Initial focused runs failed for the missing package/API and later exposed claim ordering, stale send-transition, local-filesystem validation, absent journal capture, concurrent send creation, and quarantine compaction behavior. The implementation and regression fixes made those focused tests pass.

## Verification

- `./scripts/quality/go.sh test -race ./internal/adapter/telegram/... -count=1` — passed.
- `make lint` — passed with `0 issues` and doccheck clean.
- `make quality` — passed: format check, lint/doccheck, architecture check, all unit tests, all race tests, leak tests, and scripted smoke.

## Review

A dedicated review identified four Important findings: missing SQLite filesystem locality validation, acceptance of absent journal rows during atomic capture, a concurrent send-intent creation race, and delayed quarantine payload compaction. Each finding received a focused regression test and implementation fix before the final quality run.

## Concerns

- Migrations intentionally support only the current experimental schema and reject unknown versions.
- Filesystem locality now fails closed: Linux uses a positive local-filesystem allowlist, and platforms without a positive verifier reject opening the store.

## Blocking Review Fix Round

- Hardened database creation and reopen validation: new files are exclusively created as `0600`; existing files must be regular, current-user-owned, exactly `0600`, and on a positively allowlisted local filesystem. Unknown, FUSE, network, and clustered filesystem types fail closed.
- Schema version 2 is a clean current-only cutover. It separates channel access hashes from channel cursors, expands locator identity with conversation plus timestamps/hash retention, and rejects both prior versions and incompatible current layouts.
- Reopen binding now compares account key, authorized user ID, gotd version, and Telegram layer. Capture requires a positive account-owned journal row before any writes or consistency advance. Channel hash reads preserve database errors.
- Locator payloads compact after 7 days while hashes remain through 30-day deletion; account erasure includes every new table. Send retry timestamps obey status invariants, and claims use one atomic `UPDATE ... RETURNING` statement rather than a deferred transaction upgrade.
- Explicit pre-SQL bounds cover journal, inbox, quarantine, peer, locator, send fields and capture batch count: raw/extension fields `64 KiB`, normalized events `1 MiB`, locators `16 KiB`.

### Fix-Round TDD and Verification

- Focused regressions were first observed failing at compile/runtime for the missing locator identity/API and size bounds, then passed after the production changes.
- A follow-up regression constructed schema version 2 with the expected locator columns but a primary key omitting `conversation`; it failed red by reopening successfully, then passed after schema validation began checking ordered primary-key positions for every required table.
- `./scripts/quality/go.sh test ./internal/adapter/telegram/store -count=1` — passed.
- `./scripts/quality/go.sh test -race ./internal/adapter/telegram/store -count=1` — passed.
- `./scripts/quality/golangci-lint.sh run ./internal/adapter/telegram/store` — passed with `0 issues`.
- `./scripts/quality/go.sh run ./scripts/quality/cmd/doccheck internal/adapter/telegram/store` — passed.
