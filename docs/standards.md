# Standards Register

This register was reviewed on 2026-08-26. Ex-Otogi adopts only the decisions stated below; external documents do not silently expand repository scope.

## Normative references

### Requirements language

- [BCP 14: RFC 2119 and RFC 8174](https://www.rfc-editor.org/info/bcp14)

Adoption: capitalized conformance terms have BCP 14 meaning. Lowercase uses are ordinary prose.

### HTTP

- [RFC 9110: HTTP Semantics](https://www.rfc-editor.org/rfc/rfc9110)
- [RFC 9457: Problem Details for HTTP APIs](https://www.rfc-editor.org/rfc/rfc9457)
- [RFC 6585: Additional HTTP Status Codes](https://www.rfc-editor.org/rfc/rfc6585)
- [RFC 9111: HTTP Caching](https://www.rfc-editor.org/rfc/rfc9111)

Adoption: a future HTTP management binding preserves safe/idempotent method semantics, conditional requests, cache directives, `Retry-After`, and standard status meanings. Machine-readable errors use `application/problem+json` with stable absolute problem-type URIs. Rate limiting uses status `429`; no non-standard rate-limit header is required by this blueprint.

### Telemetry

- [OpenTelemetry Specification](https://opentelemetry.io/docs/specs/otel/)
- [W3C Trace Context](https://www.w3.org/TR/trace-context/)

Adoption: traces, metrics, logs, resources, context propagation, and semantic naming follow stable OpenTelemetry specifications. Experimental semantic conventions require an explicit repository decision before becoming a compatibility contract. Remote trace state is untrusted input.

## Engineering guidance adopted as policy

### Go

- [Go release policy and history](https://go.dev/doc/devel/release)
- [Go security policy and guidance](https://go.dev/doc/security/)
- [Go race detector](https://go.dev/doc/articles/race_detector)
- [Go fuzzing](https://go.dev/doc/security/fuzz/)
- [`govulncheck`](https://go.dev/doc/security/vuln/)

Adoption: builds use a stable Go release still receiving upstream security fixes. Race testing covers concurrent code; fuzzing targets parsers and trust boundaries; vulnerability findings are evaluated by reachable call paths and release exposure. A tool result is evidence, not permission to ignore a known risk.

### Secure development and supply chain

- [NIST SP 800-218, Secure Software Development Framework 1.1](https://doi.org/10.6028/NIST.SP.800-218)
- [SLSA 1.2](https://slsa.dev/spec/v1.2/)
- [CycloneDX specification](https://cyclonedx.org/specification/overview/)

Adoption: the repository implements concrete preparation, protection, production, and vulnerability-response controls from SSDF. Releases include a machine-readable SBOM, checksums, signatures, and build provenance. Release automation SHOULD reach SLSA Build L2; inability to do so MUST be disclosed with the artifact. Build L3 is a future hardening target, not a present conformance claim.

### SQLite

- [Write-Ahead Logging](https://www.sqlite.org/wal.html)
- [Online Backup API](https://www.sqlite.org/backup.html)
- [Atomic Commit](https://www.sqlite.org/atomiccommit.html)
- [`PRAGMA integrity_check`](https://www.sqlite.org/pragma.html#pragma_integrity_check)

Adoption: SQLite is a local-filesystem, single-node store. WAL configuration is verified rather than assumed; checkpoint pressure is observable; backups use an online database-aware snapshot mechanism; restore drills include integrity and application-schema checks. Copying only a live main database file is prohibited.

## Informative references

- [The Twelve-Factor App: Config](https://12factor.net/config) informs separation of deploy-specific configuration, but Ex-Otogi permits typed configuration files and secret references rather than requiring environment variables.
- [OWASP Application Security Verification Standard](https://owasp.org/www-project-application-security-verification-standard/) provides review prompts; `security.md` is the binding project policy.
- [Semantic Versioning 2.0.0](https://semver.org/) informs public contract versioning; Ex-Otogi does not promise a public compatibility surface until a contract is explicitly declared stable.

## Reference maintenance

A change that relies on a new revision MUST record the reviewed version or date and update the concrete Ex-Otogi rule it affects. Broken or superseded links are specification defects. A newer external version does not automatically change implementation requirements.
