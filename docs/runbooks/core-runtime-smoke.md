# Build, Gates, and Offline Smoke

Run commands from the repository root. The launchers in `scripts/quality/` select Go 1.27.0 and the pinned linter; the first run may download them.

## Build and run the offline smoke

```sh
./scripts/quality/go.sh build -o /tmp/ex-otogi ./cmd/otogi
/tmp/ex-otogi
```

Expected output:

```text
echo: hello
```

Today `cmd/otogi` wires a scripted source, the kernel, the echo module, and a capture messenger. It submits one `hello` event, drains, prints the captured reply, and exits. It becomes the real bot entrypoint in the runnable-bot milestone.

## Quality gates

```sh
make format    # apply formatting
make quality   # fmt-check, lint, arch-check, test, test-race, test-fuzz-smoke, test-corpus, test-leak, smoke
```

`make smoke` fails unless the binary exits 0 and prints exactly `echo: hello`. Individual gates are listed in [`../quality.md`](../quality.md).
