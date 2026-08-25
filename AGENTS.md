# Ex-Otogi Engineering Rules

This file is the executable index for work on Ex-Otogi. Detailed requirements live in `docs/`; do not duplicate or weaken them here.

## Authority and conformance

1. Direct user instructions control task intent.
2. This file controls repository workflow and non-negotiable engineering rules.
3. `docs/README.md` defines specification precedence; linked specifications control their domains.
4. An accepted design or plan may add constraints, but may not violate higher-authority rules.

The key words **MUST**, **MUST NOT**, **SHOULD**, **SHOULD NOT**, and **MAY** are normative as defined in BCP 14. A deviation from a MUST requires an approved specification change, not a local exception.

## Required workflow

For every non-trivial change:

1. **Research:** read the governing documents and affected code paths; inspect callers before changing a contract.
2. **Plan:** state milestones, dependencies, observable success criteria, and rollback implications before implementation.
3. **Implement sequentially:** keep one milestone active; finish and verify it before starting the next.
4. **Verify behavior:** exercise the changed path at its real boundary. Tests supplement this evidence; they do not replace it.
5. **Review the whole change:** remove obsolete paths, update callers and operator material, and check architecture, security, reliability, and observability impacts.
6. **Record evidence:** report exact commands or scenarios run and their outcomes. Never claim unobserved success.

Do not ask a human for facts available from the repository, toolchain, or referenced primary sources. Stop only for a material product decision or an external prerequisite that cannot be obtained safely.

## Non-negotiable system invariants

- Ex-Otogi is a single-node, statically composed Go modular monolith unless an approved specification demonstrates a need to distribute it.
- Dependencies flow from contracts to kernel policy to adapters. Kernel packages MUST NOT import adapter implementations. See `docs/architecture.md`.
- Every goroutine, queue, timer, subscription, and external resource has one explicit owner and a bounded termination path. See `docs/runtime.md`.
- Work queues and concurrency are bounded. Overload produces an explicit admission, shedding, or backpressure decision; it never grows memory without limit.
- Cancellation and deadlines propagate across every blocking boundary. Retry loops are bounded, cancellable, observable, and restricted to idempotent operations.
- Panics are contained and reported at goroutine, task, and extension boundaries; panic is never expected control flow.
- External input is untrusted. Authentication, authorization, resource limits, and sensitive-data handling follow `docs/security.md`.
- Errors are handled exactly once: return, translate, compensate, or terminate the owning lifecycle. Logging alone is not handling.
- Telemetry MUST NOT contain secrets or raw message content by default, and telemetry failure MUST NOT block core processing. See `docs/observability.md`.
- Durable state follows `docs/operations.md`; code MUST NOT copy a live SQLite database file or assume WAL works across a network filesystem.
- There is no compatibility promise to the pre-`next` implementation. Do not add shims, dual writes, legacy aliases, or migrations unless a future approved specification requires them.

## Go engineering rules

- Use a stable Go release covered by upstream security fixes, as defined in `docs/quality.md`.
- Accept `context.Context` first for request-scoped, blocking, or long-lived operations. Do not store request contexts in structs.
- Prefer clear ownership and synchronous code. Start concurrency only when it improves required behavior and its lifecycle can be proven.
- Preserve error identity with `%w` when adding operation context. Use typed or sentinel errors only when callers need stable branching semantics.
- Avoid hidden mutable global state. Construct dependencies explicitly at the composition root.
- Public contracts use domain types rather than adapter payloads. Exported API has behavioral Godoc.
- Avoid avoidable allocation, copying, reflection, and serialization on hot paths; require benchmark or profile evidence before complexity motivated solely by performance.
- Generated code is reproducible and never edited by hand.

## Verification gates

Run the smallest relevant checks during development and the authoritative gates before completion. The implemented repository MUST provide stable commands for:

- formatting and generated-file consistency;
- static analysis and architecture-boundary enforcement;
- unit and integration tests;
- race tests for concurrent code;
- fuzz tests for parsers and trust-boundary decoders;
- leak and shutdown tests for long-lived components;
- vulnerability scanning and release-artifact verification.

The exact gate contract is in `docs/quality.md`. A failing required gate blocks completion. Do not suppress, skip, or weaken a gate to make a change pass; fix the cause or amend the governing specification.

## Specification map

Start at `docs/README.md`.

- Product boundary: `docs/product.md`
- Architecture: `docs/architecture.md`
- Runtime semantics: `docs/runtime.md`
- Extensions: `docs/extensions.md`
- Reliability: `docs/reliability.md`
- Security: `docs/security.md`
- Observability: `docs/observability.md`
- Quality and release: `docs/quality.md`
- Configuration, persistence, and operations: `docs/operations.md`
- Operator capabilities: `docs/management-plane.md`
- External standards: `docs/standards.md`

Long-term memory internals and control-panel UX are intentionally unspecified. Changes in those areas require their own approved specifications while respecting these core-runtime contracts.
