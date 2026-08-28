# Operations

## Deployment model

One Ex-Otogi process and its SQLite files run on one host. The process may be supervised by an init system or container runtime, but it does not require a cluster control plane. Horizontal coordination, active-active writers, and network-filesystem databases are outside scope.

The release manifest declares supported operating systems, architectures, filesystem assumptions, minimum free space, ports, and resource limits.

## Configuration

Configuration is typed, versioned, and validated as one complete candidate before resources start. Precedence from lowest to highest is:

1. compiled safe defaults;
2. one primary configuration file;
3. explicitly supported environment overrides;
4. command-line operational overrides.

The effective value records its non-secret source. Unknown fields, duplicate identities, invalid durations, incompatible options, unsafe zero limits, and values outside the published envelope fail validation. Errors identify field paths without echoing secrets.

Environment variables are reserved for simple deploy-specific overrides and secret references; arbitrary object trees encoded into variable names are prohibited. Command-line arguments MUST NOT carry secret values.

The process provides a validation-only command that performs schema and dependency checks without binding public listeners or mutating durable state.

### Telegram storage configuration

Each Telegram account has two absolute, clean, distinct paths: a gotd session file and an adapter SQLite database. Both MUST be on a local filesystem; network, FUSE, URI-style, in-memory, symlink, and shared paths are rejected. On Unix-like systems the session parent directory MUST be mode `0700`; existing or newly created session and database files MUST be regular mode `0600`, and an existing database MUST be owned by the current service user. SQLite WAL and shared-memory sidecars inherit the same protected directory boundary. One account MUST NOT reuse another account's session or database.

