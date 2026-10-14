# Security

Ex-Otogi runs on a host the operator controls, for one operator. Security covers the things that realistically go wrong: leaked credentials, an exposed management API, and hostile or oversized platform input.

## Credentials

- Telegram application ID and hash, the account phone number, and the 2FA password come from the config file or environment variables, never from command-line arguments.
- The gotd session file is equivalent to a logged-in account. Keep it, together with the SQLite databases, in a data directory readable only by the service user.
- Credentials are not written to logs or the event log. They are configuration, not event data, so nothing needs redacting: just do not log them.
- Do not commit credentials. `.env.example` shows the variable names.

## Management plane access

- The management HTTP server binds to `127.0.0.1` by default.
- When bound to any other address, it requires a bearer token from the configuration. Use TLS or a reverse proxy when it is reachable over a network.

## Event data

The event log and inbox store full message content by design, because operators need it to debug the bot. Protect these files like the session file. There is no redaction or minimization layer.

## Platform input

Platform payloads are untrusted. Drivers enforce limits only where the platform itself does not and where a real resource problem exists: maximum media download size, maximum stored payload size, and timeouts on every network call. Parsers return errors on malformed input; they never panic.

## Dependencies

Prefer mature, actively maintained, widely used libraries over our own implementations; size alone is not a reason to avoid one. Run `govulncheck` when upgrading dependencies or before tagging a release, and fix reachable vulnerabilities.
