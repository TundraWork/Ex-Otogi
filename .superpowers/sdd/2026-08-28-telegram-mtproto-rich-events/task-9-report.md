# Task 9 Report

## Delivered

- Raw `tg.MessagesSendMessage` rich-text sender with one-text-part validation, supported Telegram entities, reply/thread mapping, durable peer reconstruction, versioned request digest, and stable positive nonzero `random_id`.
- Durable send lifecycle integration using the existing send-intent store, including idempotency conflict handling, exact flood/slowmode retry times, no hidden retry/sleep, conservative ambiguity handling, startup reconciliation, 30-day terminal retention, and indefinite ambiguous retention.
- Returned RPC updates pass through the existing journal gate. `updateMessageID` is captured into the same transaction that normalizes the journal row and completes the matching send intent.
- Origin refetch selects `messages.getMessages` or `channels.getMessages` from the provider locator. Media refresh refetches its origin and returns bounded refreshed locator material without downloading bytes.

## Red evidence

Focused sender tests initially failed to compile because `NewSender`, `RandomID`, `RequestDigest`, `RateLimitError`, and `NewRefetcher` did not exist. The reconciliation test separately failed because `Sender.Reconcile` did not exist.

## Green evidence

- `env -u GOROOT go test ./internal/adapter/telegram ./internal/adapter/telegram/store ./internal/adapter/telegram/updates ./internal/adapter/telegram/mapper`
- `env -u GOROOT go test -race ./internal/adapter/telegram -run 'Test(SendCodec|UnsupportedGraph|RateLimit|PeerReconstruction|RestartReconciliation|UnsupportedAnnotation|SameKey|OriginRefetch|MediaRefresh)'`
- `env -u GOROOT go vet ./internal/adapter/telegram ./internal/adapter/telegram/store ./internal/adapter/telegram/updates ./internal/adapter/telegram/mapper`

All listed focused checks passed after implementation. Package TestMain runs goleak for the Telegram adapter and updates package.

## Review fixes

The pre-commit review identified and the implementation corrected: cancelled caller contexts during mandatory post-RPC persistence, false zero-random-ID correlation, ignored ambiguity transition failures, silent annotation-target loss, unbounded refreshed file references, and missing durable `updateMessageID` capture.

## Residual concerns

- The full Telegram package race invocation exposed the pre-existing lifecycle test failure `TestDriverCallerCancellationWinsStopRace` (`state=stopped cause=context canceled`); the Task 9-only race set passes. This Task does not modify that lifecycle path.
- Startup reconciliation consumes the existing recoverable-intent state and durable update correlation. A still-unproven in-flight RPC is deliberately retained as ambiguous for operator resolution; no automatic retry occurs.
