# Security boundaries

- **Providers:** Executables run with the invoking user's filesystem and network permissions. Use trusted providers and protected configuration.
- **Credentials:** Pass secrets through environment variables and explicit credential references. The host limits process environment exposure and redacts exact secret values in handled diagnostics, but a malicious provider can still access data available to its OS account.
- **Plans:** Artifact and provider executable digests are verified at execution. Plans are not signed; protect reviewed plans and their files.
- **Recovery:** A durable journal records dispatched side effects. Unknown outcomes require reconciliation. Journal checksums detect corruption, not malicious rewriting by someone with filesystem access.
- **Downloads:** Release `SHA256SUMS` verify archive integrity, but checksums fetched from the same unsigned release are not independent publisher signatures. The GitHub Action's `binary` input bypasses managed binary verification.
- **External status:** Provider-reported completion may reflect API acceptance or pending review rather than public availability. Check the destination when its result is uncertain.

Report vulnerabilities through [private GitHub Security Advisories](../SECURITY.md).
