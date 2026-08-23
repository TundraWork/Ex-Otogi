# Milestone 5: Memory Simplification

## Accepted architecture changes

The runtime dependency cycle was removed by introducing `modules/llmruntime`
as the owner of shared LLM configuration and provider construction. Registration
is now ordered as:

```text
llmruntime -> semanticstore -> memory -> llmchat
```

This fixes a production wiring defect: `llmchat` previously registered before
`memory`, resolved no `SemanticRetriever`, and silently continued without
semantic retrieval. Configured retrieval is now a required startup dependency.

Memory ownership is now explicit:

- `llmchat` consumes only the semantic retriever and sends scope, prompt,
  actors, reply-root context, and an optional rune budget;
- `memory` selects the embedding provider and owns query planning,
  similarity threshold, result count, ranking, reinforcement, and retention;
- `semanticstore` owns persistence and similarity lookup and no longer has an
  independent per-scope eviction policy;
- `llmruntime` loads the shared file once and publishes immutable config and
  provider registries.

## Configuration consolidation

The canonical LLM file now has one top-level `memory` object containing
formation, retrieval, retention, and persistence settings. Agent-level semantic
memory contains only `enabled` and `max_memory_runes`.

Removed configuration:

- per-agent `embedding_provider`, `max_retrieved_memories`, and
  `min_memory_similarity`;
- semanticstore `max_entries`, whose hidden eviction competed with memory
  retention;
- `duplicate_similarity_threshold`, which repository analysis proved was
  parsed and logged but never used by an algorithm;
- duplicated `modules.llmchat` and `modules.memory` config-file references.

The bot now uses one `modules.llmruntime.config_file` entry. Stale module
sections fail validation rather than being ignored. `natural_memory` remains a
temporary one-way alias; using it together with `memory` is an error. Agents
cannot enable retrieval while global memory is disabled.

## Evaluation decision

The accepted boundary/configuration candidate retained:

- formation F1: `1.0000`;
- supersession correctness: `1.0000`;
- prohibited-memory rate: `0.0000`;
- Recall@5, MRR, and nDCG@5: `1.0000`;
- identical deterministic provider-call counts.

Architecture/configuration changes use the semantic non-regression gate because
they do not claim an inference-cost reduction. Algorithm-removal candidates
additionally require a 20% deterministic provider-call reduction.

The retrieval-planner removal candidate reduced provider calls by 20%, but
nDCG@5 fell from `1.0000` to approximately `0.9385` (6.15% relative). It was
rejected because this exceeds the 2% quality threshold. No unverified algorithm
removal was accepted.

## Verification

- canonical and legacy memory parsing, dual-key rejection, and removed-knob
  rejection are covered;
- provider-runtime, store, memory, chat, and application registration suites
  pass;
- `go test ./...` passes;
- the deterministic semantic non-regression gate passes;
- the planner-disabled mechanism gate rejects the candidate as expected.
