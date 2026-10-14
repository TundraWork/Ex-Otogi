# Extensions

## Model

Modules and drivers are Go packages compiled into the binary and registered explicitly in `cmd/otogi`. There are no dynamically loaded plugins. Flexibility comes from a rich, stable SDK in `pkg/otogi`: a module written once works on every platform the event model covers, and new modules need no kernel changes.

## Modules

A module implements bot behavior. It:

- declares its identity, subscribed actions, ordering, and timeout in a `ModuleDescriptor`;
- handles each event through `Handle(ctx, event, invocation)` and returns an error or nil;
- acts only through capabilities on its `Invocation` (send, send elsewhere, edit, delete, react, fetch media);
- never imports platform SDKs, kernel internals, or another module's package.

### SDK

The module SDK is designed in [`2026-10-14-module-sdk-design.md`](superpowers/specs/2026-10-14-module-sdk-design.md) and delivered in steps:

- messaging: send, send to other conversations, reply and quote (relations), edit, delete, react, send and fetch media (implemented);
- commands and filters declared in the descriptor;
- per-module key/value store, private SQLite database, and typed config;
- cron jobs, persistent timers, and typed services shared between modules.

Every effect names a slot; the kernel derives its idempotency key from the unit of work (event, job run, or timer run), the module, and the slot, so replays never repeat effects. Drivers receive effects through `otogi.Effector` and return `ErrUnsupported` for kinds they do not implement.

## Drivers

A driver adapts one IM platform. It:

- connects and authenticates to the platform;
- converts platform updates into rich events, keeping unknown platform data as extensions rather than dropping it;
- owns platform recovery (sequence state, catching up on missed history);
- implements outbound capabilities for its platform and translates platform errors into `pkg/otogi` errors;
- follows the component lifecycle in `runtime.md`.

A driver does not decide routing or module behavior.

## Configuration

Each module and driver has its own typed section in the main config file. Unknown fields are rejected so typos surface at startup. Defaults are documented next to the config struct.

## Failure isolation

A module panic, error, or timeout affects only that delivery. It is recorded with the module identity, and other modules still receive the event.

## Middleware

Middleware runs on every event before modules, in registration order, and may modify or drop it. It is where cross-cutting policy goes: allowlists, sleep mode, rate limits, command parsing. It must be fast.

## Compatibility

The SDK is unstable while `next` is in development. Breaking changes update all in-tree modules in the same change. There is no compatibility with `master` modules.
