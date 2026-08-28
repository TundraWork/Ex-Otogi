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
make test-fuzz-smoke
make test-corpus
make test-leak
make smoke
make quality
```

`make smoke` captures standard output with POSIX command substitution, which removes all trailing newline characters, then requires the remaining text to equal `echo: hello`; a nonzero executable exit also fails the target. `make test-fuzz-smoke` runs bounded smoke executions of both Telegram fuzz targets, `FuzzConvertEntitiesUTF16` and `FuzzDecodeEventInput`. `make quality` runs `fmt-check`, `lint`, `arch-check`, `test`, `test-race`, `test-fuzz-smoke`, `test-corpus`, `test-leak`, and `smoke`; it runs neither mutating `make format` nor opt-in `make telegram-smoke`.

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
