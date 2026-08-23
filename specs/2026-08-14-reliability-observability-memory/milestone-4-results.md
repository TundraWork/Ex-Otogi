# Milestone 4: Memory Evaluation Baseline

## Architecture exercised

The evaluator is implemented inside `modules/memory` so it can invoke the real
formation and retrieval paths without copying their algorithms. A deterministic
LLM provider supplies prescribed extraction and planning outputs, and a
deterministic embedding provider replaces only the network boundary. The CLI
composition root supplies the production `semanticstore` implementation.

Consequently the benchmark executes the production conversation serialization,
existing-memory search, update/delete handling, embeddings, similarity lookup,
ranking, reinforcement, and context rendering. Runtime-generated record IDs used
by corrections are resolved from stable corpus keys immediately before each
formation window.

## Corpus

The version 1 corpus contains five architecture-representative scenarios and
nine articles:

- English durable preferences versus transient events;
- a two-window correction that updates an existing record and removes the
  superseded fact;
- Chinese durable facts and preferences;
- explicit exclusion of a bank PIN while preserving a safe hobby;
- retrieval ranking among unrelated memories.

It includes six retrieval requests with graded relevance judgments. The corpus
is intentionally compact: each scenario distinguishes a system behavior rather
than adding near-duplicate test cases.

## Reproduction

Generate the checked-in planner-enabled baseline:

```sh
make memory-eval
```

Evaluate a candidate and enforce the checked-in thresholds:

```sh
go run ./cmd/memoryeval \
  -planner=false \
  -require-cost-reduction \
  -baseline specs/2026-08-14-reliability-observability-memory/memory-evaluation-baseline.json \
  -json /tmp/memory-candidate.json \
  -markdown /tmp/memory-candidate.md
```

The JSON candidate report embeds its machine-readable comparison before the
command exits unsuccessfully for a rejected candidate. Latency is measured and
reported but is not a merge gate because local wall-clock measurements are not
reproducible. Semantic quality and provider-call counts are deterministic gates.

## Baseline and first candidate

The planner-enabled baseline achieved:

- formation precision, recall, and F1: `1.0000`;
- supersession correctness: `1.0000`;
- duplicate and prohibited-memory rates: `0.0000`;
- Recall@5, MRR, and nDCG@5: `1.0000`;
- one retrieval-planning call and one retrieval embedding call per chat request.

Disabling retrieval planning retained formation quality and Recall@5, eliminated
one provider call per retrieval, but reduced MRR to about `0.9167` and nDCG@5 to
about `0.9385`. The 6.15% relative nDCG regression exceeds the 2% threshold, so
the current direct-retrieval candidate is rejected. Planner removal should only
be reconsidered after improving direct query/ranking behavior.

## Acceptance rule

Every candidate is accepted only when:

- formation F1 and nDCG@5 regress by no more than 2% relative;
- prohibited-memory rate does not increase.

Mechanism-removal candidates additionally pass `-require-cost-reduction` and
must reduce deterministic provider calls by at least 20%. Boundary and
configuration refactors use the semantic non-regression gate because they do not
claim to reduce inference cost.

Measured p50/p95 latency remains evidence in the reports. The architecture's
15% latency criterion can be evaluated in a controlled performance environment,
but is not allowed to make a local merge gate flaky.
