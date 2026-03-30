# Execution Plan: Telegram Bot Token Authentication

## Milestone 1: Extend Config Parsing
**Files:** `internal/driver/telegram/runtime.go`
**Success criterion:** `parseRuntimeConfig()` accepts `bot_token` field and validates auth mode (bot token vs userbot vs ambiguous vs missing).

Steps:
1. Add `BotToken string` field to `runtimeConfig` struct with JSON tag `"bot_token"`
2. Add `botToken string` field to `parsedRuntimeConfig` struct
3. In `parseRuntimeConfig()`, parse and trim `bot_token` into `cfg.botToken`
4. After all fields are parsed, add auth-mode validation:
   - If both `botToken` and `phone` are set: return error "bot_token and phone are mutually exclusive"
   - If neither `botToken` nor `phone` is set: return error "either bot_token or phone is required"
5. Keep `app_id` and `app_hash` validation unchanged (both always required)

## Milestone 2: Update Authentication Logic
**Files:** `internal/driver/telegram/runtime.go`
**Success criterion:** `authenticateGotdClient()` uses bot token auth when `botToken` is configured, falls back to existing userbot flow otherwise.

Steps:
1. In `authenticateGotdClient()`, after the existing authorized-status check, add a branch:
   - If `cfg.botToken != ""`: call `client.Auth().Bot(authCtx, cfg.botToken)` and log success
   - Else: run existing userbot flow (unchanged)
2. Log message for bot auth: `"telegram authorized with bot token"`

## Milestone 3: Add Tests
**Files:** `internal/driver/telegram/runtime_test.go`
**Success criterion:** Table-driven tests cover all config parsing scenarios and `go test -race` passes.

Steps:
1. Add table-driven test `TestParseRuntimeConfigAuthMode` covering:
   - Bot token only (valid)
   - Phone only (valid, existing behavior)
   - Both bot token and phone (error: mutually exclusive)
   - Neither bot token nor phone (error: missing credentials)
   - Bot token with whitespace trimming
2. Verify existing tests still pass unmodified

## Milestone 4: Update Example Config
**Files:** `config/bot.example.json`
**Success criterion:** Example config shows `bot_token` field with documentation comment.

Steps:
1. Add `"bot_token": ""` field to the Telegram driver config in `bot.example.json`
2. Remove `"code"` field (or keep empty) — it's only relevant for userbot mode

## Milestone 5: Quality Gate
**Success criterion:** `make quality` passes cleanly.

Steps:
1. Run `make fmt` to ensure formatting
2. Run `make quality` to verify all checks pass
