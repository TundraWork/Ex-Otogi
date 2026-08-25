# Extension Contracts

## Model

Extensions are reviewed Go packages compiled into the Ex-Otogi binary. The composition root registers them explicitly. Dynamic native plugins, arbitrary uploaded code, filesystem discovery, and mutable package-global registries are prohibited.

Static composition provides type safety and reproducible artifacts; it is not a security sandbox. An extension receives only the capabilities it requires and remains subject to runtime limits.

## Extension kinds

### Module

A module implements application behavior. It declares event interests and handles authorized domain events through capability interfaces. It MUST NOT parse platform wire payloads, import platform SDKs, access raw storage, or create management endpoints directly.

### Driver

A driver adapts one external platform or infrastructure protocol. It authenticates remote input, validates resource limits, maps payloads to domain events, and translates capability calls to external effects. It MUST NOT choose business routing or module policy.

### Infrastructure adapter

An infrastructure adapter implements a kernel capability such as storage, clock, telemetry, or HTTP serving. It follows the owning contract and does not expose implementation types across the boundary.

## Descriptor

Each extension exposes an immutable descriptor containing:

- reverse-DNS or repository-qualified identity;
- human-readable name;
- extension kind;
- contract version range;
- configuration schema version;
- capabilities provided and required;
- event kinds consumed or emitted;
- declared ordering and concurrency scopes;
- sensitive-data categories accessed;
- required network destinations and durable namespaces.

Descriptor validation occurs before construction. Duplicate identities, capability ambiguity, missing dependencies, incompatible contract versions, or dependency cycles fail startup.

## Construction and lifecycle

Registration supplies a descriptor and constructor. A constructor receives validated extension-specific configuration plus explicit capability handles and returns an inert instance. It MUST NOT read global configuration, open network listeners, mutate registries, or start goroutines.

Long-lived extensions implement the lifecycle in `runtime.md`. Handler-only modules may omit `Start` when construction needs no external readiness. All handlers accept context first and are safe for the declared concurrency model.

An extension owns every goroutine and resource it starts. `Stop` returns only after they terminate or reports a timeout cause. Module and driver entrypoints are panic boundaries managed by the runtime supervisor.

## Capability access

Capabilities are narrow, scope-aware handles issued after authorization and configuration validation. A capability MUST enforce tenant, platform account, conversation, destination, and resource constraints at its boundary; callers do not receive ambient process authority.

Capability APIs define:

- idempotency semantics;
- durability class;
- timeout and cancellation behavior;
- stable error categories;
- ownership of returned buffers and streams;
- concurrency safety;
- telemetry emitted without sensitive payloads.

An optional capability is represented explicitly and tested both present and absent. A nil dependency is not feature negotiation.

## Configuration

Each extension owns a versioned typed schema. Unknown fields are rejected by default. Defaults are deterministic and documented. Validation checks relationships and resource bounds, not only primitive types.

Secrets are references resolved by the composition root or an authorized secret capability; schemas MUST NOT encourage inline secret values. Configuration errors identify the extension and field path without echoing sensitive values.

Reload is opt-in per field. An extension that supports reload validates a complete candidate, applies it atomically, and preserves the prior configuration on failure. Capability changes, dependency graph changes, storage migrations, and listener identity changes require restart unless a later specification proves a safe transition.

## Invocation behavior

- At admission, the kernel fixes the eligible module set and creates one durable delivery per `(runtime event ID, module identity)`. Routing order is deterministic but does not make module deliveries one shared transaction.
- Each module delivery uses ordering key `(module identity, module-declared ordering key)`. A retry blocks later events only for that module/key; deliveries to other modules proceed independently unless the event's owning policy explicitly declares fail-fast routing.
- A module declares whether handlers for one ordering key are serial or concurrency-safe.
- Handler `success` commits terminal success for that delivery. `permanent rejection` commits a terminal rejected outcome. `retryable failure` keeps that delivery at its ordering-key head and applies the kernel retry budget; exhaustion commits terminal failed. `canceled` follows the owning context: shutdown preserves durable pending delivery, while caller cancellation commits terminal canceled only when no durable recovery contract applies.
- The source event is complete and may be acknowledged upstream only after every fixed module delivery is terminal. Aggregate outcome is `succeeded` when all deliveries succeeded, `rejected` when none failed and at least one rejected, `canceled` when none failed and at least one canceled, and `failed` when any delivery exhausted retries or ended in permanent failure. Aggregate acknowledgement never erases per-module outcomes; durable failed/rejected/canceled deliveries remain queryable through diagnostic retention.
- Extensions MUST NOT implement private retry loops around runtime capabilities; they return classified errors to the owning policy.
- Before each logical side effect, the runtime derives an idempotency key from `(runtime event ID, module identity, capability identity, effect slot)`. The effect slot is a module-declared stable name plus zero-based occurrence for repeated same-kind effects. The same logical effect retains its key across retries and crash recovery; fan-out destinations use distinct capability identity or effect slot. Changing effect-slot meaning is a persisted-contract migration.
- One extension MUST NOT obtain another extension's concrete instance.

## Compatibility

Contracts are unstable until explicitly declared stable. Once stable:

- breaking source or semantic changes require a new major contract version;
- additive optional capability changes require a minor version;
- fixes that preserve observable behavior require a patch version;
- one runtime release supports only the contract ranges declared in its release manifest.

The `next` branch does not support pre-`next` extension interfaces. Compatibility adapters require a separate approved specification and an end-of-life date.

## Observability and audit

The runtime attributes invocation duration, outcome, saturation, and failure class to bounded extension identity. Extensions may add approved low-cardinality attributes but MUST NOT emit raw message content, secrets, user identifiers, or unbounded platform values by default.

Capability issuance, denied access, configuration changes, lifecycle failures, and administrative enable/disable decisions produce audit records.

## Verification

Each extension package provides contract tests for descriptor validation, construction without side effects, capability scoping, cancellation, declared concurrency, panic containment, and lifecycle termination. Drivers additionally test malformed, oversized, replayed, and unauthenticated input. The composition test proves every registered extension graph is complete and acyclic.
