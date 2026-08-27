# Task 1 report

Status: complete.

## Implemented

- Added immutable protocol-neutral `Extension`, `ProviderLocator`, annotation, media/source/hash, relation/reference, part, and bounded `Message` DAG values.
- Added `EventAction`, `Direction`, `Completeness`, rich event envelope, rich outbound message, `ErrUnsupported`, and `Invocation.Send`; removed text-only `Text`, `EventKind`, and `SendText` APIs.
- Migrated kernel compile adapters, action routing compile surface, capture/scripted fixtures, echo, smoke composition, and exact API-shape lock.
- Added Telegram, Matrix, Discord, XMPP, MIME, and QQ corpus fixtures/tests.
- Enforced per-owner and event aggregate count/byte/depth limits, overflow-safe accounting, media-source exclusivity, defensive ownership, ordered duplicate extension preservation, and relation/message ownership.

## TDD evidence

Initial rich-value tests failed to compile on undefined `Extension`, `PartID`, `ProviderLocator`, and related constructors. Focused implementation then passed.

Additional review regressions failed before fixes:

- `TestTextPartRejectsEmptyWithoutAnnotations`: empty unannotated text was accepted.
- `TestMediaHashRejectsOversizedValueBeforeCopy`: oversized hash bytes were copied.
- `TestMessageRejectsLeafFirstDepthOverflow`: depth validation depended on part order.
- `TestMessageCountsHashBytesInMetadata`: hash bytes were omitted from aggregate metadata.

After fixes, the focused command passed:

`go test ./pkg/otogi -run 'TestMessageRejectsLeafFirstDepthOverflow|TestMessageCountsHashBytesInMetadata|TestTextPartRejectsEmptyWithoutAnnotations|TestMediaHashRejectsOversizedValueBeforeCopy' -count=1`

### Review fix round 1

Eight review regressions were added before fixes. They covered immutable core action constants, malformed relation/extension rejection, Discord redacted event construction, root overhead accounting, a real Discord interactive tree, typed known-part payload families, graph roots/reachability, and Telegram opaque media locator preservation. The malformed-owner/reachability/envelope tests failed against the prior implementation, and the action compile-time assertion failed while actions were variables.

Known location/contact/poll/interactive/embedded content now uses dedicated immutable payload types, constructors, and accessors; `NewTypedPart` was removed. `Message` now rejects rootless/disconnected graphs and malformed owned values, charges root collection overhead and typed payload metadata, and event envelopes validate extensions. Core actions are immutable typed constants whose recognition does not depend on mutable globals.

Focused review command passed:

`go test ./pkg/otogi -run 'TestMessageRejectsMalformedOwnedValues|TestMessageRequiresRootsAndReachability|TestEventInputRejectsMalformedEnvelopeExtension|TestMessageChargesRootCollectionMetadata|TestDiscordComponentAndRedactedCorpus|TestTelegramEntityMediaReplyCorpus|TestTypedPartFamilies' -count=1`

Re-review found direct string conversion could bypass `NewEventAction`; `TestContractsRejectUnvalidatedDirectEventActions` failed before the shared validator was applied to `NewEventInput` and `NewModuleDescriptor`, then passed after the fix.

## Quality evidence

Fresh checkpoint command:

`make fmt-check && make lint && make arch-check && make test && make test-race && make test-leak && make smoke`

Result: all commands exited zero; golangci-lint reported 0 issues; unit and race suites passed for every package; leak checks and rich-message smoke passed.

## Concerns

None. Telegram driver/storage/gotd work is intentionally absent.
