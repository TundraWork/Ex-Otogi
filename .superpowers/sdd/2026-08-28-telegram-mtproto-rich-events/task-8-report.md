# Task 8 Report: Telegram Authorization and Driver Lifecycle

## Scope delivered

- Added validated Telegram driver configuration with explicit dependency seams for client creation, update option configuration, durable store opening, manager, resynchronizer, dispatcher, and dispatcher drain.
- Added a narrow phone/code/2FA authenticator over pinned gotd v0.161 APIs.
- Explicitly rejects signup/TOS, QR, passkey, payment-required code, email verification, and OAuth flows with `ErrUnsupportedAuth`.
- Added a deterministic client factory seam and production `GotdClientFactory` around `telegram.NewClient` and the client's one-shot `Run`.
- Added serialized lifecycle states, manager startup gate observation, durable recovery readiness, child failure/panic propagation, dispatcher drain delegation, one-shot restart rejection, idempotent shutdown, caller-deadline handling, and SQLite close-after-child-exit ordering.
- Driver errors do not include credential callback values, session bytes, code hashes, or panic values. Panic reports contain only a bounded stack digest.

## Focused evidence

Environment required an explicit Go 1.27 GOROOT because the shell's inherited GOROOT pointed at Go 1.26.5.

- `GOROOT=/home/tundra/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.0.linux-amd64 go test ./internal/adapter/telegram -run '^(TestConfig|TestUserAuthenticator|TestNamedUnsupported|TestDriver)' -timeout 20s`
  - PASS (`ok`, 0.024s)
- `GOROOT=/home/tundra/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.0.linux-amd64 go test -race ./internal/adapter/telegram -run '^(TestConfig|TestUserAuthenticator|TestNamedUnsupported|TestDriver|TestValidateSession|TestNewSession)' -count=1 -timeout 30s`
  - PASS (`ok`, 1.035s)
- `GOROOT=/home/tundra/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.0.linux-amd64 go test ./internal/adapter/telegram -count=10 -timeout 30s`
  - PASS (`ok`, 0.248s); package `TestMain` uses `goleak.VerifyTestMain`.

## Review fixes

A focused reviewer identified update option ordering, close-before-child-exit, Start/Stop publication races, immediate dispatcher cancellation during drain, clean child exit handling, pre-callback client failure hangs, and concurrent Stop deadline handling. The implementation now configures update options before client construction, publishes client ownership under the lifecycle lock, starts asynchronous one-time cleanup observed independently by every Stop caller, closes storage only after children exit, delegates graceful drain separately from cancellation, treats unexpected child completion as fatal, and signals startup failures from the client supervisor.

## Residual concerns

- Task 10 composition must supply `ConfigureClientOptions`, `RunManager`, `RunResynchronizer`, `RunDispatcher`, and `DrainDispatcher` using the existing Task 5/6/7 concrete components. This task intentionally does not add the composition root.
- `InboxDispatcher` itself currently exposes cancellation-based `Run` but no graceful claim-stop method. The Task 8 driver therefore requires an injected `DrainDispatcher` policy so composition can coordinate admission shutdown without conflating it with final cancellation.
- On a Stop deadline, visible state becomes stopped while owned cleanup continues in the background until children exit; SQLite remains open until that completion, preserving close-last and avoiding use-after-close.
