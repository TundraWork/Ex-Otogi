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
- streamed messages: `Invocation.Stream` shows a message while it is still being written, such as a language model's reply. The module passes the whole content so far to `Update` and the final content to `Close`; the driver decides how the platform shows it. Telegram shows "typing", then a live draft in private chats or an edited message elsewhere, continuing in new messages past its limits. Drivers implement `otogi.Streamer`; without one, the kernel sends the final content on `Close`;
- commands and filters declared in the descriptor (implemented): a declared command goes only to its module, and modules receive incoming events only unless their filter opts in to outgoing or system events;
- per-module key/value store, private SQLite database, `Starter`/`Stopper`, and typed config (implemented; see `modules/notes`);
- cron jobs, persistent timers, and typed services shared between modules (implemented; see `modules/reminders`).
- formatted text: `otogi.RichText` builds annotated text, and `pkg/markdown` converts Markdown into it (see `modules/todou`) and back, for handing chat text to a language model.

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

The module convention: a `Config` struct with `yaml` tags and a `New(Config)` constructor in `modules/<id>`, plus one entry in `cmd/otogi/modules.go` that decodes the section. A module is enabled by listing its ID under `modules:`.

## Operators and the account's owner

`operators` in the config lists platform-qualified actor IDs (`telegram:user:123`, exactly as `/whoami` prints them). The composition root hands the list to modules that gate commands; a denied user is told their ID. Messages sent from the bot's own account are the owner's and always count as an operator.

Command-driven modules admit outgoing messages in their filter so the owner can command the bot from the account itself. Their replies must never start with a command prefix, so a reply cannot invoke a command. Modules that react to every message (echo) stay incoming-only.

## Failure isolation

A module panic, error, or timeout affects only that delivery. It is recorded with the module identity, and other modules still receive the event.

## Middleware

Middleware runs on every event before modules, in registration order, and may modify or drop it. It is where cross-cutting policy goes: allowlists, sleep mode, rate limits, command parsing. It must be fast.

## Compatibility

The SDK is unstable while `next` is in development. Breaking changes update all in-tree modules in the same change. There is no compatibility with `master` modules.
