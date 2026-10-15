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

## Inspect a running bot

```sh
curl -s localhost:8090/api/status
curl -s 'localhost:8090/api/events?limit=20'                  # newest records
curl -s 'localhost:8090/api/events?kind=module.failed'        # failures only
curl -s 'localhost:8090/api/events?q=hello'                   # text in payloads
curl -s localhost:8090/api/events/<event-id>/trace            # one event end to end
curl -s -X POST localhost:8090/api/shutdown                   # graceful stop
```

The event log is also a plain SQLite file: `sqlite3 data/eventlog.sqlite "SELECT id, kind, payload FROM records ORDER BY id DESC LIMIT 20"`.

## Quality gates

```sh
make format    # apply formatting
make quality   # lint, test-race, test-fuzz-smoke, smoke
```

`make smoke` fails unless the binary exits 0 and prints exactly `echo: hello`. Individual gates are listed in [`../quality.md`](../quality.md).

## Deploy to the test host

The test host runs the `next` branch as `ex-otogi-next.service` from `/srv/ex-otogi-next` (`otogi`, `otogi.yaml`, `0600` `otogi.env` with the Telegram app credentials, and `data/`). The management API is on the host's `127.0.0.1:8383`.

```sh
make deploy            # build linux/amd64, upload, validate the host's config with the new binary,
                       # keep otogi.prev, swap, restart, and wait for /readyz
make deploy-rollback   # restore otogi.prev and restart
```

`DEPLOY_HOST`, `DEPLOY_DIR`, `DEPLOY_SERVICE`, and `DEPLOY_READY_URL` override the defaults. A deploy that does not become ready within 60 seconds prints the service's recent logs and fails. Inspect the running bot with `ssh <host> 'curl -s localhost:8383/api/status'` and `journalctl -u ex-otogi-next -f`.
