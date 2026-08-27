# Experimental Core Runtime Smoke

This runbook covers the experimental, non-conforming event-bridge slice. It is not a production deployment or release-verification procedure.

## Prerequisites

Run commands from the repository root. The committed launchers select Go 1.27.0 and ignore an externally configured `GOROOT`; Go may download that toolchain and the pinned linter on first use.

## Build and run

Build outside the repository tree, then run the executable:

```sh
./scripts/quality/go.sh build -o /tmp/ex-otogi ./cmd/otogi
/tmp/ex-otogi
```

Expected standard output is exactly:

```text
echo: hello
```

The executable composes the scripted source, kernel, echo module, and capture adapter. It submits one `hello` event, drains the kernel, verifies one successful delivery, prints the captured reply, and stops.

## Verification

Apply formatting before the nonmutating checks:

```sh
make format
make fmt-check
make lint
make arch-check
make test
make test-race
make test-leak
make smoke
make quality
```

`make smoke` runs the executable and succeeds only when its complete output is `echo: hello`. `make quality` aggregates every listed nonmutating milestone gate; it does not run `make format`.

To verify that repository launchers reject a stale external toolchain root:

```sh
GOROOT=/definitely/stale make quality
```

## Deferred scope

These commands cover only the experimental milestone. The slice has no real IM platform driver or long-lived signal handling. It also deliberately defers:

- SQLite, migrations, durable delivery, deduplication, retries, and recovery;
- encrypted panic evidence, key management, evidence storage, and decryption tooling;
- generic extension graphs and dynamic plugins;
- authentication, management APIs, reload, and telemetry export;
- security and vulnerability scanning, backup and restore, fuzz/load/fault suites, generated-file verification, SBOMs, signatures, provenance, artifacts, and release verification.

There are no placeholder interfaces or fake-success commands for deferred capabilities. Passing the milestone gates does not establish production readiness or specification conformance.
