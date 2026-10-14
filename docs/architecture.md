# Architecture

## Style

Ex-Otogi is one Go process: drivers, kernel, modules, management plane, and SQLite storage. Boundaries are package boundaries, not network boundaries. Adding a broker, sidecar, or separate service needs evidence that one process cannot do the job.

## Layers and dependencies

```text
cmd/otogi (composition root) ──> everything
internal/kernel      ──> pkg/otogi
modules/*            ──> pkg/otogi
internal/adapter/*   ──> pkg/otogi
pkg/otogi            ──> standard library only
```

- **`pkg/otogi`, contracts:** protocol-neutral event, content, and messaging values, plus the module SDK interfaces.
- **`internal/kernel`:** admission, routing to modules, invocation, panic containment, lifecycle.
- **`modules/*`:** bot behavior, written only against `pkg/otogi`.
- **`internal/adapter/*`:** platform drivers and infrastructure (storage, management HTTP, and so on). Adapters translate I/O. They do not make routing or module decisions.
- **`cmd/otogi`:** loads configuration, constructs everything, and runs the process lifecycle.

Kernel, modules, and adapters import only `pkg/otogi` from this repository. Third-party libraries are welcome anywhere except `pkg/otogi`, which stays standard-library-only so the module SDK carries no dependency baggage; platform SDKs stay inside their adapter. Sub-packages of one adapter may import each other. depguard rules in `.golangci.yml` enforce this as part of `make lint`. Tests may import collaborators, such as the real kernel, for integration tests.

## Platform types stay in their adapter

gotd and the generated `tg` types are imported only under `internal/adapter/telegram`. The same applies to every future platform SDK. Drivers convert platform payloads to `pkg/otogi` rich events before anything else sees them. Platform-specific state (update sequence state, access hashes, the session) is owned by the driver.

Platform details that modules occasionally need travel as extension data on the rich event. Modules never receive SDK types.

## Composition

`cmd/otogi` is the only place that picks concrete implementations. It:

1. loads and validates the configuration file;
2. constructs storage, drivers, modules, the management plane, and the kernel;
3. starts them in dependency order and reports readiness;
4. on shutdown, drains and stops them in reverse order.

Constructors do no I/O and start no goroutines; `Start` does. Modules and drivers are registered explicitly in code. There is no init-time registration, filesystem discovery, or service locator.

## Persistence

SQLite on local disk is the store. Each component that persists data owns its own schema and queries; others go through its API, never its tables. Transactions start and end inside the owning component. Today's owners:

- the Telegram adapter, for its durable inbox, update state, and send records (one database per account, next to its gotd session file);
- the event log (see `observability.md`), when implemented.

## Data flow

1. A driver receives a platform update, durably records it, and converts it to a rich event.
2. The driver submits the event to the kernel and waits for its completion acknowledgement before marking it done.
3. The kernel routes the event to subscribed modules.
4. Modules act through capabilities such as `Invocation.Send`, which the kernel routes back to the right driver.
5. Each step is recorded in the event log.

Adapters translate SDK errors into `pkg/otogi` errors once. Kernel and modules never inspect SDK error types.
