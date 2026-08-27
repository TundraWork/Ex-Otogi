# Task 4 Report

Status: complete.

## Delivered

- Added `internal/adapter/telegram/mapper/` with focused peer, entity, locator/extension codec, media, relation/metadata, update, and quarantine mapping.
- Mapped canonical Telegram user/chat/channel identities, authorized-user-scoped accounts and source keys, common/channel boxes, incoming/outgoing flow, send-as-compatible actor projection, missing actors, channel deletes, and unresolved common-delete tombstones.
- Mapped layer-228 common/channel new and edit updates, `message`, `messageService`, and `messageEmpty`; unsupported update constructors enter a namespaced bounded deterministic raw quarantine envelope instead of a message lifecycle action.
- Converted Telegram UTF-16 entity offsets to core UTF-8 byte annotations with BMP, surrogate, overlap/nesting, malformed-range, unknown-constructor, collection, and preservation bounds. Unsupported or malformed entities retain deterministic TL bytes as ordered Telegram extensions.
- Added typed location/contact and photo/document media projections. Photo/document media use opaque refresh locators containing ID, access hash, file reference, DC, and variant data; no media bytes are eagerly loaded.
- Added provider-message and media locator codecs plus request-shape selection between `messages.getMessages` and `channels.getMessages`; no RPC is executed.
- Added typed reply and thread relations, including cross-dialog reply peers and forum-topic top IDs. Forward, grouped album, reactions, reply metadata, and a bounded raw message preservation record remain ordered Telegram extensions.
- Added deterministic normalized source fingerprints, excluding negative difference-derived positions, plus typed-nil and oversized-input rejection.
- Added package/API Godoc and mapper unit, fuzz, race, bounds, determinism, codec, media, metadata, relation, and update tests.
- No manager, update lifecycle, transport, dispatcher, networking, or send RPC was added.

## Exact Files

- `internal/adapter/telegram/mapper/doc.go`
- `internal/adapter/telegram/mapper/peer.go`
- `internal/adapter/telegram/mapper/entity.go`
- `internal/adapter/telegram/mapper/codec.go`
- `internal/adapter/telegram/mapper/update.go`
- `internal/adapter/telegram/mapper/peer_test.go`
- `internal/adapter/telegram/mapper/entity_test.go`
- `internal/adapter/telegram/mapper/codec_test.go`
- `internal/adapter/telegram/mapper/update_test.go`
- `internal/adapter/telegram/mapper/media_test.go`
- `internal/adapter/telegram/mapper/metadata_test.go`
- `internal/adapter/telegram/mapper/robustness_test.go`

## TDD Evidence

Tests were written before each production slice and were observed failing for the intended missing behavior:

- Entity tests first failed because `ConvertEntities` and UTF-16 conversion did not exist; after implementation they passed, then the fuzz target exercised rune/surrogate boundaries.
- Peer tests first failed because canonical `PeerID` and flow/actor mapping did not exist; after implementation they passed.
- Locator, refetch-selection, media-locator, and quarantine tests first failed on undefined APIs; after the codecs were implemented they passed.
- New/edit/delete/channel/service/empty/unsupported update tests first failed on missing `Config`, `Mapper`, and mapping APIs; after implementation they passed.
- Metadata ordering and reply relation tests failed with unordered extensions and no typed relation; ordered mapping and typed relations made them green.
- Typed media tests failed because geo/contact and photo/document values were opaque; typed projections and refresh locators made them green.
- Robustness regressions failed for typed-nil panics, negative-position source-key divergence, unbounded entity/delete collections, and unscoped event accounts; each passed after the corresponding source fix.
- Cross-peer/topic regression failed with one relation instead of reply plus thread; target-peer and top-ID handling made it green.

## Verification

- `go test ./internal/adapter/telegram/mapper` — passed.
- `go test -race ./internal/adapter/telegram/mapper` — passed.
- `go vet ./internal/adapter/telegram/mapper` — passed.
- `go test ./internal/adapter/telegram/mapper -run '^$' -fuzz=FuzzConvertEntitiesUTF16 -fuzztime=2s` — passed; final run executed 471,527 cases and retained 48 interesting inputs.
- `make lint` — passed with `0 issues`; doccheck passed.
- `make test` — all repository packages passed.
- `make test-race` — all repository packages passed.
- `git diff --check` — passed.

The workstation exposed a Go 1.27 command with a Go 1.26.5 `GOROOT`; verification explicitly set `GOROOT=/home/tundra/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.0.linux-amd64` so compiler and standard-library artifacts matched the repository's Go 1.27 requirement.

## Internal Review Fixes

A dedicated reviewer found Important issues. Before final verification, the mapper was corrected to:

- reject typed-nil update/message constructors rather than panic;
- exclude negative provider positions from source-key identity;
- project photo/document metadata and refresh locators;
- honor cross-dialog reply peers and topic top IDs;
- preserve all unprojected message flags/optional fields through one bounded deterministic raw message extension;
- bound entity and delete collections before allocation;
- scope accounts, quarantine, deletes, relations, and message source identities by the authorized Telegram user;
- validate positive channel/message IDs and add focused regression coverage.

Known layer-228 constructors without a safe protocol-neutral projection remain constructor-preserving Telegram extensions; they are not falsely assigned unrelated core semantics.

## Self-Review and Scope

- Generated `tg` types occur only inside `internal/adapter/telegram/mapper`; none enter `pkg/otogi` or kernel APIs.
- Mapping and codecs are deterministic and network-free. Refetch selection returns request values only.
- Raw extension/quarantine guarantees apply to the stored deterministic TL/envelope bytes, not to an alleged canonical Telegram wire representation.
- Bounds are checked before large mapper-owned allocations, and opaque locator/preservation payloads use the core/store-compatible limits.
- The implementation does not claim recovery of deleted, TTL, inaccessible, or secret media/content.
- Task 5 manager/journal integration and locator-backed resolution of common deletes remain outside Task 4 by plan.

## Commit

- Implementation: `6945e891 feat: map Telegram updates to rich events`
