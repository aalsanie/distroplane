# GitHub Actions

The composite Action runs `plan`, `apply`, or `reconcile` with the same CLI used locally. See the [README](../README.md#github-actions) for a minimal plan/apply example.

## Installation

Unless `binary` is supplied, each invocation installs the selected Distroplane release for the runner's OS/architecture. It downloads `SHA256SUMS`, the CLI, and official providers, verifies each executable against the checksum file, and adds the install directory to `PATH`.

Using a release tag such as `aalsanie/distroplane@v0.9.0-rc.2` selects the matching binary release when `version` is omitted. If the Action is pinned to a commit instead, set `version` explicitly to the binary release you intend to run.

Supported release targets are Linux, macOS, and Windows on amd64/arm64. The Action requires PowerShell 7. Providers may have additional requirements; Homebrew and WinGet require Git.

## Plan handoff

`upload-plan: 'true'` uploads the generated plan as an Actions artifact. If approval separates planning from execution, also preserve the exact configuration, release artifacts, provider binaries when custom providers are used, and later the journal.

Plans contain absolute artifact paths and provider digests. Restore the same bytes at the same paths on the same OS/architecture before applying a reviewed plan. Rebuilding an artifact or regenerating the plan changes what was reviewed.

The built-in plan upload contains only the plan. Evidence upload contains only the evidence bundle. Neither preserves configuration, release artifacts, or the journal.

## Pending work

CLI exit code `4` is exposed by the Action as a successful step with `pending=true`; it does not mean all targets are published.

Persist the plan, inputs, and journal before the runner disappears. A later job can restore them and run:

```yaml
- uses: aalsanie/distroplane@v0.9.0-rc.2
  env:
    NPM_TOKEN: ${{ secrets.NPM_TOKEN }}
  with:
    command: reconcile
    plan: reviewed-plan/PLAN.json
    journal: run.journal
    credential-mappings: npm-publish=NPM_TOKEN
    upload-evidence: 'true'
```

Use the credentials required by the actual target. Reconciliation observes pending or ambiguous work and does not publish new content. If it unblocks operations that never started, run `apply` again.

## OIDC

`oidc: required` only verifies that GitHub exposed its OIDC request URL and credential to the job. It does not request an ID token or pass GitHub's request credential to a provider.

For npm trusted publishing, grant `id-token: write`, obtain an npm-audience ID token in the workflow, and map it to the provider's configured reference. See the [npm provider](../providers/npm/README.md#authentication).

## Inputs and outputs

[`action.yml`](../action.yml) is the authoritative input/output contract. Important outputs include `plan-path`, `run-id`, `completed`, `pending`, `exit-code`, `result-json`, and evidence metadata.

`upload-evidence: 'true'` exports and uploads evidence after apply/reconcile when a journal exists. Use distinct artifact names when a workflow creates more than one plan or evidence artifact.
