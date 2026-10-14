# External References

References the design relies on. A newer external version does not change the design by itself.

## Go

- [Go release policy](https://go.dev/doc/devel/release): use a release that still gets security fixes.
- [Race detector](https://go.dev/doc/articles/race_detector), [fuzzing](https://go.dev/doc/security/fuzz/), [`govulncheck`](https://go.dev/doc/security/vuln/).

## SQLite

- [Write-Ahead Logging](https://www.sqlite.org/wal.html): local disk only.
- [Online Backup API](https://www.sqlite.org/backup.html) and [`VACUUM INTO`](https://www.sqlite.org/lang_vacuum.html#vacuuminto): how to back up a live database.

## Telegram

- [Telegram API](https://core.telegram.org/api) and [working with updates](https://core.telegram.org/api/updates): update sequences, gaps, and `getDifference`.
- [gotd/td](https://github.com/gotd/td): the MTProto client, its update manager, peer cache, and login flow. Its version is pinned in `go.mod`; see the Telegram runbook for upgrades.

## HTTP

- [RFC 9110](https://www.rfc-editor.org/rfc/rfc9110): method semantics for the management API (safe `GET`, `POST` for commands).

## Rich event model (informative)

The protocol-neutral content model borrows ideas from [CloudEvents](https://cloudevents.io/) (context/data separation), [Matrix](https://spec.matrix.org/) (typed events and relations), XMPP extension elements, MIME multipart, [ActivityStreams](https://www.w3.org/TR/activitystreams-core/), Discord components, and QQ/NTQQ element chains.
