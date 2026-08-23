# Refactor Milestones

## Milestone 1: baseline and governing architecture

Status: **[DONE]**

Success criteria:

- continue from the existing `feat-webui` implementation rather than duplicating it on `master`;
- record clean Go and frontend baseline results or their concrete environment blockers;
- accept the ownership boundaries, measurement model, compatibility policy, and rollout sequence in `architecture.md`.

## Milestone 2: latest-first observability

Status: **[DONE]**

Implement explicit latest/newer/older query modes through contracts, SQLite, HTTP/OpenAPI, generated client, and Vue state. Add the indexes required by the query shape.

Success criteria:

- opening Events displays the newest matching page with one request;
- polling only requests IDs above the current high cursor;
- Load older only requests IDs below the current low cursor;
- filters apply identically in every direction;
- store and HTTP contract tests cover empty stores, boundaries, gaps, and concurrent inserts;
- a 100,000-row local benchmark records p95 query latency.

## Milestone 3: operation-oriented observability

Status: **[DONE]**

Inventory current event kinds against operator questions, define lifecycle pairs, demote internal detail, and fill missing outcomes and durations.

Success criteria:

- every accepted chat request has a terminal correlated event;
- every provider call has outcome, duration, attempt, and failure class;
- memory formation and retrieval expose terminal outcome and cost counts;
- default event-volume fixtures meet the budgets in `architecture.md`;
- prompts and responses appear only as explicitly retained artifacts.

## Milestone 4: memory evaluation baseline

Status: **[DONE]**

Build a deterministic evaluation command around the public formation, store, and retrieval boundaries. Check in representative English and multilingual conversation fixtures, including corrections, preferences, transient statements, sensitive/prohibited content, and irrelevant retrieval candidates.

Success criteria:

- one command produces machine-readable and human-readable reports;
- reports include every memory metric in `architecture.md`;
- the current implementation has a checked-in baseline;
- merge thresholds fail on material regression.

## Milestone 5: memory simplification

Status: **[DONE]**

Execution sequence:

1. extract shared configuration and provider construction from `llmchat` into
   an infrastructure `llmruntime` module;
2. register `llmruntime -> semanticstore -> memory -> llmchat` so service
   dependencies are explicit and semantic retrieval cannot be silently absent;
3. remove embedding-provider selection and detailed retrieval policy from
   `llmchat`; memory owns provider selection, search/ranking policy, and
   global defaults, while an agent retains only enablement and context budget;
4. replace `natural_memory` plus the separate `semanticstore` section with
   one `memory` configuration, reject ambiguous dual configuration, and
   document the migration;
5. rerun the deterministic evaluator and retain only changes that meet the
   checked-in semantic-quality and provider-cost thresholds.

Run candidate removals in the documented order. Keep only changes that pass the acceptance rule. Consolidate configuration under one memory capability and provide explicit migration validation.

Success criteria:

- `llmchat` depends only on the semantic retriever contract;
- memory owns formation and retrieval policy;
- semanticstore owns persistence and similarity lookup only;
- one documented configuration controls memory;
- accepted simplifications meet quality and cost thresholds.

## Milestone 6: chat request state machine

Status: **[DONE]**

Introduce provider-independent failure classification, central retry policy, explicit state transitions, and class-specific user outcomes. Ensure retry and tool replay obey visible-output and idempotency rules.

Execution sequence:

1. define one provider-independent failure value and the accepted lifecycle states;
2. translate OpenAI and Gemini SDK/stream failures at the provider boundary;
3. move retry decisions to generation evidence: retry only before visible answer
   output and before a completed tool-call response;
4. make the request orchestrator own state transitions, delivery failures, terminal
   classification, and correlation-safe user messages;
5. disable hidden provider SDK generation retries so the reported three-attempt
   application budget is the effective budget;
6. publish a compact scenario report covering pre-output retry, partial-output
   failure, timeout, rate limit, safety refusal, empty stream, tool failure, and
   delivery failure.

Success criteria:

- all state transitions and terminal outcomes are observable;
- known failures never use the generic fallback;
- transient pre-output calls retry within one shared attempt budget;
- partial-output streams and non-idempotent tools are not replayed;
- reliability report demonstrates the target behavior.

## Milestone 7: rollout and full verification

Status: **[DONE]**

Run backend and frontend quality gates, race and leak checks, fresh management
database creation, and end-to-end API/frontend scenarios. Publish configuration,
telemetry-reset, rollback, and operational guidance with before/after reports.

Success criteria:

- `make quality` passes;
- frontend install, type check, lint, and build pass;
- architecture dependency flow remains valid;
- fresh management database creation and the telemetry-reset procedure have been exercised;
- before/after reports are checked in.

## Milestone 8: clean-break memory architecture

Status: **[DONE]**

This milestone deliberately replaces the transitional memory configuration and
persistence format. It does not read, translate, dual-write, or silently reuse
the old JSON snapshot. Operators archive the old file and start the current
SQLite store as a new memory generation.

Execution sequence:

1. remove `natural_memory`, per-mechanism tuning, legacy metadata fallbacks, and
   the transitional per-agent `semantic_memory` object from the current
   configuration contract;
2. replace periodic whole-file JSON snapshots with transactional SQLite
   storage, a current-only schema, and a store-level embedding fingerprint
   containing provider profile, model, and vector dimensions;
3. make memory availability a startup invariant: enabled agents require the
   retriever, the retriever requires its store and providers, invalid requests
   fail explicitly, and disabled memory does not publish a pretend-available
   service;
4. remove access-count reinforcement and age-decay pruning so retrieval cannot
   create a popularity feedback loop or erase durable facts implicitly; retain
   explicit expiry and extraction-authored update/delete decisions;
5. keep LLM retrieval planning because its measured removal failed the nDCG
   threshold, but classify planner fallback as an observable degraded retrieval
   rather than a silent success;
6. rerun the deterministic evaluator and focused store/runtime checks, document
   archive-and-reset rollout, then run the complete repository quality gates.

Success criteria:

- only the current `memory` and agent `memory_enabled` keys are accepted;
- memory configuration exposes capability choices, not algorithm internals;
- every committed mutation is durable without a flush goroutine or shutdown
  snapshot, and incompatible schemas or embedding fingerprints fail startup;
- records have one typed representation with no legacy metadata mirror;
- retrieval has no access reinforcement or age-decay score, and maintenance
  deletes only explicitly expired records;
- planner failure produces useful heuristic retrieval plus a
  `memory.retrieval.degraded` operation;
- formation F1 and nDCG@5 remain within the accepted regression thresholds;
- the fresh-store procedure is exercised without deleting the archived source;
- `make quality` and frontend quality gates pass.
