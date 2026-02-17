# Ex-Otogi

Ex-Otogi is a high-concurrency, event-driven Go framework built around the platform-neutral `otogi` protocol for IM events and modules.
The runtime is designed to support multiple messaging platforms through drivers (Telegram is the current built-in implementation).

The project name **Ex-Otogi** is inspired by "Ex-おとぎ話 (Ex-Otogibanashi)", the opening song of "超かぐや姫！ (Cosmic Princess Kaguya!)".

## Architecture

- `pkg/otogi`: stable contracts, events, and framework interfaces.
- `internal/kernel`: runtime orchestration, lifecycle, and event dispatch.
- `internal/driver`: platform adapters (Telegram).
- `modules/memory`: memory module for article projection and event history.

Dependency direction: `pkg/otogi -> internal/kernel -> internal/driver`.

## Quick Start

1. Copy the example config:
   ```sh
   cp config/bot.example.json config/bot.json
   ```
2. Edit `config/bot.json` and set Telegram API credentials:
   - `telegram.app_id`
   - `telegram.app_hash`
3. Configure one auth path in the same file:
   - bot: set `telegram.bot_token`
   - user: set `telegram.phone` and optionally `telegram.code` (or enter code interactively)
4. Run:
   ```sh
   go run ./cmd/bot
   ```

## Development

- `make lint`: static checks + architecture checks.
- `make test`: run `go test -race ./...`.
- `make generate`: regenerate mocks/generated code.
- `make dev`: run with `air` hot reload if available.
