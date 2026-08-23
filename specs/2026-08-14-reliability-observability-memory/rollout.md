# Rollout and rollback

## Scope boundaries

The management SQLite database contains disposable observability data only:
events, artifacts, and snapshots. It is not an application-data store. Its
schema, payload JSON, event kinds, and management API are version-local.

Semantic memory is not disposable telemetry. This release intentionally starts
a new memory generation, but the old JSON file must be archived rather than
deleted or overwritten.

## Configuration migration

1. Replace the bot module sections `llmchat`, `memory`, and `semanticstore` with
   one `modules.llmruntime.config_file` entry.
2. Configure only `extraction_provider`, `extraction_model`,
   `embedding_provider`, and `database_file` under the LLM file's top-level
   `memory` object.
3. Replace each agent's `semantic_memory` object with `memory_enabled: true` or
   omit the flag to disable retrieval for that agent.
4. Rename OpenAI `max_retries` to `embedding_max_retries` if embedding SDK
   retries are desired. Chat generation has a fixed effective budget of three
   application attempts and zero SDK retries.
5. Validate configuration in staging. Removed or ambiguous fields fail strict
   parsing instead of being silently ignored.

`natural_memory`, `semantic_memory`, JSON persistence fields, and mechanism
knobs are not accepted. Strict parsing reports them as unknown fields.

## Memory store cutover

Stop the bot before archiving the old snapshot. With the previous default and
the new default paths:

~~~sh
mkdir -p data/memory-archive
mv data/llm_memory.json data/memory-archive/llm_memory.pre-sqlite.json
~~~

Do not run this command when the source file has a different configured path;
archive that exact file instead. Do not create `data/memory.db` by copying or
renaming the JSON file. Start the new bot after configuration is updated. It
creates a fresh SQLite schema and binds it to the configured embedding provider
profile, model, and dimensions.

The archived JSON is offline rollback data only. The current runtime has no
importer, converter, old-schema reader, or dual-write path. Changing embedding
model, provider profile, or dimensions also requires archiving the entire
SQLite generation (`memory.db`, `memory.db-wal`, and `memory.db-shm` when they
exist) while the bot is stopped, then starting a new database.

## Observability database reset

For this incompatible observability release, start with a fresh management
database. Stop the bot before moving SQLite files so the database and its WAL
state remain consistent. With the default path:

```sh
mkdir -p data/observability-archive
mv data/management.db data/observability-archive/management.pre-refactor.db
test ! -e data/management.db-wal || mv data/management.db-wal data/observability-archive/management.pre-refactor.db-wal
test ! -e data/management.db-shm || mv data/management.db-shm data/observability-archive/management.pre-refactor.db-shm
```

Start the new bot. It creates the current schema and begins a new telemetry
window. The archived database is for offline inspection only and must not be
reintroduced into the running service. If historical telemetry is not needed,
the operator may delete the archive later according to local retention policy.

## Deployment verification

- Open Events and confirm the first request displays the newest page.
- Confirm polling uses `after_id` and Load older uses `before_id`.
- Trigger one successful chat and confirm accepted, state-transition, provider,
  and terminal events share one trace.
- Trigger or simulate one classified failure and confirm the user message has a
  short reference and the terminal event carries the same failure class.
- Run the deterministic memory evaluation and compare it with the checked-in
  baseline.
- Confirm `memory.retrieval.degraded` appears when the planner is made to fail
  and that useful heuristic retrieval still completes.

## Rollback

Roll back the application and configuration together. Use a fresh management
database for the rollback version as well; observability databases are not
forward- or backward-compatible artifacts. Stop the bot and restore the
archived JSON memory generation only with the pre-cutover application. Never
point either application generation at the other generation's memory store.

The source rollback does not require restoring old telemetry. This keeps the
operational recovery path independent of management schema evolution.
