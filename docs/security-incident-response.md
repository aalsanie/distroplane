# Release security and incident response

**Owners:** The repository maintainer acts as incident commander and release owner until named deputies are appointed. GitHub repository administrators control Actions and release credentials; the maintainer coordinating with destination owners controls provider/token revocation. Record the actual accountable humans and their approval before a v1.0.0 release. No automation silently deletes or replaces an external publication.

## Containment: suspected compromise or incorrect publication

1. **Stop new dispatch.** Disable affected scheduled/manual publishing workflows and revoke the relevant GitHub environment approval; do not start a new Distroplane `publish` or `apply`. Quarantine any affected provider executable, release bundle, Action reference, Maven bundle, and GitHub credential. Prevent unreviewed `main` changes or new release tags while investigating. Preserve logs before expiration and redact secrets before sharing.
2. **Protect active runs.** Copy the project-owned `.distroplane` catalog, journal, plans, retained providers, original artifacts, and approved configuration to controlled storage; preserve timestamps and checksums. Do not delete or rewrite a journal to force a rerun. A dispatched operation with unknown outcome is uncertain even when the remote destination looks absent.
3. **Scope exposure.** Identify exact source commits, immutable release tags, affected asset SHA-256 values, GitHub Action references, Maven coordinates, Homebrew formula commits, WinGet PR/catalog entries, provider digests, token IDs, and destination release identifiers. Use GitHub Actions audit and provider-side logs where available. Treat checksums distributed with the same compromised release as **non-independent** evidence.
4. **Notify privately.** Use [private vulnerability reporting](../SECURITY.md). Coordinate disclosure with the repository maintainer and affected registry/catalog owners. Do not publish credentials, journal payloads, or unredacted CI logs.

## Credential and signing key rotation

- Revoke the affected token at the destination **first** (npm, SDKMAN, GitHub PATs, Sonatype Central), then replace the corresponding GitHub environment/repository secret. Remove old deployment access and audit recent use. Do not paste new credentials into command lines or logs.
- If the Maven signing key may be compromised, revoke or retire it under the OpenPGP policy, publish the updated public-key state, create a replacement key, update the protected `maven-central` environment, and validate signatures before the next approved publication. A GitHub release checksum is not a substitute for the signing-key decision.
- If Windows code-signing material may be compromised, revoke the certificate with the CA and prevent `pfx` release-mode usage until new certificate controls are approved. When shipping `unsigned` MSI packages, explicitly disclose that no Authenticode publisher identity exists.
- After rotating secrets, use a designated **nonproduction fixture** to verify credential scoping and redaction. Revoke fixture tokens afterward. Previous private journals must not be attached to a new plan as a shortcut.

## Withdrawal and package channel actions

| Channel | Immediate action | Limitation |
| --- | --- | --- |
| GitHub releases/Action | Halt publishing; restrict compromised download and tag references, coordinate safe replacement with consumers | Deleting a release does not revoke downloaded binaries or cached Actions; never silently move an immutable tag |
| Homebrew custom tap | Stop merging formula updates; submit a tested revert or fix with verified asset hashes | Existing installations retain local files; a PR is not installed availability |
| Windows MSI/WinGet | Warn consumers, coordinate manifest removal/update with Microsoft, and verify publisher signature policy | Microsoft catalog review/propagation is external; a GitHub PR merge is not client availability |
| Maven Central | Contact Sonatype for malicious-content response; publish a corrected **new** version where authorized | Published immutable coordinates cannot be overwritten by republishing the same version |
| npm / SDKMAN | Use the destination's withdrawal/yank/revocation procedure with its owner; verify observed downstream state | API acceptance or PR state alone does not prove consumer availability or complete withdrawal |

## Recovery after provider or artifact compromise

A reviewed plan binds exact artifact bytes and provider executable digest. `resume` and `reconcile` require the original trusted retained provider; they must **not** substitute an upgraded provider. If the retained binary is suspected compromised, quarantine execution and require a release-owner decision: restore the originally vetted bytes from an independently authenticated backup, or keep the run blocked and reconcile destination state outside Distroplane. Never disable digest checks or silently create a replacement run. Preserve proof of any externally published side effect before considering a new release.

Inspect existing state read-only with `distroplane status --config distroplane.json --json`; use `distroplane resume --config distroplane.json --reconcile-only --json` **only after** the retained provider and its credential path are trusted again. Reconciliation is not necessarily free of external authentication calls, and provider-reported state can be weaker than consumer availability.

## Incident drill and closure evidence

Before releasing v1.0.0, the release owner must record a tabletop drill with: incident date and participants, affected immutable identifiers, responsible persons for each channel, token rotation rehearsal, halt and restore actions, log redaction, journal preservation, rollback/withdrawal decisions, and approval. A written runbook alone is **not** evidence that the drill occurred. Include the private incident tracking reference in the restricted release signoff, not public secrets or an invented approval.
