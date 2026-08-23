# Milestone 3: operation-oriented observability results

## Operator timeline contract

The default timeline now excludes debug events. Diagnostic starts and internal
algorithm detail remain queryable with `level=debug`; the unfiltered operator
view contains operation outcomes.

Ownership is singular:

| Operation | Owner | Default-visible records |
|---|---|---|
| Chat request | `modules/llmchat` | one accepted root and one terminal outcome |
| LLM provider call | provider adapter | one terminal outcome per application attempt |
| Tool call | `modules/llmchat` | one terminal outcome |
| Memory retrieval | `modules/memory` | one terminal aggregate |
| Memory formation | `modules/memory` | one terminal aggregate, plus warnings for partial degradation |
| Semantic persistence/search | `modules/semanticstore` | none; storage is subordinate to memory operations |
| Async runtime failure | kernel composition root | one error outcome |

Routine inbound events, event-bus publishes, semantic-store methods, queueing,
flush steps, and extraction/retrieval algorithm steps no longer produce
operator events.

## Causality and payloads

- `chat.request.accepted` is the root operation event. Its ID becomes the parent
  for provider, memory, tool, and chat terminal records.
- Every accepted chat request records exactly one of `completed`, `failed`, or
  `canceled`.
- Provider terminal payloads include duration, actual application-level attempt,
  and a coarse failure class. Rich provider-independent failure classification
  remains part of the explicit chat state-machine milestone.
- Memory formation reports window reason, article/input counts, buffer time,
  extracted/applied/failed counts, duration, and outcome in one record.
- Memory retrieval reports query/candidate/result counts, planner use, duration,
  and outcome in one record.

## Retention policy

Provider adapters and the inbound dispatcher no longer automatically persist
full prompts, responses, embedding inputs, or raw platform events. The artifact
API remains available for a future explicit, bounded retention policy. Event
descriptions use metadata and counts rather than conversation content.

## Default event-volume evidence

The counts below follow the operation ownership table and exclude debug starts:

| Scenario | Default events | Budget |
|---|---:|---:|
| Simple chat, memory disabled | accepted + provider completed + chat completed = 3 | <= 12 |
| Simple chat, memory retrieval enabled without planner | accepted + retrieval completed + provider completed + chat completed = 4 | <= 12 |
| Simple chat, memory retrieval with planner | accepted + planner provider completed + retrieval completed + answer provider completed + chat completed = 5 | <= 12 |
| Successful memory formation window | extraction provider completed + formation completed = 2 | <= 8 |
| Formation with candidate embedding | extraction provider completed + formation completed = 2; embedding detail remains debug | <= 8 |

Retries add one terminal provider outcome per actual attempt, making degradation
visible without restoring per-chunk or per-step noise.

## Verification

- lifecycle contract tests prove accepted/terminal cardinality, shared trace, and
  terminal-parent linkage;
- provider tests prove metadata-only descriptions and no implicit artifacts;
- memory tests prove aggregate terminal records and bounded degradation warnings;
- query tests prove debug exclusion by default and explicit debug retrieval.
