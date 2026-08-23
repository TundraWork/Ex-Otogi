# Milestone 7 Results: Rollout Verification

Status: verified

## Compatibility decision

Management telemetry is explicitly disposable and version-local. Based on that
constraint, the rollout removed historical compatibility machinery instead of
testing it:

- the current schema is one fresh Goose migration;
- event and snapshot payloads are JSON selected by event `kind` or snapshot
  namespace;
- the redundant `payload_type` database/API field is removed;
- the manual Go DTO decoder registry is removed;
- incompatible releases use a fresh management database.

Memory persistence and user/conversation data retain their independent backup
and migration requirements.

## Verification completed

- full Go suite and race suite;
- fresh management schema creation;
- generated OpenAPI client drift check;
- frontend dependency install, Vue type check, lint, and production build;
- deterministic chat reliability scenarios;
- deterministic memory evaluation from earlier milestones.

The production frontend build reports a size warning for the existing formatting
dependency chunk. Events remains a dynamically split approximately 17 kB chunk;
the warning does not affect the latest-first request path.

## Final results

- `make quality`: passed.
- architecture and changed-code policy checks: passed.
- full race suite and goroutine leak suite: passed.
- report-only coverage: 70.7%.
- OpenAPI regeneration: exact; generated TypeScript client is current.
- frontend frozen install, Vue type check, oxlint, and production build: passed.
- deterministic memory evaluation: accepted with formation F1 and nDCG@5
  both 1.0 and zero regression.
- 100,000-event SQLite p95: latest 0.53 ms, newer 0.50 ms, older
  0.43 ms, filtered latest 0.48 ms.
- fresh management database creation: passed. Unmarked legacy databases fail
  startup with an explicit reset path.

The non-blocking security report identifies 23 reachable advisories from the
Go 1.26.1 standard library and existing grpc/x-text/x-net versions. The primary
toolchain remediation is Go 1.26.6; dependency upgrades are separate follow-up
work and did not fail the repository's configured quality gate.
