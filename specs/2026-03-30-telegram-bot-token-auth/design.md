# Design: Telegram Bot Token Authentication

## Overview

The Telegram driver currently only supports **userbot authentication** (phone + code + optional 2FA password) via gotd's `auth.Flow`. This change adds **Telegram Bot Token** authentication as an alternative login method, using gotd's `client.Auth().Bot(ctx, token)` API.

## Current State

- `runtimeConfig` / `parsedRuntimeConfig` in `internal/driver/telegram/runtime.go` hold userbot credentials: `phone`, `code`, `password`
- `authenticateGotdClient()` checks `client.Auth().Status()` first; if not authorized, runs the userbot flow via `auth.NewFlow(authenticator, ...)`
- `parseRuntimeConfig()` requires `app_id` and `app_hash` but does not validate auth credential completeness

## Proposed Changes

### 1. Config Extension

Add `bot_token` field to `runtimeConfig` JSON struct and `botToken` to `parsedRuntimeConfig`.

```json
{
  "app_id": 12345,
  "app_hash": "abc123",
  "bot_token": "123456:ABC-DEF..."
}
```

### 2. Auth Mode Selection

The auth mode is determined by which credentials are provided:

| `bot_token` | `phone` | Auth mode |
|:-----------:|:-------:|:----------|
| set         | empty   | Bot token |
| empty       | set     | Userbot   |
| set         | set     | **Error** (ambiguous) |
| empty       | empty   | **Error** (no credentials) |

Validation happens in `parseRuntimeConfig()` after all fields are parsed.

### 3. Authentication Logic

In `authenticateGotdClient()`:
1. Check `client.Auth().Status()` — if already authorized, return (unchanged)
2. If `botToken` is non-empty: call `client.Auth().Bot(ctx, botToken)`
3. Otherwise: run existing userbot `auth.NewFlow` (unchanged)

### 4. gotd API

Bot auth uses `client.Auth().Bot(ctx, token)` which is already available in the gotd/td v0.139.0 dependency:
```go
func (c *Client) Bot(ctx context.Context, token string) (*tg.AuthAuthorization, error)
```

No new dependencies are required.

### 5. Scope Boundaries

- `app_id` and `app_hash` remain required for both modes (gotd client constructor needs them)
- The `GotdUserbotSource` and `gotdAuthenticatedClient` types are reused without renaming — the "authenticated client" abstraction is auth-mode-agnostic
- Update mapper, decoder, driver, outbound dispatcher — **no changes needed** (Telegram Bot API updates flow through the same gotd channels)
- Session file remains applicable for both modes (gotd persists bot sessions too)
