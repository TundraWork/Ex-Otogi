# Ex-Otogi

Ex-Otogi is a high-concurrency, event-driven Go framework built around the platform-neutral `otogi` protocol for IM events and modules.
The runtime is designed to support multiple messaging platforms through drivers (Telegram is the current built-in implementation).

The project name **Ex-Otogi** is inspired by "Ex-おとぎ話 (Ex-Otogibanashi)", the opening song of "超かぐや姫！ (Cosmic Princess Kaguya!)".

## Architecture

- `pkg/otogi`: stable contracts, events, and framework interfaces.
- `internal/kernel`: runtime orchestration, lifecycle, and event dispatch.
- `internal/driver`: platform adapters (Telegram).
- `modules/demo`: reference module implementation.

Dependency direction: `internal/driver -> internal/kernel -> pkg/otogi`.

## Quick Start

1. Copy the example config:
   ```sh
   cp config/bot.example.json config/bot.json
   ```
2. Set Telegram API credentials required by `github.com/gotd/td/telegram.ClientFromEnvironment`.
3. Configure one auth path:
   - bot: set `OTOGI_TELEGRAM_BOT_TOKEN`
   - user: set `OTOGI_TELEGRAM_PHONE` and provide `OTOGI_TELEGRAM_CODE` (or enter code interactively)
4. Run:
   ```sh
   go run ./cmd/bot
   ```

## Development

- `make lint`: static checks + architecture checks.
- `make test`: run `go test -race ./...`.
- `make generate`: regenerate mocks/generated code.
- `make dev`: run with `air` hot reload if available.
