# Security model

Distroplane coordinates release publication across external package and distribution systems. It deliberately keeps the core provider-neutral and runs providers as separate operating-system processes. This document describes the security boundary that model creates, the controls implemented by the repository, and the risks that remain.

## Assets

The security-sensitive assets are release artifacts, immutable plans, journal history, provider executables, provider credentials, evidence bundles, repository credentials used by Git-backed providers, and the external package or distribution accounts that providers can modify.

## Trust boundaries

The Distroplane core is trusted to validate configuration, bind artifacts and providers to immutable plan identity, sequence side effects, persist execution history, and redact credentials. Provider executables are separately versioned programs and are trusted only for the targets for which they are explicitly configured. External registries, package services, Git hosts, and review systems are remote authorities whose responses can fail, be delayed, or be compromised.

The local operating-system account and filesystem are part of the trusted computing base. Distroplane reduces exposure within that boundary, but it is not an operating-system sandbox.

## Threats and controls

| Threat | Control | Residual risk |
| --- | --- | --- |
| Provider executable replaced after planning | New plans include the SHA-256 digest of each provider executable in plan identity. Apply and reconcile verify the digest before provider use and recheck it before each invocation. | A same-user attacker that can replace an executable in the very small interval between the final digest check and OS process creation is inside the local host trust boundary. |
| Malicious provider executable | Providers run out of process with a minimal environment. Only declared credential material is injected. Provider stdout/stderr are bounded and protocol validated. | A provider explicitly given a credential can misuse or exfiltrate that credential. Distroplane does not sandbox provider filesystem or network access. |
| Compromised external registry or API | Side-effecting operations use stable idempotency keys where supported. Unknown post-dispatch outcomes become ambiguous and are reconciled before another publication attempt. Evidence records observed external state. | The external authority can lie about its own state or later mutate it. Evidence proves what Distroplane observed, not the integrity of a compromised remote service. |
| Artifact substitution | Plans bind artifact SHA-256 and size. Apply verifies the artifact again before execution. Providers that consume local artifact bytes validate the planned digest and size again. | A hostile local account with write access can race filesystem changes; local host integrity remains required. |
| Secret leakage | Credentials are resolved per declared requirement, unrelated ambient environment variables are not inherited by providers, secret material is redacted from provider errors/evidence, and in-memory material is cleared on release where practical. | Operating-system administrators, debuggers, crash tooling, or a provider intentionally given a credential can observe it. |
| Shell or command injection | Provider processes and Git commands are started with argument arrays rather than shell command strings. Identifiers, paths, URLs, and environment entries are validated. | External tools such as Git remain part of the trusted local toolchain. |
| Path traversal | Repository-relative provider paths are normalized and reject escapes. State files use caller-selected roots and deterministic internal names. | The configured state root itself is trusted input and can intentionally point anywhere the invoking account can write. |
| Symlink substitution | Git-backed providers reject unsafe repository path traversal and symlink cases before modifying managed files. Artifact hashing requires a regular file. | General filesystem aliasing outside provider-managed repository paths is governed by local host permissions. |
| Plan tampering | Plan identity is derived from semantic content, including artifact and provider digests. Loading recomputes identity and rejects mismatches or malformed content. | Anyone able to replace both the plan and the caller's intended configuration can change intent; deployment systems should preserve immutable plan artifacts. |
| Journal tampering or corruption | Journal records are framed, checksummed, sequenced, append-only, fsynced, and replay-validated. Truncated final records recover only to the last durable frame; corruption inside the durable prefix is rejected. | Distroplane does not cryptographically sign local journals. Protect journal storage with normal CI/workstation access controls. |
| Evidence tampering | Evidence is deterministically derived from the immutable plan and validated journal. Secret-bearing fields are rejected. | Exported evidence files are not signed by the core. Release systems can add external attestations or artifact signatures. |
| Untrusted pull-request execution | Repository workflows use read-only contents permissions by default, disable persisted checkout credentials, and grant OIDC/write permissions only to jobs that need them. | Maintainers must continue reviewing workflow changes before merging and must not expose production secrets to untrusted pull-request code. |

## Side-effect safety

A side-effecting operation has explicit durable boundaries for provider start, dispatch, provider response, and result. A failure before dispatch can be retried when classified retryable. Once dispatch is durable, an unknown outcome is not retried as a fresh publication. It is recorded as ambiguous and reconciliation is required.

Cancellation follows the same rule. Provider process trees are terminated on supported release platforms, but a cancellation after dispatch is still treated as an uncertain external outcome until reconciliation proves state.

## Accepted risks

Distroplane intentionally accepts the following risks:

- providers are trusted executable code and are not sandboxed;
- the local operating-system account, kernel, filesystem, installed Git client, and CA trust store are trusted;
- remote services remain authoritative for their own eventual state;
- journals and evidence are checksummed/validated but are not internally signed;
- core release provenance is supplied by the release platform rather than a custom signing service;
- compatibility with pre-release plans that do not contain provider digests is for inspection only; production execution should use a newly generated digest-bound plan.

Security reports should follow [SECURITY.md](../SECURITY.md).
