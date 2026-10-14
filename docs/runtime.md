# Runtime

## Events

An event is an immutable `pkg/otogi` value with:

- a kernel-assigned event ID;
- platform, account, and conversation scope;
- the platform's own event ID when it has one;
- an action (created, edited, deleted, reaction, membership, and so on);
- timestamps, actor, and relations (reply, thread, edit target, and so on);
- a rich content graph of ordered typed parts (text with annotations, media, files, embeds, and unknown platform data kept as extensions).

The model is designed against Telegram, Matrix, XMPP, Discord, and QQ, so one module sees the same shapes whichever platform the event came from. Details are in the Telegram design spec under `docs/superpowers/specs/`.

## Delivery

Delivery is at-least-once from the platform to modules:

1. The driver writes each inbound update to its durable inbox before processing.
2. It submits the converted event to the kernel and waits for the kernel's completion acknowledgement.
3. Only then does it mark the inbox row done. After a crash, unfinished rows are replayed.

Because events can be replayed, outbound effects carry an idempotency key derived from (event ID, module, effect slot). A driver that records sends by key does not repeat a send on replay; Telegram does this. Modules with their own side effects use the same key.

Missed platform history is the driver's job. Telegram, for example, fetches updates it missed while offline. If history cannot be recovered, the driver records a gap in the event log and keeps running; it does not block the bot.

## Pipeline, ordering, and concurrency

`Submit` validates an event, takes one of `MaxInFlight` slots (blocking under the caller's context when none is free), and runs the middleware chain synchronously in registration order. Middleware may modify or drop the event; drops are recorded. The event then fans out into one delivery per subscribed module.

Each delivery goes into a lane keyed by (module, platform, account, conversation). A pool of kernel-owned workers runs lanes, one delivery at a time per lane. So a module sees one conversation's events in order, while different conversations, and different modules on the same event, run in parallel. A slow module delays only its own lane. A module may declare `OrderNone` to run every delivery independently.

Modules are independent: there is no order between modules for the same event. Anything that must happen before all modules belongs in middleware.

When every delivery of an event has finished, the kernel calls the driver's `done` callback with the outcome. Details are in the [lean core contracts design](superpowers/specs/2026-10-14-lean-core-contracts-design.md).

## Queues

Every queue has a fixed capacity and one owner. When a queue is full, the producer either gets an explicit capacity error or blocks under its context. A driver with a durable inbox simply leaves the work in the inbox and retries later. Nothing is dropped silently.

## Context

Blocking and long-lived operations take `context.Context` first and return promptly when it is cancelled. Background work runs under its component's lifecycle context. Cleanup that must outlive a cancelled request gets its own bounded context.

## Component lifecycle

Long-lived components (kernel, drivers, management plane) follow:

```text
new -> starting -> ready -> draining -> stopped
          \          \         \
           +-------> failed ----+-> stopped
```

- `Start(ctx)` runs once and returns when the component can serve or has failed.
- A required component failing after start is recorded with its cause and triggers graceful process shutdown with a non-zero exit.
- `Drain(ctx)` stops taking new work and lets accepted work finish.
- `Stop(ctx)` releases everything, is safe to call more than once, and works from any state.

Components are not restarted in-process. The process supervisor (systemd, a container runtime) restarts the process.

## Goroutines

Every goroutine has an owner that can cancel it and waits for it on stop. Blocking channel operations also select on cancellation. Only the producer closes a channel. No detached goroutines.

## Panics and errors

The kernel recovers module panics per delivery and records the panic and stack (in the process log until the event log exists); other deliveries continue. Each delivery also has a timeout (default 10 minutes, overridable per module) that cancels its context. Drivers recover panics in their goroutines and fail their lifecycle with the panic as cause.

An error is handled once: returned, translated at a boundary, or recorded as a failed outcome. Logging alone does not handle an error.

## Shutdown

On SIGINT or SIGTERM:

1. mark the process not ready;
2. stop drivers from taking new platform input;
3. drain the kernel within the drain timeout;
4. stop components in reverse start order;
5. exit 0 on a clean stop and non-zero otherwise.

A second signal forces immediate exit. With a durable inbox, unfinished events are replayed on the next start.
