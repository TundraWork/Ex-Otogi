# Reliability, Observability, and Memory Refactor Architecture

Status: accepted for incremental implementation

## Context

Ex-Otogi has three coupled reliability problems:

1. long-term memory formation, semantic storage, and retrieval evolved as adjacent mechanisms with duplicated configuration and no repeatable quality or cost evaluation;
2. diagnostic logs and operator-visible lifecycle events are mixed, while the management timeline can only page from the oldest retained event toward the newest;
3. LLM chat is implemented as a collection of streaming, retry, tool, placeholder, and delivery helpers rather than one explicit request lifecycle, so failures collapse into a generic user response.

The `feat-webui` line already provides useful foundations: a SQLite management store, a management API and Vue control panel, provider event recording, a consolidated `memory` module, a `semanticstore` module, and bounded chat retries. This refactor adopts those foundations and changes their contracts where system behavior is wrong.

## Architectural principles

- Optimize system boundaries and observable outcomes before local abstractions.
- Keep one owner for every policy. Storage, formation, retrieval, chat orchestration, provider transport, and presentation each have distinct owners.
- Treat operator events as a stable product contract. Logs remain ephemeral diagnostics.
- Measure memory quality, latency, and provider cost together. A more elaborate mechanism is not an improvement unless the evaluation shows it.
- Prefer removal and consolidation after measurement over adding another compatibility layer.
- Preserve `pkg/otogi -> internal/kernel -> internal/driver`; modules depend on public contracts and services, not driver implementations.

## Target system

### Memory capability

`modules/memory` owns the complete application policy for long-term memory:

```text
article events
    -> formation window
    -> extraction decisions
    -> semantic store

chat prompt
    -> semantic retriever
    -> ranked context document
    -> llmchat
```

Responsibilities are divided as follows:

- `pkg/otogi/ai` defines small storage and retrieval contracts. It contains no formation or ranking implementation.
- `modules/semanticstore` owns transactional durable semantic records and similarity lookup. It has no LLM prompts and no chat policy.
- `modules/memory` owns formation windows, extraction, retrieval planning, ranking, and explicit expiry.
- `modules/llmchat` asks a semantic retriever for context. It does not know formation configuration, storage metadata, decay formulas, or embedding providers.

There is one optional top-level `memory` configuration section. Its presence
configures the extraction model, embedding provider, and SQLite database path;
algorithm constants are implementation policy verified by the evaluator, not
operator tuning. Provider profiles remain shared infrastructure. An agent has
one `memory_enabled` capability switch. Legacy `natural_memory`, the
`semantic_memory` agent object, mechanism-level knobs, and old module sections
are rejected by strict parsing.

The semantic store uses a current-only SQLite schema and commits each mutation
transactionally. The database metadata binds all vectors to one embedding
fingerprint: provider profile, model, and dimensions. Startup fails when the
schema or fingerprint differs; records from incompatible embedding spaces are
never skipped or compared silently. There is no runtime JSON import, schema
migration, or dual-write path.

Memory records have one typed representation. Importance, provenance, actors,
evidence, expiry, keywords, tags, and links are not duplicated into a generic
legacy metadata map. Retrieval does not mutate access counters and ranking does
not reward prior retrievals. Durable facts do not decay merely because time
passes; removal is caused by an explicit extraction decision or an explicit
`valid_until` boundary. This avoids a self-reinforcing popularity loop and
unreviewable implicit data loss.

Before further algorithmic simplification, the current implementation is frozen as the evaluation baseline. Candidate removal order is:

1. retrieval-planning LLM call, if heuristic or direct hybrid retrieval meets the quality threshold;
2. LLM-driven consolidation, if deterministic deduplication and bounded retention meet formation quality;
3. access-count reinforcement, if it harms freshness or offers no retrieval gain;
4. redundant per-agent knobs that do not produce a meaningful evaluation difference.

### Observability

Observability has two separate outputs:

- diagnostic logs: high-cardinality implementation detail for developers, controlled by log level and not guaranteed as an API;
- management operations: bounded, typed lifecycle records for operators and the control panel.

The default timeline records operations, not loop iterations or store method calls. Each important operation uses a stable `kind`, trace identity, component, start time, outcome, duration, and a bounded payload. The initial operation families are:

- `chat.request`: accepted, completed, failed, canceled;
- `llm.call`: started, completed, failed, retried;
- `tool.call`: started, completed, failed;
- `memory.formation`: window flushed, completed, skipped, failed;
- `memory.retrieval`: completed, degraded, failed;
- `memory.maintenance`: completed, failed;
- `runtime.delivery`: failed or materially retried;
- `runtime.async`: dropped, timed out, panicked.

Routine semantic-store searches, every intermediate extraction decision, streaming chunks, and successful message edits are debug diagnostics. They are not default timeline events. Full prompts, responses, and extraction documents are opt-in artifacts with retention limits; they are never placed in subjects or descriptions.

The event query model has three explicit directions:

- latest: no cursor, return the newest matching page;
- newer: `after_id`, return records newer than the visible high-water mark for polling;
- older: `before_id`, return records older than the visible low-water mark for history.

