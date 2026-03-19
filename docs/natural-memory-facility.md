# Natural Memory Facility

## Goal

Natural memory replaces tool-driven `remember` / `recall` / `forget` behavior
with a staged pipeline:

1. Normalize conversation activity into self-contained memory units.
2. Synthesize related units into stronger canonical memories at write time.
3. Retrieve memories adaptively inside `llmchat`, then reinforce what was used.

The storage boundary stays conversation-scoped:

- `tenant_id`
- `platform`
- `conversation_id`

This keeps the system architecture simple while still making memories durable,
actor-aware, and useful across later turns.

## Modules Involved

```text
IM platform
  -> driver publishes article.created
  -> kernel event bus
     -> eventcache
     -> naturalmemory
     -> llmchat
     -> llmmemory
```

- `eventcache` is the raw article-history layer.
- `naturalmemory` observes all non-empty `article.created` events, including bot
  output.
- `llmmemory` stores typed semantic records and supports semantic search.
- `llmchat` retrieves semantic memories automatically and injects them into the
  model request.

Because event delivery is asynchronous, memories extracted from message `M` are
most reliably available to `llmchat` on later messages rather than guaranteed
for the same turn.

## Record Model

Semantic memories are stored through `ai.LLMMemoryService`.

Each record contains:

- `Scope`
- `Content`
- `Category`
- `Embedding`
- `Profile`
- optional `Metadata`
- `CreatedAt`
- `UpdatedAt`

The typed profile carries the lifecycle state and provenance:

- `Kind`: `unit` or `synthesized`
- `Importance`
- `LastAccessedAt`
- `AccessCount`
- `Source`
- `SourceArticleID`
- `SourceActor`
- `SubjectActor`
- `EvidenceRecordIDs`

`Metadata` remains as compatibility and extension space, but the primary memory
mechanics now rely on typed profile fields rather than ad-hoc string keys.

## Stage 1: Normalize Memories

`naturalmemory` subscribes to `article.created` and builds an extraction window
from:

- `eventcache.ListConversationContextBefore(...)`
- the current article

The extractor prompt includes:

- an anchor timestamp
- participant identities
- recent semantic memories in the same scope
- rules for self-contained output

The extraction model is expected to emit memory units that:

- resolve actors explicitly
- avoid pronouns when the referent can be named
- convert relative time into absolute time when possible
- stay meaningful outside the original chat turn

Each extracted candidate includes:

- `content`
- `category`
- `importance`
- optional `subject_actor_id`
- optional `subject_actor_name`

Newly stored records enter as `Profile.Kind = unit`.

## Stage 2: Synthesize On Write

Extraction is not the final write step.

For each candidate unit, `naturalmemory`:

1. Embeds the candidate as a document vector.
2. Searches nearby memories in the same scope.
3. Calls a synthesis decision prompt over the candidate plus top matches.
4. Applies one of three outcomes:
   - `add`
   - `rewrite`
   - `noop`

`rewrite` is the main canonicalization path.

When a candidate rewrites an existing memory, the kept record is updated
through the full-record `LLMMemoryService.Update(...)` path:

- `Content` becomes the canonical rewritten memory.
- `Category` can be adjusted by the synthesis decision.
- `Embedding` is refreshed from the rewritten content.
- `Profile.Kind` is promoted to `synthesized`.
- provenance and evidence IDs are merged.
- importance is preserved at the stronger level.

This is how the system avoids accumulating many parallel variants of the same
fact.

## Stage 3: Reflective Maintenance

Background consolidation runs on scopes that recently received activity.

The ordered cycle is:

1. Prune low-value memories using importance and decay-adjusted score.
2. Merge leftover near-duplicates.
3. Generate a small number of reflection memories from the strongest survivors.
4. Cap the scope to `max_memories_per_scope`.

Reflections are stored as:

- `Profile.Kind = synthesized`
- `Category = reflection`

They are not a separate storage tier. They are synthesized memories produced
during consolidation.

## Retrieval Flow In `llmchat`

`llmchat` no longer uses raw vector-search order directly.

When an agent has semantic memory enabled, retrieval works like this:

1. Build search intents.
2. Search semantic memory for each intent.
3. Union matches by record ID.
4. Re-rank by a composite score.
5. Trim injected context to the rune budget.
6. Reinforce selected memories.
7. Inject the final XML block as a system message.

### Retrieval Planning

If retrieval planning is enabled, `llmchat` calls the configured natural-memory
 extraction provider/model to produce up to three short search intents.

If planning is unavailable or fails, `llmchat` falls back to heuristic queries:

- current prompt
- reply-root summary + current prompt when the turn looks like a follow-up

### Re-Ranking

Final ranking uses more than similarity:

- vector similarity
- importance
- recency decay
- memory kind weight
- actor relevance

Current weights favor:

- `synthesized` over `unit`
- memories about the current actor
- memories tied to actors present in the active reply chain

### Reinforcement

Every memory actually injected into the request is reinforced through
`LLMMemoryService.Update(...)`:

- `LastAccessedAt` becomes now
- `AccessCount` increments

This closes the loop between retrieval and long-term retention.

### Injected Context Shape

Injected memories are serialized as:

```xml
<semantic_memories count="N">
  <memory id="..." category="..." kind="..." importance="..." subject_actor="..." source_actor="...">
    memory content
  </memory>
</semantic_memories>
```

The serializer keeps the payload within the configured rune budget and will
truncate memory content when necessary.

## Configuration

Top-level `natural_memory` in `llm.json` now controls both formation and shared
retrieval behavior.

Key knobs:

- `enabled`
- `extraction_provider`
- `extraction_model`
- `embedding_provider`
- `extraction_timeout`
- `extraction_max_input_runes`
- `context_window_size`
- `synthesis_match_limit`
- `consolidation_interval`
- `consolidation_provider`
- `consolidation_model`
- `consolidation_timeout`
- `max_memories_per_scope`
- `decay_factor`
- `min_importance`
- `duplicate_similarity_threshold`
- `reflection_min_source_memories`
- `reflection_source_limit`
- `reflection_max_generated`
- `retrieval_planning_enabled`
- `retrieval_planning_timeout`

`llmchat` reads the natural-memory retrieval settings from the same config so
retrieval planning and recency weighting stay aligned with the write pipeline.

## What Was Removed

The old tool-based memory path is gone:

- `remember`
- `recall`
- `forget`

Memory formation and retrieval are automatic now.

## Operational Notes

- Memory storage is conversation-scoped, not global across all chats.
- The system is designed to degrade gracefully:
  - extraction failure skips memory formation for that event
  - synthesis failure falls back to heuristic add/update behavior
  - reflection failure does not block prune/merge/cap
  - retrieval planning failure falls back to heuristic queries
- Persistence remains handled by `modules/llmmemory`, including migration from
  older records that only carried string metadata.

## Summary

The implemented architecture is intentionally centered on a staged memory
pipeline:

- normalize useful facts from conversation activity
- synthesize related facts into canonical memories on write
- retrieve adaptively and reinforce the memories that helped answer later turns

That is the core design shift inspired by modern memory systems such as Mem0
and SimpleMem, adapted to Ex-Otogi's existing event-driven architecture.
