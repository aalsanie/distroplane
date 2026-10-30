# Security model

Distroplane trusts the local OS account, filesystem, configured provider executables, and destination services. A provider runs as a subprocess of the invoking user: this narrows environment/credential exposure, but **does not** isolate filesystem or network access. Do not run untrusted providers or pull-request configuration with publication secrets.

## Integrity and execution boundaries

Plans bind artifact hashes and sizes, provider identity/version, and provider executable SHA-256. Execution verifies artifact contents and the selected provider binary. This protects against accidental changes between planning and execution; plans are content-addressed but **not signed**. Protect reviewed plans and configuration at the approval boundary.

Before handing a side-effecting request to a provider, the host persists a journal dispatch event. Unknown post-dispatch outcomes require reconciliation. The journal is checksummed, synchronized, and locked against concurrent writers, but an attacker who controls its files can rewrite the history. Evidence exports inherit that limitation and are not signed or independently verified against destinations.

## Credentials

Providers receive an allowlisted process environment plus only the credential references declared for that invocation. The host rejects environment-name collisions and resolves secrets immediately before execution. Journal/evidence records carry credential reference metadata, not intentional secret values; exact resolved secret bytes are redacted from handled errors, diagnostics, and result fields.

These measures do not prevent a malicious provider from reading accessible files, using the network, or encoding secrets to evade exact-value redaction. Never store passwords or tokens directly in opaque provider configuration or payloads: configuration and intent are persisted in plans.

## Release assets and GitHub Actions

The Action downloads executables over HTTPS and verifies each against `SHA256SUMS` from the same **unsigned** GitHub release. This is not an independent supply-chain signature. Supplying the Action's `binary` input bypasses managed installation and checksum verification.

`oidc: required` checks the availability of GitHub's OIDC request environment; it does not request or validate an ID token. The npm provider can exchange a separately provided npm-audience token for trusted publication.

Provider-reported `PUBLISHED` is not uniform proof of downstream availability. SDKMAN may report API acceptance; WinGet requires subsequent destination observation. See the [provider guides](configuration.md). Report vulnerabilities using [private vulnerability reporting](../SECURITY.md).