Responses expose both low and high cursors. Initial page and older-page rows are selected by descending index and returned newest-first. Newer polling is selected ascending to advance safely, then merged into newest-first presentation. Cursor direction is part of the API contract, not inferred by the frontend.

### LLM chat lifecycle

One chat request is modeled as a state machine:

```text
accepted -> context_ready -> generating <-> executing_tools -> delivering -> completed
    |             |               |                 |              |
    +-------------+---------------+-----------------+--------------+-> failed/canceled
```

The orchestrator owns state transitions and emits operation events. Provider adapters translate SDK errors into provider-independent failure classes:

- `invalid_request`
- `authentication`
- `rate_limited`
- `provider_unavailable`
- `timeout`
- `canceled`
- `safety_refusal`
- `empty_response`
- `tool_failure`
- `delivery_failure`
- `internal`

Retry policy is configured once at the chat/provider boundary. Only transient failures before visible answer output are retryable. A stream that already delivered answer text is never restarted because doing so can duplicate or contradict content. Tool calls are only replayed when the tool explicitly declares idempotency. Provider SDK retry behavior and application retry behavior must not multiply unknowingly; the effective maximum attempts is reported in configuration validation and management events.

User failure messages are stable by class and include a short correlation identifier. They do not leak provider details. The generic sentence remains only the final `internal` fallback, not the response for every failure.

## Measurement model

### Memory quality and cost

A checked-in, provider-free evaluation corpus contains conversation turns, expected durable memories, prohibited memories, and retrieval queries with relevance grades. Deterministic fake extractors and fixed embeddings make the required gate reproducible. Optional live-provider runs are reports, not merge gates.

The baseline report includes:

- formation precision and recall;
- contradiction/supersession correctness;
- duplicate rate and prohibited-memory rate;
- Recall@5, MRR, and nDCG@5 for retrieval;
- p50/p95 formation and retrieval latency;
- extraction, planning, embedding, and consolidation calls per 100 articles and per chat request;
- stored records and bytes per 1,000 articles.

A simplification is accepted when it does not reduce formation F1 or nDCG@5 by more than 2% relative, does not increase prohibited-memory rate, and either reduces provider calls by at least 20% or p95 latency by at least 15%. These are initial engineering thresholds and will be replaced with production-derived SLOs once representative traces exist.

### Chat reliability

The baseline and regression report includes:

- successful responses / accepted chat requests;
- generic fallback responses / accepted requests;
- failures by class and provider;
- p50/p95 time to first visible answer and total completion time;
- attempts per provider call;
- empty stream, tool failure, timeout, and delivery failure rates.

The rollout target is zero generic fallbacks for classified provider, timeout, tool, and delivery failures, plus a measurable reduction in transient generation failures after bounded retry.

### Observability volume and responsiveness

Measure:

- timeline events per successful chat request and per memory window;
- database bytes per 1,000 operations;
- p95 latest/newer/older query time with 100,000 retained events;
- initial frontend request count, transferred bytes, and time to render the newest page.

Initial targets are at most 12 default timeline events per chat request without tools, at most 8 per completed memory window, p95 query time below 100 ms on the reference development machine, and one request to display the latest 50 events. Debug events and artifacts are measured separately.

## Compatibility and rollout

Each milestone ships as a vertical slice and leaves the repository runnable:

1. establish this architecture and baseline;
2. introduce bidirectional event pagination and update the frontend;
3. reduce event volume while adding missing operation outcomes;
4. add the memory evaluation corpus and baseline report;
5. simplify memory policy only where the report supports it;
6. introduce the chat state machine and typed failures;
7. remove deprecated configuration after a migration window and publish before/after results.

Management telemetry is disposable and version-local. Event, artifact, snapshot,
payload, and management API compatibility is not preserved across incompatible
observability releases. Operators deploy such releases with a fresh
`management.db`; retaining or exporting the old file is optional and is not a
runtime migration requirement. This freedom applies only to observability data.
Memory persistence and user/conversation data are not disposable, but this
release intentionally provides no runtime compatibility. Operators stop the
bot, archive the old JSON snapshot without modifying it, and start a fresh
current-schema memory database. Rollback restores the matching application,
configuration, and archived memory generation together.

## Rejected approaches

- Refactoring memory algorithms before an evaluation corpus: there is no defensible way to choose between mechanisms.
- Treating structured logs as the management API: retention, schema stability, correlation, and pagination requirements differ.
- Adding retries at every layer: compounded retries increase latency and load and can replay non-idempotent work.
- Keeping both `naturalmemory` and `memory` as first-class systems: it preserves the ownership ambiguity that caused the configuration problem.
- Loading all retained events into the browser: it turns retention size into startup latency and memory usage.

## Milestone verification

Architecture verification is evidence-based: package dependency checks, API contract tests at cursor boundaries, the evaluation report, operation-volume assertions, failure-state scenarios, race/leak checks for lifecycle changes, and the repository quality gates. Test count or branch coverage is not itself an architectural goal.
