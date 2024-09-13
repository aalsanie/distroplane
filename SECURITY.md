# Security Policy

## Supported versions

Distroplane is pre-1.0 and does not yet publish a long-term support matrix. Security fixes are applied to the latest development line. A formal supported-version policy will be published before v1.0.

## Reporting a vulnerability

Prefer GitHub private vulnerability reporting for this repository when available. Do not disclose exploitable details in a public issue.

If private vulnerability reporting is unavailable, open a minimal public issue asking the maintainer for a private reporting channel without including exploit details, secrets, proof-of-concept payloads, or sensitive environment information.

## Security principles

Distroplane is designed around least privilege:

- provider implementations execute out of process;
- credentials are scoped to the provider that requires them;
- secrets must never be persisted in plans or journals;
- side effects must be idempotency-aware;
- ambiguous publication outcomes must be reconciled rather than blindly retried.
