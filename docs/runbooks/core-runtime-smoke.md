# Build, Gates, and Offline Smoke

Run commands from the repository root. `go.mod` selects Go 1.27.0 and `tools/go.mod` pins golangci-lint; the first run may download and build them.

## Build and run the offline smoke

```sh
go build -o /tmp/otogi ./cmd/otogi
/tmp/otogi smoke
```

Expected output:

```text
echo: hello
```

`otogi smoke` wires the kernel, the echo module, and a capture messenger. It submits one `hello` event, waits for its outcome, prints the captured reply, and exits. It needs no config file and no network.

## Run the bot

```sh
cp otogi.example.yaml otogi.yaml        # then edit
export OTOGI_TELEGRAM_APP_ID=... OTOGI_TELEGRAM_APP_HASH=...
/tmp/otogi validate                     # check the config
/tmp/otogi login main                   # once per Telegram account
/tmp/otogi run                          # until SIGINT/SIGTERM
```

Run it under a supervisor that restarts it on non-zero exit. Logs go to stdout (`log.format: json` or `text`).

## Quality gates

```sh
make format    # apply formatting
make quality   # lint, test-race, test-fuzz-smoke, smoke
```

`make smoke` fails unless the binary exits 0 and prints exactly `echo: hello`. Individual gates are listed in [`../quality.md`](../quality.md).
