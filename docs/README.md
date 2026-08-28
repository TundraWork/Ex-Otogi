# Ex-Otogi Core Runtime Specification

This directory is the authoritative blueprint for the `next` core runtime: a self-hosted, single-node, event-driven IM automation service implemented as a Go modular monolith.

## Conformance language

The key words **MUST**, **MUST NOT**, **REQUIRED**, **SHOULD**, **SHOULD NOT**, and **MAY** are interpreted as described by [BCP 14](https://www.rfc-editor.org/info/bcp14) when they appear in capitals.

An implementation conforms only when it satisfies every applicable MUST and MUST NOT. A SHOULD may be violated only when the design records the concrete reason, consequences, and compensating evidence. Examples and rationale are non-normative.

## Experimental milestone status

The current executable work is an **experimental, non-conforming vertical slice**, not an Ex-Otogi release. It intentionally implements only the approved milestone scope. The full product requirements in `product.md` and full release gates in `quality.md` remain binding before any production-ready or conforming release claim.

Commands introduced for this slice are milestone gates only. They MUST NOT be named or documented as satisfying deferred authenticated management, security scanning, telemetry, SBOM, signature, provenance, artifact, or release-verification requirements.

The implemented core event bridge and its exact offline milestone commands are documented in [`runbooks/core-runtime-smoke.md`](runbooks/core-runtime-smoke.md). The opt-in Telegram MTProto driver, including its separate live smoke and operator constraints, is documented in [`runbooks/telegram-mtproto.md`](runbooks/telegram-mtproto.md). Neither runbook is a production release procedure.

## Scope

This suite specifies the core runtime, its statically linked extensions, platform adapters, operator boundary, engineering system, and single-node operations.

It intentionally does not specify:

- long-term memory formation, retrieval, or ranking;
- control-panel information architecture or visual design;
- compatibility with code, configuration, APIs, or data created before `next`;
- multi-node coordination or distributed execution.

A future specification may add a deferred capability only through the contracts defined here. Distribution requires measured evidence that a single node cannot meet an approved workload or availability objective.

## Document map

| Document | Authority |
| --- | --- |
| [`product.md`](product.md) | Goals, actors, workload envelope, service qualities, non-goals |
| [`architecture.md`](architecture.md) | Package boundaries, dependency direction, composition, persistence ownership |
| [`runtime.md`](runtime.md) | Event processing, concurrency, lifecycle, cancellation, shutdown |
| [`extensions.md`](extensions.md) | Module and platform-driver contracts |
| [`reliability.md`](reliability.md) | Failure model, overload, retries, recovery, durability |
| [`security.md`](security.md) | Trust boundaries, authorization, secrets, secure development |
| [`observability.md`](observability.md) | Traces, metrics, logs, health, diagnostic evidence |
| [`quality.md`](quality.md) | Go policy, tests, analysis, CI, release evidence |
| [`operations.md`](operations.md) | Configuration, SQLite, migrations, backup, rollout, rollback |
| [`management-plane.md`](management-plane.md) | Technology-neutral operator capabilities and protocol rules |
| [`standards.md`](standards.md) | External standards and adoption decisions |
| [`runbooks/core-runtime-smoke.md`](runbooks/core-runtime-smoke.md) | Experimental offline core smoke and milestone gate commands |
| [`runbooks/telegram-mtproto.md`](runbooks/telegram-mtproto.md) | Experimental Telegram storage, auth, recovery, live smoke, and upgrade operations |

`AGENTS.md` is the executable workflow index. It does not override domain requirements in this suite.

## Precedence

When requirements conflict:

1. `product.md` controls product scope and approved service qualities.
2. The domain-specific document in the table controls its named domain.
3. `architecture.md` controls dependency and ownership boundaries shared by domains.
4. `quality.md` controls required verification evidence.
5. `standards.md` is subordinate to explicit Ex-Otogi requirements; an external standard does not silently add behavior.

A conflict discovered between documents is a specification defect. Implementers MUST stop the affected change and amend the authoritative documents rather than choose a convenient interpretation.

## Change protocol

A normative change MUST include:

1. the problem and evidence;
2. the affected requirement and stakeholders;
3. alternatives considered;
4. the selected behavior, failure semantics, and migration impact;
5. verification and rollback criteria;
6. updates to every conflicting or dependent document in the same change.

Public contracts and persisted schemas require an explicit compatibility decision. Compatibility is not assumed. Generated artifacts MUST be derived from an approved source contract and MUST NOT become the only specification.

## Specification quality gate

A change to this suite is complete only when:

- every normative statement is observable or linked to verification evidence;
- terminology and lifecycle states remain consistent across documents;
- all relative links resolve;
- no placeholder or unresolved decision remains;
- deferred areas remain deferred unless the change explicitly specifies them;
- rationale is separated from requirements;
- obsolete rules are removed rather than retained as aliases.
