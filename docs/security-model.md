# Security model

Distroplane trusts the invoking OS account, the local filesystem, the selected provider executables, and the configured publication destinations. Separate provider processes limit accidental credential exposure; they are not a filesystem or network sandbox.

## Provider execution and credentials

Planning records each provider's reported name/version, capabilities, and executable SHA-256. Apply and reconcile require those digests and verify the selected executable before use. The host checks the required protocol and capability through `describe`, then rechecks the executable digest before dispatch. A malicious provider can still lie about its identity or results.

Providers receive a limited environment and only the credentials requested by their planned target requirements. Ambient variables are not inherited wholesale, including unrelated provider secrets and GitHub's OIDC request credentials. External tools required by a provider remain part of the trusted local execution environment.

Credentials are supplied by reference-to-environment mappings, resolved just before execution, and delivered under the environment names declared by the provider. Resolution events record reference metadata, not values. The host redacts exact resolved values from returned errors, diagnostics, provider state, and evidence. Credential buffers are cleared where practical; this is not a guarantee against memory inspection or copies held by the runtime.

A provider given a credential can misuse it. It can also read files accessible to the same OS account. Redaction cannot prevent deliberate encoding/exfiltration or recognize every secret a user puts into opaque configuration. Keep tokens out of configuration, URLs, plan annotations, and attestation references. Use separate CI jobs/environments for distinct trust boundaries.

## Plans and artifacts

Plan loading recomputes semantic identity and rejects a mismatched plan ID. Artifact SHA-256 and size bind local release bytes to that intent; apply rechecks them before execution. npm also verifies the tarball inside the provider. Providers that publish download URLs use the local artifact hash as metadata; the core does not download those URLs to verify the hosted bytes.

Plans are content-addressed, not signed. An attacker who can replace both a plan and its expected identity can supply different intent. Protect the reviewed plan and configuration in your approval system. Core artifact source paths are excluded from semantic identity, so a plan hash alone does not authenticate every stored path. Paths within opaque provider payloads may be included.

Executable and artifact hashing does not eliminate a same-user race between verification and use. Provider digests identify exact bytes; they do not establish publisher trust or replace release verification. Plans without executable digests can be inspected, but the CLI rejects them for execution.

## Journal and external outcomes

Journal events are sequenced, framed, checksummed, and replay-validated. Each successful append synchronizes the file. Writers hold an OS lock; Unix additionally synchronizes the containing directory when required. Directory synchronization is skipped on Windows, so power-loss behavior also depends on that filesystem and OS.

An incomplete last frame recovers to the valid prefix. Corruption in a complete frame is rejected. These checks detect damage; they are not cryptographic authentication against an attacker who can rewrite the file and its checksums. Protect the journal and exported evidence with normal storage access controls.

The executor records dispatch before releasing the request to the provider. An unknown outcome after dispatch becomes ambiguous and requires reconciliation. A timeout or process cancellation cannot undo an external request that was already accepted. There is no general exactly-once publication or rollback guarantee.

Provider results are claims about the remote system. npm upload acceptance, SDKMAN vendor acceptance, and WinGet repository state do not prove the same thing. See the [provider guides](../README.md#providers) for current limitations. Evidence records those results; export does not query the service again or sign the bundle.

## Action installation and OIDC

The Action downloads an exact release's CLI and provider binaries over HTTPS and verifies each against that release's `SHA256SUMS` before executing it. The manifest is fetched from the same GitHub release and is unsigned. The installer does not verify release attestations. `binary` bypasses downloading and checksum checks.

Pin the Action to a commit and set an explicit binary `version` when you need stable Action code. The release assets remain a separate trust input. [Release verification](release-verification.md) describes the files and source metadata.

`oidc: required` only checks that GitHub exposed its token-request URL and credential. It does not request, validate, or persist an ID token. The npm provider can exchange a separately supplied ID token with npm; see [trusted publishing](../providers/npm/README.md#trusted-publishing). Account trust configuration, audience selection, and short-lived token acquisition remain workflow responsibilities.

## Operational limits

Provider requests, stdout, and stderr are bounded; operations have deadlines and cancellation handling. The host invokes executables directly without shell command interpolation. External tools such as Git still interpret their own arguments and configuration, so only use trusted provider settings and repository URLs.

These controls do not isolate hostile code on a shared runner. Do not execute an untrusted pull request's providers or configuration in a job containing publication credentials. Prefer protected environments and short-lived credentials where the destination supports them.

Report vulnerabilities through [SECURITY.md](../SECURITY.md).
