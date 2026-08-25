# Reliability

## Failure model

Failures are classified at the boundary that can act on them:

- **invalid:** input or configuration cannot satisfy the contract; do not retry unchanged;
- **unauthorized:** identity lacks permission; do not retry without a credential or policy change;
- **conflict:** current state prevents the operation; caller may re-read and make a new decision;
- **capacity:** a local or remote limit rejected work; retry only within an explicit budget;
- **transient dependency:** a bounded dependency outage may recover;
- **permanent dependency:** configuration, protocol, or unsupported behavior requires intervention;
- **canceled/deadline:** owning context ended; do not detach a retry;
- **ambiguous effect:** an external side effect may have happened; retry only with proven idempotency;
- **invariant:** internal state is unsafe; fail the owning component or process.

Adapters translate implementation errors into these stable classes once. The original cause remains available for diagnosis without exposing secrets to clients.

## Retry policy

Retries are allowed only when the operation is idempotent or carries a stable idempotency key, the failure class is retryable, and the caller owns a finite retry budget.

A retry policy defines maximum attempts, maximum elapsed time, per-attempt timeout, exponential backoff, randomized jitter, and cancellation. Server guidance such as `Retry-After` is honored inside the remaining budget. Nested layers MUST NOT independently retry the same operation.

Authentication failures, validation failures, unsupported operations, and exhausted capacity without retry guidance are not retried. Retry attempts, final outcome, and budget exhaustion are observable.

## Overload and isolation

The runtime protects itself before downstream dependencies:

1. reject work above hard input limits;
2. apply per-principal and per-destination admission limits;
3. enforce bounded process, driver, module, and storage concurrency;
4. shed only work declared expendable;
5. preserve capacity for shutdown, health, and operator diagnosis.

Circuit breakers MAY protect a remote dependency when measured failures would otherwise consume capacity. Breaker states and thresholds are explicit; probes are bounded; a breaker does not convert permanent errors into transient ones.

One failing ordering key, module, platform account, or exporter SHOULD not starve unrelated work. Isolation is verified under fault and load, not assumed from package boundaries.

## Durability classes

Every queued operation and state record declares one class:

- **ephemeral:** loss on process exit is acceptable;
- **recoverable:** can be reconstructed from an authoritative source;
- **durable:** acknowledged only after transaction commit and recovered after restart;
- **auditable:** durable plus retention and tamper-evidence requirements.

Durability is end-to-end. An in-memory acknowledgement cannot be described as durable because a later worker intends to persist it.

## Crash recovery

Startup MUST distinguish clean shutdown, interrupted shutdown, and incompatible or corrupt state. Recovery:

- rolls back incomplete SQLite transactions through database guarantees;
- resumes or classifies durable intents without duplicating non-idempotent effects;
- validates schema and critical invariants before readiness;
- records recovery duration and unresolved work;
- fails closed when ownership or integrity cannot be established.

Automatic repair that discards durable data is prohibited. Destructive recovery requires an explicit operator command, backup, and audit record.

## Backup and restore objectives

Each release declares recovery point objective (RPO) and recovery time objective (RTO) for durable and auditable state. A default deployment MUST support online, database-aware backups while serving traffic.

A backup is accepted only when it includes schema metadata, release compatibility, configuration references without secret values, checksum, creation time, and completion status. Restore is tested into an empty installation and verifies integrity, schema compatibility, required state, and readiness. An untested backup is not recovery evidence.

## Dependency failure

Required dependencies gate readiness. Optional dependencies expose degraded capability without falsifying readiness for unrelated paths. Dependency health uses bounded active checks or recent real operations; it MUST NOT create synchronized retry storms.

DNS, clock, disk-full, permission, file-descriptor, and database-busy failures receive explicit tests or fault scenarios when applicable. Disk-space reserves or admission thresholds MUST leave capacity for diagnostics and clean shutdown.

## Fault testing

The authoritative gate includes deterministic fault injection at contract boundaries. Required scenarios include:

- cancellation during queue admission and external I/O;
- timeout before and after an ambiguous side effect;
- process interruption during durable work;
- SQLite busy, disk-full, corruption detection, and restore;
- platform rate limiting and temporary unavailability;
- module panic and stalled handler;
- telemetry exporter outage;
- shutdown while queues are saturated.

A scenario passes only when outcome, retained state, emitted evidence, resource termination, and subsequent recovery match the documented contract.

## Reliability evidence

Release evidence includes capacity results, shutdown timing, race and leak results, fault-scenario outcomes, latest successful restore drill, and known deviations from published RPO/RTO. A reliability regression blocks release unless the governing product requirement is explicitly amended.
