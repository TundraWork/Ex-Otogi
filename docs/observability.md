# Observability

## Purpose

Telemetry explains externally visible behavior, capacity, and failure without becoming part of correctness. Ex-Otogi uses OpenTelemetry concepts and W3C trace context while keeping exporter choice replaceable.

Telemetry libraries MUST NOT leak into domain contracts. Instrumentation depends on narrow internal ports or is applied at boundaries.

## Correlation

Every accepted event has a runtime event ID and local trace. Valid remote trace context MAY be linked or continued according to boundary policy, but it is untrusted: unsupported fields, excess baggage, and invalid identifiers are discarded.

Logs emitted during an invocation include trace and span IDs when sampled, plus bounded component, extension, outcome, and failure-class fields. User IDs, conversation IDs, event IDs, request paths containing identifiers, and error text are not metric labels.

## Traces

Spans represent meaningful boundaries:

- external receive and validation;
- admission and queue wait;
- kernel routing;
- module invocation;
- durable transaction;
- external side effect;
- management operation;
- startup, recovery, drain, and shutdown.

Span status reflects the operation contract, not merely whether an error object exists. Cancellation, timeout, rejection, and capacity outcomes are distinguished. Payloads and secret values MUST NOT be span attributes or events.

Sampling policy is explicit. Error and latency diagnostics SHOULD retain enough parent context to explain causality without forcing full sampling of all participant traffic.

## Metrics

The runtime exposes cumulative counters, current-state gauges, and duration/size histograms for:

- ingress, acceptance, rejection, shedding, deferment, and completion;
- queue depth, oldest age, wait duration, and saturation;
- active and limited concurrency;
- invocation and external-call duration by bounded outcome;
- retries, breaker state, and ambiguous effects;
- database transaction, busy, checkpoint, WAL, backup, and restore state;
- lifecycle state and shutdown progress;
- telemetry drops and exporter failures.

Metric names, units, descriptions, and label sets are reviewed contracts. Labels use bounded enumerations or configured extension identities. A new label requires a cardinality estimate and load evidence.

## Logs

Logs are structured records with timestamp, severity, stable message identifier, component, action, outcome, and correlation fields. Free-form prose supplements these fields but does not replace them.

Expected validation and authorization failures are not logged repeatedly at error severity; aggregate metrics and rate-limited diagnostic records prevent attacker-controlled log amplification. An error is logged at the boundary that makes the final handling decision.

Sensitive fields are excluded at construction. Redaction is defense in depth, not the primary control. Panic reports and stack traces follow the same policy.

## Health endpoints

- **Liveness** answers whether the process can make progress. It does not depend on optional remote services and does not trigger expensive work.
- **Readiness** answers whether the instance should accept new work. It is false during startup, invariant failure, unavailable required dependencies, drain, and shutdown.
- **Diagnostics** provide authenticated component and capacity detail; they are not exposed by the unauthenticated health endpoint.

Health responses are bounded, fast, cache-controlled, and free of secrets and participant data.

## Telemetry pipeline behavior

Instrumentation on a core path is non-blocking or bounded to a negligible configured budget. Export uses bounded memory and honors process shutdown deadlines. When capacity is exhausted, telemetry drops according to explicit priority and increments a self-observability counter.

Exporter failure MUST NOT crash, deadlock, or indefinitely delay event processing. It may mark observability degraded for operators. Audit durability is separate from best-effort telemetry and uses the kernel-owned storage contract in `architecture.md`; exporters consume audit records only as derived output.

## Stable vocabulary

The resource describes service name, release version, instance ID, deployment environment, and host attributes approved for disclosure. Telemetry uses these exact bounded values:

| Field | Allowed values |
| --- | --- |
| `component` | `composition`, `kernel`, `module`, `driver`, `storage`, `management`, `telemetry` |
| `outcome` | `accepted`, `succeeded`, `rejected`, `shed`, `deferred`, `retrying`, `failed`, `ambiguous`, `canceled`, `timed_out`, `forced`, `lost` |
| `failure.class` | `invalid`, `unauthorized`, `conflict`, `capacity`, `transient_dependency`, `permanent_dependency`, `canceled`, `deadline`, `ambiguous_effect`, `invariant`, `other` |
| `lifecycle.state` | `new`, `starting`, `ready`, `draining`, `stopped`, `failed` |

`failure.class` maps one-for-one to the taxonomy in `reliability.md`; an unrecognized adapter error maps to `other` and retains detail only in a bounded diagnostic record. `lifecycle.state` maps one-for-one to `runtime.md`. Platform type and configured extension identity come from startup-validated descriptors; their combined count is bounded by the published workload envelope. Raw platform names or remote identifiers never create new label values.

Semantic convention upgrades are reviewed for renamed attributes, changed units, and cardinality. Experimental convention changes are not silently adopted through dependency upgrades.

## Diagnostic acceptance

For every required failure scenario in `reliability.md`, tests or smoke scenarios MUST show that an operator can determine:

1. what operation failed;
2. which bounded component owned it;
3. whether work was rejected, retrying, ambiguous, or lost;
4. the causal error class;
5. current readiness and capacity impact;
6. the correlation path without access to secret or raw message content.

Telemetry tests inspect emitted fields for semantic correctness and forbidden sensitive categories. Load tests measure telemetry overhead and cardinality growth.
