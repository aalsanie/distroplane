# Security model

Distroplane trusts the invoking OS account, local filesystem, selected provider executables, and configured publication destinations. Provider processes reduce accidental credential exposure; they are not a filesystem or network sandbox.

## Providers and credentials

Planning records provider identity/version/capabilities and executable SHA-256. Apply and reconcile verify the selected executable digest before dispatch. A malicious provider can still lie about identity, results, or exfiltrate credentials/files available to the same OS account.

Provider processes receive an allowlisted runtime environment: `PATH`, applicable temporary-directory variables, and Windows runtime variables. Unrelated parent variables are not inherited. Declared credentials are resolved immediately before execution and added only for the invoked provider; name collisions are rejected.

Journal/evidence records contain credential reference metadata, not secret values. Exact resolved values are redacted from returned provider errors, diagnostics, state, and evidence where handled by the host. This cannot prevent deliberate encoding/exfiltration or secrets placed directly in opaque provider configuration.

Use separate jobs/environments for separate trust boundaries and do not execute untrusted pull-request configuration/providers in a job with publication credentials.

## Plans, artifacts, and journal

Plans bind artifact hashes/sizes and provider executable digests to saved intent. Apply rechecks artifacts and provider binaries. Plans are content-addressed, not signed; protect reviewed plans/configuration in the approval system.

The journal is framed, checksummed, sequence-validated, and synchronized on append. These checks detect corruption, not malicious rewriting by an attacker with filesystem access. Evidence has the same limitation and is not signed by Distroplane.

The executor records dispatch before releasing a side-effecting request. A timeout or cancellation after dispatch can leave the external result unknown; reconciliation is then required. Distroplane does not provide a general exactly-once or rollback guarantee.

Provider results have provider-specific meaning. See the [provider guides](../README.md#providers) before treating evidence as proof of destination availability.

## Action installation and OIDC

The Action downloads release binaries over HTTPS and verifies them against that release's `SHA256SUMS`. The checksum manifest is fetched from the same unsigned GitHub release. Supplying `binary` bypasses installation and checksum verification.

`oidc: required` checks only whether GitHub exposed its OIDC request environment. It does not request, validate, or persist an ID token. The npm provider can exchange a separately supplied npm-audience ID token.

Report vulnerabilities through [SECURITY.md](../SECURITY.md).
