# Security

Ex-Otogi runs on a host the operator controls, for one operator. Security covers the things that realistically go wrong: leaked credentials, an exposed management API, and hostile or oversized platform input.

## Credentials

- Telegram application ID and hash, the account phone number, and the 2FA password come from the config file or environment variables, never from command-line arguments.
- The gotd session file is equivalent to a logged-in account. Keep it, together with the SQLite databases, in a data directory readable only by the service user.
- Credentials are not written to logs or the event log. They are configuration, not event data, so nothing needs redacting: just do not log them.
- The Gemini API key comes from the config file, normally as `${OTOGI_GEMINI_API_KEY}`. It is sent only to the configured base URL.
- Do not commit credentials. `.env.example` shows the variable names.

## Language model data

Calls to Gemini send the conversation, images included, to Google (or to the configured gateway). Calls are stateless (`store=false`), so Google keeps no interaction for later turns, but its terms still apply: on the free tier Google may use prompts and replies to improve its products, with human review, and apps used by people in the EEA, Switzerland, or the UK must use the paid tier. Google's terms require users to be 18 or older. These are the operator's decisions; see the [Gemini API terms](https://ai.google.dev/gemini-api/terms).

## Management plane access

- The management HTTP server binds to `127.0.0.1` by default.
- When bound to any other address, it requires a bearer token from the configuration. Use TLS or a reverse proxy when it is reachable over a network.

## Event data

The event log and inbox store full message content by design, because operators need it to debug the bot. Protect these files like the session file. There is no redaction or minimization layer.

## Platform input

Platform payloads are untrusted. Drivers enforce limits only where the platform itself does not and where a real resource problem exists: maximum media download size, maximum stored payload size, and timeouts on every network call. Parsers return errors on malformed input; they never panic.

## Dependencies

Prefer mature, actively maintained, widely used libraries over our own implementations; size alone is not a reason to avoid one. Run `govulncheck` when upgrading dependencies or before tagging a release, and fix reachable vulnerabilities.
