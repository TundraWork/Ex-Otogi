# Security

## Security model

Ex-Otogi assumes platform payloads, network peers, management clients, configuration files, restored databases, extension configuration, and propagated telemetry context may be malicious or malformed. Statically linked extension code is trusted release code but receives least authority.

Protected assets include platform credentials, participant content, identity and authorization state, durable module data, audit evidence, release integrity, and runtime availability.

## Trust boundaries

Boundary adapters MUST authenticate where the protocol supports it, validate syntax and semantics, enforce byte/count/depth/time limits, normalize once, and reject ambiguous representations before creating domain values.

Trust is not inherited across:

- public/platform network to driver;
- management client to management plane;
- configuration or secret provider to composition root;
- extension to issued capability;
- process to database or backup artifact;
- remote trace context to local telemetry.

Threat modeling is required when a change adds a boundary, protected asset, interpreter, credential, or externally reachable operation.

## Identity and authorization

Management access is authenticated and default-deny. Authorization evaluates principal, action, resource, and deployment scope for every protected operation. UI visibility is not authorization.

Platform identities are namespaced by platform and account. Unverified display names are never stable identity. Modules receive scoped actor references and capabilities only after kernel policy.

Privilege changes, credential changes, destructive operations, backup/restore, extension enablement, authorization denials, and access to participant content create audit events through the kernel audit capability. Each record contains an immutable ID, occurrence time, principal reference, action, resource class/reference, outcome, correlation, and previous-record digest for its audit stream. It excludes credentials and participant content.

Audit records are durable and append-only to ordinary runtime code. The deployment declares a retention period before first startup; the default is 180 days. Expiry is an auditable maintenance action, runs only after a backup satisfying the audit RPO, and never rewrites retained records. Verification recomputes the digest chain and treats a gap or mismatch as an integrity failure; digest chaining detects local tampering but does not claim protection from an administrator who controls both database and backups.

## Secrets

Secrets MUST NOT be committed, embedded in release artifacts, accepted through ordinary command-line arguments, returned by management reads, or written to logs, traces, metrics, panic reports, or error details.

Configuration stores secret references. Resolution occurs through an explicit provider with least filesystem or network privilege. Secret values have bounded lifetime in memory, are not unnecessarily copied, and are redacted structurally rather than by best-effort string replacement.

Rotation behavior is declared per credential. If safe reload is unsupported, rotation uses a bounded restart. Missing or unreadable required secrets fail startup without echoing the value.

### Telegram authorization and session secrets

Telegram authorization first reuses the opaque gotd session when it is valid. If the account is unauthorized, an injected authenticator supplies the phone number, delivered login code, and, when Telegram requires it, the 2FA password through cancellable callbacks. Supported code delivery metadata is limited to app, SMS, call, flash-call, and missed-call challenges. Signup, QR, passkey, payment-required/Fragment, email verification, OAuth, and unknown authorization paths fail explicitly as unsupported; the runtime MUST NOT silently switch flows.

The application hash, phone number, login code, 2FA password, and session bytes are secrets. They MUST NOT be supplied in command-line arguments or written to logs, traces, metrics, errors, diagnostic exports, or audit records. Callback results are consumed only by the authorization attempt and MUST NOT be persisted by Ex-Otogi. Session bytes remain opaque and MUST NOT be copied except by the encrypted paired backup procedure in [`operations.md`](operations.md#telegram-state-backup-and-restore).

## Data minimization and privacy

The runtime collects and retains only data required by an approved capability. Raw message content is excluded from routine telemetry and audit evidence. Persisted sensitive fields have an owner, purpose, retention rule, deletion behavior, and backup treatment.

Diagnostic exports require an explicit operator action, enumerate included categories before creation, and apply redaction. Access to participant content is recorded through the audit capability described above.

Telegram durable payloads, raw quarantine records, locators, send intents, and session data require deployment-defined retention and erasure schedules. Erasure MUST cover the account's SQLite rows, SQLite sidecars, session file, paired backups after their retention expires, and temporary restore material. Deleting only the SQLite database or only the session is incomplete account erasure. Operators preserve legally or operationally required audit evidence under its independent retention rule and record the erasure outcome without participant content or credentials.

## Resource and parser safety

Every external input path defines maximum bytes, collection count, nesting depth, decompressed size, processing time, and concurrency. Limits are enforced before expensive allocation, decoding, regular-expression evaluation, or downstream calls.

Parsers reject trailing garbage, duplicate security-sensitive fields, invalid Unicode where identifiers require canonical form, numeric overflow, and unsupported schema versions. Archive paths and filenames are treated as hostile. URLs are validated against allowed schemes and destinations before server-side requests.

## Network and platform security

Production listeners use authenticated encryption unless bound to a deliberately local protected transport. TLS verification is enabled and host identity is checked. Insecure modes require a development-only build or explicit local flag and cannot be enabled silently by configuration fallback.

Outbound destinations are capability-scoped. Redirects, proxies, and DNS resolution MUST NOT bypass destination policy. Timeouts and response-size limits apply to every external request.

## Dependencies and build security

Dependencies are minimized, version-resolved reproducibly, and reviewed for maintenance, license, transitive weight, and vulnerability exposure. `govulncheck` or an equivalent Go call-aware scan runs in the release gate. A reachable known vulnerability blocks release unless a time-bounded, owner-assigned risk acceptance documents exposure and mitigation.

Release artifacts include source revision, dependency/SBOM inventory, checksums, signature, and build provenance as defined in `quality.md`. Signing credentials are unavailable to untrusted build steps. Published artifacts are built by CI from reviewed source, not a developer workstation.

## Vulnerability handling

The project maintains a private reporting path and response owner. A report is triaged for affected versions, reachability, exploit preconditions, confidentiality/integrity/availability impact, and remediation urgency. Fixes address the root cause, add regression evidence, and include coordinated disclosure and upgrade guidance when users are affected.

Secrets exposed by an incident are rotated; deleting them from source history alone is insufficient. Security logs preserve evidence without increasing sensitive-data exposure.

## Secure development evidence

Changes at trust boundaries require negative tests for malformed, oversized, unauthenticated, unauthorized, replayed, and timeout cases. Parsers and decoders with meaningful input space maintain fuzz targets. Authentication and authorization tests assert denied behavior, not only successful access.

The release security record includes threat-model changes, vulnerability-scan results, SBOM, provenance verification, unresolved risk acceptances, and confirmation that diagnostic artifacts were checked for secrets.
