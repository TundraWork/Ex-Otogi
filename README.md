# Ex-Otogi

Ex-Otogi is a high-concurrency, event-driven Go framework built around the platform-neutral `otogi` protocol for IM events and modules.
The runtime is designed to support multiple messaging platforms through drivers (Telegram is the current built-in implementation).

The project name **Ex-Otogi** is inspired by "Ex-おとぎ話 (Ex-Otogibanashi)", the opening song of "超かぐや姫！ (Cosmic Princess Kaguya!)".

## Architecture

- `pkg/otogi`: "Otogi protocol", stable contracts, events, and framework interfaces.
- `internal/kernel`: runtime orchestration, lifecycle, and event dispatch.
- `internal/driver`: platform adapters.
- `modules/memory`: memory module for article projection and event history.
- `pkg/llm`: LLM contracts and provider implementations.

Dependency direction: `pkg/otogi -> internal/kernel -> internal/driver`.

## Quick Start

1. Copy the example config:
   ```sh
   cp config/bot.example.json config/bot.json
   ```
2. Edit `config/bot.json` and configure at least one driver in `drivers[]`:
   - `drivers[0].name` (driver instance id)
   - `drivers[0].type` (currently `telegram`)
   - `drivers[0].config.app_id`
   - `drivers[0].config.app_hash`
3. Configure user account auth in the same driver config:
   - `drivers[0].config.phone`
   - optionally `drivers[0].config.code` (or enter code interactively)
4. Configure routing defaults in `routing.default`:
   - `routing.default.sources`
   - `routing.default.sink`
5. Optional: enable LLM module by setting `llm.config_file` in `config/bot.json`:
   - copy `config/llm.example.json` to `config/llm.json`
   - define provider profiles under `providers` (`type: "openai"` and `type: "gemini"` are supported)
   - set `providers.<profile>.api_key` and reference the profile from each `agents[].provider`
   - OpenAI profile options live under `providers.<profile>.openai`
     (`organization`, `project`, `max_retries`)
   - Gemini profile defaults live under `providers.<profile>.gemini`
     (`api_version`, `google_search`, `url_context`, `thinking_budget`, `include_thoughts`, `thinking_level`, `response_mime_type`)
   - per-agent provider tuning is passed through `agents[].request_metadata`
     (for Gemini use keys like `gemini.google_search`, `gemini.thinking_level`, `gemini.response_mime_type`)
   - set `llm.config_file` to `config/llm.json`
   - alternatively set `OTOGI_LLM_CONFIG_FILE` to override the path
6. Run:
   ```sh
   go run ./cmd/bot
   ```

## Development

- `make fmt`: format code.
- `make lint`: static checks + architecture checks.
- `make test`: run `go test -race ./...`.
- `make generate`: regenerate mocks/generated code.
- `make dev`: run with `air` hot reload if available.
