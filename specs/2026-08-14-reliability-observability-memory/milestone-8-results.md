# Milestone 8 Results: Clean-Break Memory Architecture

The transitional memory schema and JSON snapshot runtime were removed. Current configuration has one optional top-level `memory` object containing the extraction provider/model, embedding provider, and SQLite database path. Agents have one `memory_enabled` capability flag. Legacy keys and mechanism-level knobs fail strict parsing.

The runtime semantic store now commits every mutation to SQLite. Store metadata binds the database to schema version 1 and one embedding fingerprint containing the provider profile, model, and dimensions. Reopening with a different fingerprint or writing a vector with the wrong dimensions fails explicitly.

Memory records have one typed representation. Legacy metadata mirroring, access counts, last-access reinforcement, recency decay, importance pruning, and implicit per-scope eviction were removed. Maintenance deletes only records whose explicit `valid_until` time has passed. Retrieval ranks using semantic similarity, typed importance, actor relevance, and keyword evidence without mutating selected records.

LLM retrieval planning remains because its previously measured removal failed the accepted nDCG threshold. Planner failure now uses deterministic queries and emits `memory.retrieval.degraded`; invalid requests and unavailable required dependencies fail instead of becoming quiet empty results.

## Evaluation

The deterministic production-path comparison accepted the clean break:

- formation precision/recall/F1: 1.0 / 1.0 / 1.0;
- supersession correctness: 1.0;
- duplicate and prohibited-memory rates: 0;
- Recall@5, MRR, and nDCG@5: 1.0 / 1.0 / 1.0;
- formation F1 and nDCG@5 regression: 0%;
- deterministic retrieval p95 reduction in the captured run: about 24%.

Focused SQLite verification exercised commit, close/reopen durability, typed round-trip, vector-width rejection, and embedding-fingerprint rejection.

## Final verification

- `make quality` passed formatting, lint, dependency architecture, agent policy, the full race suite, leak checks, and coverage reporting (69.4% total).
- `pnpm install --frozen-lockfile`, API generation, Vue type-check, frontend lint, and the production build passed. Regenerating the management client produced the same SHA-256 hash.
- The checked-in `config/llm.example.json` passed the production strict loader.
- Runtime source contains no removed memory configuration, persistence, metadata, reinforcement, or pruning symbols; strict negative tests retain old keys only to prove rejection.
- `git diff --check` passed and no documentation links target the removed legacy memory guide.

The non-blocking security report continues to identify dependency and Go toolchain advisories. They are independent of this memory architecture change and remain visible for dependency maintenance.
