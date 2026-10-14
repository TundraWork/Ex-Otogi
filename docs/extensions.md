# Extensions

## Model

Modules and drivers are Go packages compiled into the binary and registered explicitly in `cmd/otogi`. There are no dynamically loaded plugins. Flexibility comes from a rich, stable SDK in `pkg/otogi`: a module written once works on every platform the event model covers, and new modules need no kernel changes.

## Modules

A module implements bot behavior. It:

- declares its identity and the events it subscribes to (today: a `ModuleDescriptor` with consumed actions);
- handles each event through `Handle(ctx, event, invocation)` and returns a result;
- acts only through capabilities handed to it (today: `Invocation.Send`);
- never imports platform SDKs, kernel internals, or another module's package.

### SDK direction

Today's SDK covers one need: send a rich message in response to an event. It grows by adding capabilities that real modules need, designed in one place rather than as one-off hooks:

- messaging: send, reply, edit, delete, react, and fetch media;
- richer subscriptions: by action, platform, conversation, command, or content type;
- per-module typed configuration from the main config file;
- per-module persistent storage, owned by the module and backed by SQLite;
- timers and scheduled work owned by the kernel;
- typed services that one module provides and others consume, declared up front and wired at startup;
- structured log and event-log annotations from module code.

Each capability gets a small interface in `pkg/otogi`, implemented by the kernel or an adapter, and documented behavior (errors, cancellation, idempotency). The concrete SDK is designed as its own milestone.

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

A module panic or error affects only that invocation. It is recorded in the event log with the module identity, and other modules still receive the event.

## Compatibility

The SDK is unstable while `next` is in development. Breaking changes update all in-tree modules in the same change. There is no compatibility with `master` modules.
