# Architecture

## Architectural style

Ex-Otogi is a statically composed Go modular monolith. One process owns ingress, kernel orchestration, modules, adapters, management capabilities, and the default SQLite store. Components retain strict boundaries even though calls may be in-process.

The runtime MUST NOT introduce a network boundary, broker, sidecar, or independently deployed service without measurements showing that the approved single-node workload or failure-isolation requirement cannot be met more simply.

## Dependency direction

Source dependencies flow inward to policy:

```text
composition root -> adapters ----> contracts
                 -> kernel ------> contracts
                 -> modules -----> contracts
```

The logical layers are:

1. **Contracts:** platform-neutral domain values and narrow capability interfaces.
2. **Kernel:** admission, routing, lifecycle, authorization coordination, and runtime policy.
3. **Modules:** application behavior expressed only through contracts.
4. **Adapters:** platform protocols, persistence engines, telemetry exporters, clocks, and transports.
5. **Composition root:** configuration loading, concrete construction, dependency validation, and process lifecycle.

The diagram shows compile-time imports, not permission to bypass policy. The following rules apply:

- Contracts MUST NOT import kernel, module, or adapter packages.
- Kernel MUST NOT import concrete adapters or modules.
- Modules MUST NOT import kernel internals, another module's internals, or concrete adapters.
- Adapters MUST translate I/O and MUST NOT decide business routing, authorization policy, retry policy, or module behavior.
- Only the composition root may select concrete implementations and assemble the process graph.
- Import-boundary checks MUST be automated in the authoritative quality gate.

## Contracts

Public contracts contain immutable or ownership-safe domain values. Platform SDK objects, SQL rows, HTTP request types, and telemetry SDK types MUST NOT cross into kernel or module APIs.

A capability interface SHOULD be declared by its consumer and contain only operations that consumer requires. Interfaces are not created solely to mock concrete code. Contract changes MUST state compatibility, cancellation, error, ownership, and concurrency semantics.

Domain identifiers are opaque typed values. IDs from separate domains MUST NOT share an interchangeable string alias. Time values identify their clock and precision; durations use `time.Duration`. Byte sequences and collections document whether ownership transfers or a copy is required.

### Telegram adapter confinement

The Telegram adapter owns every gotd value and generated `github.com/gotd/td/tg` type. Those types MUST remain below `internal/adapter/telegram`; the adapter translates them to protocol-neutral contracts before calling the kernel, modules, or any other adapter. Session sequence state, access hashes, transport journals, raw update quarantine, and Telegram account binding are likewise adapter-owned. The import guard and generated-version checks are defined in [`quality.md`](quality.md#telegram-quality-gates).

## Kernel ownership

The kernel owns:

- admission and overload decisions;
- event normalization invariants after adapter validation;
- authorization coordination and capability issuance;
- deterministic routing and handler ordering policy;
- invocation lifecycle and cancellation;
- failure classification at module boundaries;
- shutdown sequencing.

The kernel does not parse platform wire formats, issue SQL, render transport payloads, or contain module-specific behavior.

## Persistence boundaries

Each durable capability owns its schema, queries, migration sequence, and transaction semantics. Packages MUST NOT share raw database connections, table knowledge, or mutable records across capability boundaries.

A transaction begins and ends inside one owning capability. A caller may request one semantic operation but MUST NOT orchestrate a transaction by receiving a raw transaction handle. Cross-capability atomicity requires an explicit owning service or a durable intent/outbox design; nested best-effort writes are prohibited.

Persisted records carry a schema version and stable identity. Migrations are forward-only within a release line and follow `operations.md`. Serialization formats at durable boundaries are versioned and bounded.

The kernel owns one audit capability; its SQLite schema, append transaction, retention deletion, and query API form one persistence boundary. Security-sensitive decisions append their audit record in the same transaction as local authoritative state when both share the audit store. When an external effect prevents atomicity, the operation records a durable intent before the effect and a terminal or ambiguous outcome afterward. Best-effort telemetry is never the audit store.

## Composition and startup

The composition root MUST:

1. parse and validate typed configuration;
2. construct clocks, storage, telemetry, drivers, modules, and kernel explicitly;
3. reject duplicate identities, missing capabilities, dependency cycles, and incompatible schema versions before readiness;
4. start components in dependency order;
5. expose readiness only after required dependencies are usable;
6. stop components in reverse dependency order within the shutdown budget.

Constructors MUST NOT start goroutines or perform hidden network I/O. Construction creates a valid inert value; lifecycle methods perform bounded work under context.

For the Telegram composition, the root constructs a single-use gotd client and its adapter-owned session storage, SQLite store, update manager, resynchronizer, and inbox dispatcher. Startup first validates and opens the separate storage resources, restores authorization, binds the authorized account, starts update capture and the manager, reconciles durable gaps, then starts dispatch. It publishes ready only after the startup criteria defined in [`runtime.md`](runtime.md#telegram-lifecycle-specialization) are met. Child failure, drain, stop ordering, and the no-restart rule are governed there; operator procedures are in the [Telegram MTProto runbook](runbooks/telegram-mtproto.md).

The opt-in Telegram smoke is intentionally not that composition root: it constructs only gotd session storage and a client to verify existing-session connectivity. It does not open the adapter SQLite store or claim account-binding, recovery, dispatcher, or driver readiness.

## Data flow

1. A driver authenticates and validates an external payload within declared size limits.
2. The driver converts it to an immutable domain event and submits it to kernel admission.
3. The kernel applies scope, authorization, routing, and capacity policy.
4. Eligible modules run through scoped capabilities.
5. Side effects return through capability ports to adapters.
6. Correlation, outcome, and capacity signals are emitted without exposing sensitive content.

An adapter failure is translated once into a stable contract error. Kernel and modules MUST NOT branch on SDK-specific status or error types.

## State and globals

Mutable process state has one owner and explicit synchronization. Package initialization MUST NOT register components, read configuration, open resources, or mutate registries. Constants and immutable lookup tables are allowed; service locators and writable singletons are not.

## Architecture verification

Conformance evidence includes:

- automated forbidden-import tests;
- construction tests for missing, duplicate, and cyclic dependencies;
- tests proving contracts do not expose adapter types;
- migration ownership checks;
- an end-to-end path from normalized ingress through kernel policy to a fake side-effect adapter.
