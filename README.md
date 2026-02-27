# Ex-Otogi

Ex-Otogi is an event-driven, high-concurrency Go framework for IM workflows. It uses platform-neutral `otogi` contracts and separates core orchestration from platform drivers.

The project name is inspired by "Ex-おとぎ話 (Ex-Otogibanashi)", the opening song of "超かぐや姫！ (Cosmic Princess Kaguya!)".

## Architecture

- `pkg/otogi`: stable public contracts, events, and interfaces.
- `internal/kernel`: runtime lifecycle, orchestration, and dispatch.
- `internal/driver`: platform adapters (Telegram implementation included).
- `modules/*`: optional modules (for example, memory and LLM chat).
- `pkg/llm`: LLM provider contracts and implementations.

Dependency direction: `pkg/otogi -> internal/kernel -> internal/driver`.

## Quick Start

1. Create bot configuration:
   ```sh
   cp config/bot.example.json config/bot.json
   ```
2. Update `config/bot.json`:
   - configure at least one entry in `drivers[]` (`type: "telegram"` is currently supported)
   - set Telegram credentials in `drivers[].config`
   - set routing defaults in `routing.default`
3. Optional LLM setup:
   ```sh
   cp config/llm.example.json config/llm.json
   ```
   - configure provider profiles in `config/llm.json`
   - set `llm.config_file` in `config/bot.json` or `OTOGI_LLM_CONFIG_FILE`
4. Run:
   ```sh
   go run ./cmd/bot
   ```

## Timeout Settings

- `kernel.module_lifecycle_timeout`: timeout for module lifecycle hooks.
- `kernel.module_handler_timeout`: default timeout for module event handlers.
- `otogi.SubscriptionSpec.HandlerTimeout`: per-subscription handler timeout override.
- `drivers[].config.publish_timeout`: timeout for outbound driver RPC calls.

## Development

- `make fmt`
- `make lint`
- `make arch-check`
- `make test`
- `make generate`
- `make dev`