The SQLite binding records the immutable deployment account key, authorized Telegram user ID, gotd module version, and generated Telegram layer. Startup fails on any mismatch rather than rebinding or creating apparently fresh state. The exact setup and inspection commands are in the [Telegram MTProto runbook](runbooks/telegram-mtproto.md#prepare-private-storage).

## Reload

Reload is opt-in. Logging level, telemetry sampling within bounded limits, and selected admission limits MAY be reloadable. The runtime validates a full candidate, computes supported changes, applies them atomically, and retains the previous configuration on failure.

Listener identity, extension graph, secret-provider type, database location, schema version, and capability wiring require restart. Reload never runs a migration or partially replaces dependencies.

## SQLite contract

SQLite is the default durable store. Database, WAL, and shared-memory files reside on a local filesystem with documented locking and durability behavior. WAL mode MUST NOT be used over a network filesystem.

Startup configures and verifies, rather than assumes:

- foreign-key enforcement;
- journal mode;
- synchronous durability level;
- busy timeout/handler;
- connection limits;
- application and schema versions.

One capability coordinates writes to avoid uncontrolled writer contention. Read transactions are short and close rows promptly. Long-running reads that block checkpoint progress are observable.

WAL size, checkpoint attempts/results/duration, busy outcomes, page count, free space, and write latency are monitored. Checkpoint policy is selected from workload evidence; defaults are not treated as universally optimal.

## Migrations

Each capability owns ordered, immutable migration files. A migration runs under an exclusive process startup phase before readiness. The migrator:

1. acquires a single-instance lock;
2. verifies current application/schema compatibility and integrity prerequisites;
3. records migration start and target;
4. applies one transactional step where SQLite permits;
5. verifies the new schema and records completion;
6. fails startup on partial or unknown state.

A released migration is never edited. Destructive transformations require a tested backup and explicit rollback/restore procedure. The application MUST NOT silently create a fresh database when an expected database is unreadable or incompatible.

## Backup

A live database backup uses SQLite's online backup API, `VACUUM INTO` where its locking and space behavior fit, or another documented database-aware snapshot. Copying only the live main database file is prohibited because committed state may reside in WAL.

A backup workflow:

- verifies destination permissions and free space;
- creates a point-in-time snapshot without unbounded write interruption;
- writes release, schema, and configuration-reference metadata;
- excludes secret values;
- computes a checksum;
- runs integrity and application-level validation on the snapshot;
- publishes success only after durable completion.

Retention, encryption, off-host transfer, and access control are deployment policy but MUST satisfy the declared RPO.

Auditable state is included in every full backup. Audit backup metadata records the covered stream IDs and first/last record IDs and digests. The default audit RPO is 24 hours; a deployment MAY choose a shorter interval but MUST NOT weaken it without an approved security-risk decision. Before retention expiry, the operator verifies that a completed, readable backup covers the records to be removed.

### Telegram state backup and restore

A Telegram recovery point is one inseparable pair: the opaque gotd session file plus a database-aware snapshot of the matching SQLite state. The pair MUST be captured while the adapter is offline, or under a coordinated procedure that prevents the two resources from representing different account moments. Copying the live SQLite main file or backing up only one member is prohibited. Backup metadata records account key, authorized user ID, gotd version, Telegram layer, schema version, creation time, and both checksums without recording credentials or session bytes.

Before leaving the protected host boundary, the pair and metadata MUST be encrypted together for an approved recovery identity, stored under least-privilege access control, and governed by the account retention policy. Restore is offline into empty private paths. It verifies encryption, checksums, metadata agreement, SQLite integrity, account binding, versions/layer, and `0700`/`0600` protections before startup. A restore drill MUST prove that the paired artifacts authorize the expected account and reach reconciled or explicitly acknowledged degraded readiness. Exact backup and restore commands are in the [runbook](runbooks/telegram-mtproto.md#paired-backup-and-restore).

## Restore

Restore is a deliberate offline operation into an empty or archived data location. It verifies checksum, SQLite integrity, application invariants, schema compatibility, ownership/permissions, required secret references, and every restored audit digest chain before replacing active state.

A restore drill runs at the cadence declared by the release or deployment, at least once per release line for production. Evidence records backup age, restored revision, duration, validation outcome, and whether RPO/RTO were met.

## Startup and health

Startup order follows `architecture.md`: validate configuration, resolve secrets, open and verify state, migrate, recover durable work, start dependencies, start admission, then mark ready. Any required step failure returns a non-zero exit and bounded diagnostic cause.

Liveness starts only after process initialization. Readiness remains false until recovery completes and becomes false before drain.

For Telegram, readiness additionally requires successful session/account binding, the update-manager startup gate, and reconciled update state. An unrecoverable difference or persisted gap keeps readiness false and records the affected account/box, position, and bounded cause. An operator MAY acknowledge that durable marker after assessing the missing-history impact; acknowledgement permits readiness but does not repair or erase the gap. The [runbook](runbooks/telegram-mtproto.md#degraded-update-gaps) defines the decision record and recovery procedure.

## Rollout and rollback

An upgrade procedure states supported source versions, schema effects, expected downtime, backup prerequisite, smoke check, and rollback boundary. Binary rollback is allowed only while the persisted schema remains readable by the prior release. Otherwise rollback means restore the pre-upgrade backup.

Operators never discover rollback incompatibility during an incident; CI tests every declared upgrade and rollback path with representative data.

Telegram upgrades pin both `github.com/gotd/td` and its generated `tg.Layer`. A change to either requires review of generated type confinement, account-binding compatibility, schema/migrations, mapper fixtures (including unknown constructors and entities), authorization variants, update-difference handling, send/refetch RPCs, and paired restore compatibility. Regenerate or update fixtures from the newly pinned public generated types, never by editing generated gotd sources. Run the exact upgrade gates in [`quality.md`](quality.md#gotd-upgrades) and the [runbook](runbooks/telegram-mtproto.md#gotd-upgrade); rollback requires the pre-upgrade paired backup when the prior binary cannot read the new binding or schema.

## Shutdown and signals

The first termination signal begins the bounded graceful sequence in `runtime.md`. A repeated signal requests immediate termination and records forced outcome when possible. Configuration reload uses a distinct explicit command or signal only when supported.

Exit codes distinguish clean stop, configuration/startup failure, invariant failure, forced shutdown, and operator-command failure.

## Runbooks

A production-ready release includes concise runbooks for install, validation, start, readiness failure, capacity saturation, dependency outage, disk pressure, backup, restore, migration failure, credential rotation, upgrade, rollback, and incident evidence collection. Each runbook uses stable commands and identifies destructive steps before execution.

The Telegram-specific runbook is [`runbooks/telegram-mtproto.md`](runbooks/telegram-mtproto.md). It covers private storage, authentication, paired backup/restore, degraded gaps, retention/erasure, opt-in live smoke, and gotd upgrades. These procedures support an experimental milestone and MUST NOT be described as production release procedures.
