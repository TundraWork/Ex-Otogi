# Long-Term Memory

Ex-Otogi has one long-term memory capability with four explicit owners:

~~~text
llmruntime -> semanticstore -> memory -> llmchat
~~~

- `llmruntime` loads provider and memory configuration once.
- `semanticstore` commits typed records to SQLite and performs scoped vector similarity lookup.
- `memory` owns formation, extraction decisions, retrieval planning, ranking, rendering, and explicit expiry.
- `llmchat` only requests memory context through `SemanticRetriever`.

## Formation and retention

Conversation articles are grouped into bounded windows. A single extraction request produces `new`, `update`, `delete`, or `noop` actions. New and updated contents are embedded in the configured vector space before the actions are committed.

Records do not disappear because they are old or infrequently retrieved. Maintenance removes a record only when its typed `valid_until` deadline has passed. Corrections and contradictions are handled by explicit extraction `update` or `delete` actions.

## Retrieval

For an enabled agent, memory plans a small query set, embeds it, searches within the tenant/platform/conversation scope, and ranks matches using vector similarity, typed importance, actor relevance, and keyword evidence. Retrieval does not mutate records or reinforce previously popular results.

The LLM planner remains because the measured direct-retrieval candidate reduced nDCG@5 beyond the accepted threshold. A planner failure falls back to deterministic queries and records `memory.retrieval.degraded`; store, embedding, and invalid-request failures propagate instead of masquerading as empty memory.

## Configuration

The bot configuration points to one LLM file:

~~~json
{"modules":{"llmruntime":{"config_file":"config/llm.json"}}}
~~~

The LLM file exposes only capability choices:

~~~json
{
  "memory": {
    "extraction_provider": "openai-main",
    "extraction_model": "gpt-4.1-mini",
    "embedding_provider": "openai-main",
    "database_file": "data/memory.db"
  },
  "providers": {
    "openai-main": {
      "type": "openai",
      "api_key": "...",
      "embedding_model": "text-embedding-3-small",
      "embedding_dimensions": 512
    }
  },
  "agents": [{"name": "Otogi", "memory_enabled": true}]
}
~~~

Omit the top-level `memory` object to disable the capability globally. An agent with `memory_enabled: true` requires that object. Mechanism knobs are internal policy and are verified through the deterministic evaluator rather than exposed as operator configuration.

## Persistence identity

The SQLite database stores a current-only schema version and the embedding provider profile, model, and dimensions. Startup fails if any of them differ from configuration. This prevents vectors from incompatible spaces from being compared or silently skipped.

There is no JSON import, old-schema migration, or dual-write path. Archive an old store and start a new database as described in the rollout guide.

## Evaluation

Run `make memory-eval`. The checked-in corpus measures formation F1, supersession, duplicates, prohibited memories, Recall@5, MRR, nDCG@5, latency, and provider-call cost. Architecture changes must meet the thresholds in the accepted architecture document.
