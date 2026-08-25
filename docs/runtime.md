# Runtime Semantics

## Event model

An accepted event is immutable and contains:

- a runtime-wide event ID;
- platform, account, tenant, and conversation scope;
- source event ID when supplied by the platform;
- kind and schema version;
- occurrence and ingestion timestamps;
- actor and subject references where applicable;
- trace and causal-parent references;
- a bounded payload expressed as a domain value.

The runtime event ID is created once at admission and remains stable through retries. A source event ID is used for deduplication only within its declared platform/account scope. Missing external IDs MUST NOT be fabricated from mutable content.

## Delivery, ordering, and idempotency

External platforms and process recovery may cause duplicate delivery. The runtime therefore provides at-least-once processing for accepted durable work unless a capability explicitly documents weaker best-effort behavior.

- A handler that performs an externally visible effect MUST use an idempotency key or an atomic state/effect intent.
- Deduplication records MUST have a documented scope, retention period, and durable or volatile class.
- The runtime MUST NOT acknowledge durable acceptance before the event or equivalent recovery intent is committed.
- Failure after an external effect but before local acknowledgement is treated as an ambiguous outcome and retried only when the effect interface is idempotent.

Ordering is guaranteed only for one module delivery key `(module identity, module-declared ordering key)`, normally derived from platform account and conversation. Work for different module delivery keys MAY execute concurrently. For one delivery key, event $N+1$ MUST NOT begin until event $N$ reaches that module's terminal outcome or exhausts that module's retry budget; retries never overtake later deliveries on the same key. A blocked module/key MUST NOT prevent another module or unrelated key from progressing. Queue age makes retry-induced head-of-line blocking observable.

## Admission and queues

Every queue has one owner, a finite capacity, a documented unit, and an overload action. Allowed actions are:

- **reject:** caller receives a stable capacity error;
- **shed:** explicitly classified expendable work is discarded and counted;
- **backpressure:** producer blocks only under a cancellable context and bounded deadline;
- **durable defer:** work is transactionally persisted for later execution.

Silent drop and unbounded queues are prohibited. Queue depth, oldest-item age, admission outcome, and saturation are observable without high-cardinality labels.

Concurrency limits exist per process and at narrower contention boundaries such as driver account, module, destination, or database writer. Limits MUST be configuration-validated against the tested workload envelope.

## Context and deadlines

Every request-scoped, blocking, or long-lived operation accepts `context.Context` first. The caller owns cancellation. Callees MUST return promptly after cancellation and MUST NOT retain the context after the operation.

- An inbound deadline is preserved unless policy imposes a shorter one.
- A boundary without an inbound deadline MUST apply its configured maximum duration when blocking on an external dependency.
- Background lifecycle work uses a component lifecycle context, never `context.Background()` as an escape from ownership.
- Cleanup that must outlive a canceled request uses a new bounded lifecycle context owned by the cleanup coordinator.
- Context values carry request metadata only; dependencies and optional parameters use explicit arguments.

## Component lifecycle

Every long-lived component follows:

```text
new -> starting -> ready -> draining -> stopped
          |         |          |
          v         v          v
        failed <----+----------+
          |
          v
        stopped
```

- `Start(ctx)` is called once, performs bounded initialization, and reports success only when the component can serve its contract. Failure transitions `starting` to `failed`.
- A required component failure transitions `ready` to `failed` and prevents process readiness.
- `Drain(ctx)` transitions `ready` to `draining`, stops admission, and allows accepted work to complete within the drain budget. Drain failure transitions to `failed`.
- `Stop(ctx)` is valid from `starting`, `ready`, `draining`, or `failed`; it releases every resource acquired so far, is idempotent, and transitions to `stopped` even when cleanup returns an aggregated error.
- `failed` preserves the cause and whether restart is safe. The core process does not loop-restart components unless an approved component contract defines state-safe restart.

Lifecycle transitions are serialized and observable. Calls in an invalid state return stable errors rather than panic.

## Goroutine and resource ownership

A goroutine may start only when its owner can name:

- the cancellation signal;
- the completion observation path;
- the panic boundary;
- all channels, timers, and resources it owns;
- the maximum time shutdown waits for it.

Owners use structured concurrency, such as an error group tied to a lifecycle context, when sibling failure should cancel the group. Detached goroutines are prohibited.

Only a producer closes a channel. Receivers never infer global completion from a channel they do not own. Timer and ticker owners stop them. Blocking sends and receives select on cancellation.

## Panic and error boundaries

Panic is reserved for impossible programmer invariants before serving traffic. Goroutine, task, module, and driver entrypoints MUST recover panics, capture stack evidence, classify the owning unit as failed, and propagate an error to its supervisor. Recovery MUST NOT continue using potentially corrupted invocation state.

An error is handled once by returning it, translating it at a boundary, compensating, or terminating the owning lifecycle. Logs describe the final handling decision. Wrapped errors preserve identity with `%w` when callers need `errors.Is` or `errors.As`.

## Time and randomness

Kernel policy uses injected clocks and randomness sources when behavior depends on time, jitter, IDs, or sampling. Tests use deterministic implementations. Wall-clock time is not used to measure duration; monotonic time is retained where Go provides it.

## Graceful shutdown

On termination:

1. mark readiness false;
2. stop external admission;
3. drain accepted work until completion or the drain deadline;
4. cancel remaining invocation contexts;
5. flush durable state and bounded telemetry;
6. stop components in reverse dependency order;
7. close storage and process resources;
8. report clean or forced termination.

Each step consumes a portion of one overall shutdown budget. A forced shutdown records unfinished work by class and exits non-zero when durability guarantees may have been violated.

## Verification

Tests MUST cover queue saturation, cancellation while blocked, duplicate delivery, ambiguous external effects, ordering-key isolation, lifecycle transition rejection, panic containment, timer/goroutine termination, and graceful plus forced shutdown. Concurrency tests run under the race detector; long-lived tests include leak detection.
