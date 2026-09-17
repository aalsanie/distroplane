# Security Policy

Distroplane treats release credentials, provider isolation, artifact identity, and retry correctness as security-sensitive design boundaries.

## Supported versions

Before the first stable release, security fixes are made on the current `main` branch. A version support matrix will be published before v1.0.

## Reporting a vulnerability

Please do not disclose suspected vulnerabilities in a public issue.

Use GitHub's private vulnerability reporting / Security Advisory flow for this repository when available. If private reporting is unavailable, open a public issue containing no vulnerability details and request a private contact channel.

Include, when possible:

- affected commit or version;
- impact;
- minimal reproduction;
- whether credentials or release integrity may be affected;
- suggested mitigation if known.

Reports involving secret exposure or unsafe duplicate publication are treated as release-blocking until assessed.
