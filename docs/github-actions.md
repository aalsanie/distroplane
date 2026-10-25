# GitHub Actions integration

The composite Action calls the same CLI used locally. It supports `plan`, `apply`, and `reconcile`; [`action.yml`](../action.yml) defines the complete input/output contract. See the [single-job example](../README.md#github-actions).

## Installation and version binding

An invocation without `binary` downloads the CLI and selected official providers for the runner's OS/architecture from a GitHub release and checks each executable against that release's `SHA256SUMS`. The Action requires PowerShell 7; release targets are Linux, macOS, and Windows on amd64/arm64. Homebrew and WinGet providers additionally require Git.

Once published, `uses: aalsanie/distroplane@v0.9.0` selects that binary release by default. When pinning the Action to a commit SHA, set `version: 0.9.0` (or another published binary release) explicitly. A supplied `binary` skips managed installation and verification.

## Plan handoff across jobs

The optional `upload-plan: 'true'` artifact contains **only the plan file**. It does not capture release artifacts, configuration, provider binaries, or a journal. The plan retains absolute artifact paths and provider digests: jobs separated by approval or runner lifetime must restore the exact bytes at the exact paths on the same OS/architecture. Changing these inputs requires a new plan and review.

Persist the journal after `apply` before the runner is destroyed. A later job can restore all inputs and run:

```yaml
- uses: aalsanie/distroplane@v0.9.0
  env:
    NPM_TOKEN: ${{ secrets.NPM_TOKEN }}
  with:
    command: reconcile
    plan: reviewed-plan/PLAN.json
    journal: run.journal
    credential-mappings: npm-publish=NPM_TOKEN
    upload-evidence: 'true'
```

Replace the example paths with the restored paths. Reconciliation never publishes; run `apply` afterward if new undispatched work is ready.

## Outputs and pending runs

Exit code `4` becomes a successful Action step with `pending=true`, **not** a completed release. Downstream jobs should check `completed`/`pending` and retain recovery inputs. Key outputs include `plan-path`, `plan-id`, `run-id`, `completed`, `pending`, `exit-code`, and `result-json`. `upload-evidence: 'true'` uploads the exported evidence bundle after apply/reconcile when a journal exists; it does not upload the journal itself. Use unique artifact names for multiple runs.

## OIDC

`oidc: required` only checks whether GitHub exposed OIDC request environment variables; it does **not** obtain an ID token. For npm trusted publishing, grant `id-token: write`, explicitly request a token with audience `npm:registry.npmjs.org`, and map it to the configured provider credential reference. See [npm authentication](../providers/npm/README.md#authentication).
