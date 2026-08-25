# Management Plane Requirements

## Purpose and boundary

The management plane gives authorized operators bounded visibility and control over one Ex-Otogi instance. This document specifies capabilities and protocol semantics, not endpoint paths, generated schemas, or control-panel UX.

Management handling is an adapter over kernel/operator contracts. It MUST NOT access module internals, raw database tables, platform SDK clients, or global mutable state.

## Access model

Management is disabled or bound to a protected local interface by default. Any non-local deployment requires authenticated encryption, authenticated principals, and explicit authorization policy.

Every operation declares an action and resource scope. Read status, inspect diagnostics, change safe runtime settings, drain, create backup, restore, and perform destructive maintenance are separate permissions. Destructive operations require explicit intent and are not exposed as safe HTTP methods.

Authentication failures reveal no credential detail. Authorization denials and privileged actions create audit records with principal, action, target class, outcome, and correlation, excluding secret or participant content.

## Required capabilities

An implementation MUST provide authenticated operations to:

- read release, process, configuration-version, and schema metadata;
- read lifecycle, liveness, readiness, drain, and degraded state;
- inspect registered extensions, declared capabilities, and lifecycle state;
- inspect bounded queue, concurrency, dependency, database, and telemetry status;
- query events and traces by bounded filters and opaque cursors;
- inspect one event's metadata and handling outcomes subject to data policy;
- initiate graceful drain and shutdown;
- validate a candidate configuration;
- apply only supported atomic reload changes;
- initiate and inspect backup operations;
- expose migration and restore status without permitting unsafe online restore.

Raw secrets are never readable. Raw message content is omitted unless a future data-access specification defines purpose, authorization, retention, and audit.

## Diagnostic history

Event and trace queries read a kernel-owned diagnostic history, not an exporter backend. The history stores bounded event metadata, lifecycle transitions, module outcomes, failure classes, correlation, and trace/span summaries; it excludes raw message content and secrets. Its schema and SQLite transaction boundary are owned by the kernel diagnostics capability.

Accepted event metadata and terminal handling outcomes are durable. Intermediate span detail is best-effort and may be absent after crash. The default diagnostic retention is 7 days with a configured byte ceiling inside the published workload envelope; age or size eviction removes oldest complete traces/events and records the new oldest cursor. Durable diagnostic history has a default 24-hour RPO, is included in every full backup, and is validated during restore. Its retention remains independent of audit retention because diagnostic history is operational evidence, not the audit authority.

Queries report the available window and whether detail is partial. A cursor older than the retained window returns an explicit expired-cursor problem containing the current oldest cursor; it never implies that missing history did not occur.

## Collections and pagination

Collections use stable sort order and opaque cursors tied to the filter and snapshot/window semantics. The service sets a maximum page size and clamps or rejects larger requests consistently. Offset pagination is not used for unbounded changing event streams.

A response states whether more results exist and provides the next cursor when applicable. Expired or invalid cursors return a stable client error; the server does not silently restart from a different position. Filters are bounded and indexed before becoming public.

## Streaming updates

Streaming is optional for the first implementation. If provided, it uses a documented resumable cursor, heartbeat, maximum connection lifetime, per-principal connection limit, bounded server buffer, and explicit slow-consumer behavior.

Streams are observational and MUST NOT become the sole source of durable audit or event history. A disconnected client can resume from the kernel diagnostic history while its cursor remains inside the declared 7-day/byte-bounded window. Backpressure from a management client never blocks core event processing.

## HTTP binding

A future HTTP binding follows RFC 9110 and RFC 9111:

- GET and HEAD are safe and have no state-changing side effects;
- PUT and DELETE are idempotent where their resource model supports them;
- POST is used for commands whose semantics are not idempotent, with an idempotency key for retryable administrative actions;
- conditional requests use entity tags or modification validators for concurrent updates;
- cache behavior is explicit; sensitive responses default to `Cache-Control: no-store`;
- `Retry-After` accompanies temporary service or rate-limit responses when a useful value is known;
- request and response media types and size limits are explicit.

Machine-readable errors use RFC 9457 `application/problem+json`. Each problem has an absolute stable type URI, HTTP status, safe title/detail, correlation instance, and structured validation entries using JSON Pointer where applicable. Stack traces, SQL, filesystem paths, secrets, and internal SDK errors are not returned.

## Limits and abuse resistance

The management adapter enforces request-body bytes, header bytes/count, parse depth, query complexity, response bytes, request duration, stream count, and per-principal concurrency/rate limits before expensive work.

Rate-limit rejection uses HTTP `429` for HTTP bindings and includes retry guidance when known. Service saturation may use `503`; it is not misreported as authorization failure. Limits and rejections are observable with bounded principal classes, not raw identities as metric labels.

## Consistency and commands

Read responses identify the observation time and instance. Multi-component summaries do not claim transactional consistency unless produced from one snapshot. Unknown or unavailable fields are represented explicitly rather than as zero values that imply health.

Long-running commands return a stable operation identity and state: queued, running, succeeded, failed, canceled, or expired. Command status includes safe progress, timestamps, final problem type, audit correlation, and idempotency expiry.

The kernel management-command capability owns a durable SQLite ledger. An idempotency identity is `(deployment epoch, authenticated principal stable ID, action, canonical resource target, client-supplied key)`. The first accepted command stores a canonical request digest and operation identity atomically before execution. A repeat with the same identity and digest returns that operation and never re-executes it; the same identity with a different digest returns a conflict. Different principals, actions, targets, or deployment epochs never collide.

Ledger entries remain for at least 7 days after terminal completion and while an operation is non-terminal. Responses publish the exact expiry time. After expiry, reuse is a new command; clients that require certainty MUST query before expiry. Process restart preserves the deployment epoch and ledger. Restore or deliberate replacement creates a new epoch so restored stale keys cannot suppress current commands. Ledger state is included in full backup but old-epoch entries are read-only after restore.

Online restore, destructive repair, and schema downgrade are not ordinary network commands. They require the offline workflow in `operations.md`.

## Versioning

The management contract is unstable until a source schema is approved. Once stable, incompatible semantic or representation changes require a new major version with an explicit support window. Adding optional fields is not considered safe when clients reject unknown fields; compatibility tests define the actual policy.

Generated OpenAPI or client code MAY be produced from the future approved source schema. Generated output MUST NOT replace these capability, security, and failure requirements.

## Verification

Acceptance tests cover unauthenticated and unauthorized denial, role separation, conditional update conflict, cursor stability/expiry, maximum limits, slow streams, idempotent command replay, RFC 9457 errors, audit records, redaction, drain behavior, and management isolation during core overload.
