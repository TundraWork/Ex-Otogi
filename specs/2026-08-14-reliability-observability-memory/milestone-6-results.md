# Milestone 6 Results: Chat Reliability

Status: verified locally

## Architecture delivered

- `pkg/otogi/ai` defines the provider-independent failure contract. OpenAI and
  Gemini translate structured SDK, HTTP, stream, and safety outcomes at their
  adapter boundaries.
- The chat orchestrator owns `accepted -> context_ready -> generating <->
  executing_tools -> delivering -> completed|failed|canceled` and records each
  non-terminal transition plus one terminal request event.
- Generation has one effective three-attempt application budget. OpenAI SDK
  generation retries are forced to zero; Gemini has no hidden generation retry
  layer. The former OpenAI profile knob is now explicitly
  `embedding_max_retries` and affects embeddings only.
- Retry is permitted only before answer text has been successfully delivered.
  A failed partially visible stream is retained and recorded rather than
  regenerated or overwritten.
- Tools execute outside generation retry. A transient follow-up failure reuses
  the completed tool result and cannot replay the tool.
- Classified failures use stable user messages with a short trace reference.
  The generic sentence is retained only for an unclassified internal failure.

## Deterministic evidence

| Scenario | Evidence |
| --- | --- |
| transient failure before output | second provider attempt succeeds |
| transient failure after visible output | one provider attempt; partial answer preserved |
| follow-up failure after a tool | one tool execution; only follow-up generation retries |
| timeout, rate limit, safety, empty response, tool and delivery failures | stable typed terminal class |
| known user-facing failures | zero generic fallback messages |
| lifecycle observability | ordered states and typed terminal payload with effective attempt budget |

Machine-readable results are in `chat-reliability-report.json`.

## Verification

Passed before the full repository quality gate:

```text
go test ./pkg/llm/providers/openai ./pkg/llm/providers/gemini ./modules/llmchat
go test ./...
```
