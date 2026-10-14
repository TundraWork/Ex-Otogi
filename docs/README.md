# Ex-Otogi `next` Documentation

These documents describe the design of the `next` runtime: a self-hosted, single-node, event-driven IM bot framework written as a Go modular monolith. `AGENTS.md` lists the priorities that every document follows: operability, maintainability, design over implementation, and flexibility.

## Status

`next` is a work in progress, not a release. It runs as a single `otogi` binary configured by `otogi.yaml`, with the core kernel, an echo module, and a Telegram MTProto user-account driver ([runbook](runbooks/core-runtime-smoke.md#run-the-bot)). The 2026-10-14 cleanup ([contracts](superpowers/specs/2026-10-14-lean-core-contracts-design.md), [Telegram](superpowers/specs/2026-10-14-telegram-on-gotd-design.md)) brought the code in line with these documents.

## Documents

| Document | Covers |
| --- | --- |
| [`product.md`](product.md) | Goals, actors, scope, non-goals |
| [`architecture.md`](architecture.md) | Layers, dependency direction, composition, persistence ownership |
| [`runtime.md`](runtime.md) | Event model, delivery, queues, lifecycle, shutdown |
| [`extensions.md`](extensions.md) | Module and driver SDK |
| [`reliability.md`](reliability.md) | Failure handling, retries, recovery |
| [`security.md`](security.md) | Credentials, management access, input limits |
| [`observability.md`](observability.md) | Structured event log and diagnostics |
| [`management-plane.md`](management-plane.md) | Operator API capabilities |
| [`operations.md`](operations.md) | Configuration, SQLite, backup, upgrade, shutdown |
| [`quality.md`](quality.md) | Tests and quality gates |
| [`standards.md`](standards.md) | External references |
| [`runbooks/core-runtime-smoke.md`](runbooks/core-runtime-smoke.md) | Build, gates, and offline smoke |
| [`runbooks/telegram-mtproto.md`](runbooks/telegram-mtproto.md) | Telegram storage, login, backup, recovery, upgrade |

`docs/superpowers/` holds the design specs and plans of past milestones. They are historical records: where they disagree with these documents, these documents win.

## Changing the design

A design change updates every affected document in the same change and removes rules it makes obsolete. When two documents conflict, fix the documents rather than picking the convenient reading. Keep documents short: state the decision and its reason, not boilerplate.

Not yet designed: long-term memory, control-panel UX, and multi-node operation (out of scope unless a single node demonstrably cannot do the job).
