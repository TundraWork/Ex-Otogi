# Product Boundary

## Mission

Ex-Otogi is a self-hosted runtime that receives events from instant-messaging platforms, applies deterministic kernel policy, invokes statically composed modules, and emits controlled side effects through platform adapters.

The product optimizes for understandable behavior, bounded resource use, safe extension, and operator diagnosis on one node. Feature count and speculative distribution are subordinate to those qualities.

## Actors

- **Participant:** sends or receives content through an external messaging platform.
- **Operator:** configures, starts, observes, backs up, restores, upgrades, and stops an installation.
- **Module author:** implements application behavior against stable runtime capabilities.
- **Driver author:** adapts a platform or external service without embedding business policy.
- **Release engineer:** produces and verifies source-derived artifacts.

## Core capabilities

A conforming runtime MUST:

1. ingest platform events and normalize them into platform-neutral domain values;
2. route accepted events to eligible modules under bounded concurrency;
3. expose explicit capabilities for messaging, time, persistence, and other approved side effects;
4. preserve correlation and causality across accepted work;
5. reject, defer, or shed work explicitly when capacity is exhausted;
6. stop accepting work and terminate owned resources within configured shutdown bounds;
7. expose authenticated operator status and diagnostic evidence;
8. persist durable state transactionally on a local single-node store;
9. produce verifiable release artifacts from reviewed source.

## Workload envelope

Every release MUST publish a tested envelope containing, at minimum:

- supported operating systems and architectures;
- maximum configured modules, platform accounts, and active conversations;
- sustained and burst ingress rates;
- maximum event and attachment metadata sizes;
- queue capacities and concurrency limits;
- steady-state memory and file-descriptor bounds;
- database size and write-rate test points;
- graceful shutdown deadline;
- recovery point and recovery time objectives.

Defaults MUST fit inside the published envelope. Inputs outside hard safety limits are rejected. Load outside a tested capacity is admitted only through explicit overload policy; the runtime MUST NOT imply unlimited capacity.

No universal numeric targets are declared before representative benchmarks exist. The first executable release specification MUST set values and provide the workload used to derive them.

## Service qualities

### Correctness

- One accepted event has one immutable identity.
- Processing is at-least-once where an external platform can redeliver; handlers therefore MUST be idempotent or use an atomic effect boundary.
- The runtime MUST NOT claim exactly-once delivery across external systems.
- Authorization and tenant/conversation scope are evaluated before a module observes protected data or obtains a capability.

### Availability

- Failure of one event, module invocation, platform account, or telemetry exporter SHOULD remain inside that boundary.
- A process-wide invariant violation or unavailable required dependency MUST fail startup or readiness visibly; it MUST NOT produce a false healthy state.
- Shutdown is graceful and time-bounded, with an explicit forced-termination outcome after the deadline.

### Operability

An operator MUST be able to determine, without source-code access:

- which configuration and release are active;
- whether the process can accept new work;
- current capacity pressure and degraded dependencies;
- why a component failed to start or stopped;
- whether backup, restore, migration, and shutdown completed.

### Security and privacy

The runtime minimizes retained data, authority, exposed network surface, and telemetry content. Secrets and raw message content are excluded from routine diagnostics by default.

## Non-goals

The core runtime is not:

- a general distributed workflow engine;
- a dynamic marketplace for untrusted native plugins;
- a multi-tenant public SaaS control plane;
- a guarantee of uninterrupted service during single-node failure;
- a repository for arbitrary message history;
- a definition of long-term semantic memory or control-panel UX.

These exclusions prevent implementation scaffolding for unapproved future systems.

## Acceptance evidence

A release satisfies this product boundary only when the quality gates demonstrate:

- representative end-to-end event ingestion and side effect;
- overload behavior without unbounded growth;
- cancellation and shutdown termination;
- crash recovery of durable state;
- authenticated operator diagnosis;
- backup restoration into a clean installation;
- artifact provenance and vulnerability review.
