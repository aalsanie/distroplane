# GitHub Actions integration

The composite Action calls the same CLI used locally. It supports `publish`, `resume`, `plan`, `apply`, and `reconcile`; [`action.yml`](../action.yml) defines the complete input/output contract. See the [single-job example](../README.md#github-actions).

## Installation and version binding

An invocation without `binary` installs the verified unified CLI/provider release archive for the runner's OS/architecture, checking its `SHA256SUMS` entry, safe members, embedded package identity, and executable hashes. Old standalone releases retain checksum-verified compatibility. The Action requires PowerShell 7; release targets are Linux, macOS, and Windows on amd64/arm64. Homebrew and WinGet providers additionally require Git.

Once published, `uses: aalsanie/distroplane@v0.9.0` selects that binary release by default. When pinning the Action to a commit SHA, set `version: 0.9.0` (or another published binary release) explicitly. A supplied `binary` skips managed installation and verification.

## One-step publishing

Until a release tag includes this Action interface, use `@main` with an explicit published binary `version`; pin to the versioned Action tag when available.

An unattended publication requires an explicit approval. A high-level Action
invocation uses the same immutable-plan, durable-journal, and reconciliation
logic as the native CLI; the Action does not implement its own publisher.

```yaml
- id: publish
  uses: aalsanie/distroplane@main
  env:
    NPM_TOKEN: ${{ secrets.NPM_TOKEN }}
  with:
    version: 0.9.2
    command: publish
    config: distroplane.json
    approve: 'true'
    upload-recovery: 'true'
    credential-mappings: npm-publish=NPM_TOKEN
- if: ${{ steps.publish.outputs.completed == 'true' }}
  run: echo "Publication completed"
```

Instead of `approve: 'true'`, a reviewed plan may be authorized using
`confirm-plan: <exact plan ID>`. An unapproved `publish` fails before
dispatch. `resume` locates a pre-existing run; it never creates a new one.
`resume` may also accept `release`, `run-id`, or `reconcile-only: 'true'`.

## Verified cross-job recovery

`upload-recovery: 'true'` uploads an opt-in, versioned `recovery.zip` with
the project configuration, its declared local artifacts, retained provider
binaries, run catalog, plans, and journals, plus exact SHA-256/size/mode
records. The bundle may contain proprietary build artifacts and release
metadata; enable it only for appropriately protected workflow artifacts.
It does not contain environment variable values or fetch credentials.
A recovering job can use:

```yaml
- uses: actions/checkout@v7
- id: resume
  uses: aalsanie/distroplane@main
  with:
    version: 0.9.2
    command: resume
    config: distroplane.json
    restore-recovery: 'true'
    recovery-artifact-name: distroplane-recovery
```

Restore validates every member and digest *before* touching the workspace.
It refuses corrupted, missing, symlinked, conflicting, or unexpected files.
Recovery is supported only on the **same OS, CPU architecture, and exact
`GITHUB_WORKSPACE` absolute path**. This is deliberate: Distroplane plans
bind absolute artifact and provider paths and cannot safely be moved to a
different runner layout. Files outside the workspace cannot be bundled.
When using `binary`, provide a workspace-local path or install the same
verified binary release again in the recovering job. Resume still validates
the original provider hashes and journal history. A recovery artifact is a
transport mechanism, not a trusted signature or an authorization to republish.

Use distinct `recovery-artifact-name` values for multiple runs in a workflow,
and persist the bundle after a pending result. An upload/download failure must
not be treated as permission to start a new run.

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

Exit code `4` becomes a successful Action step with `pending=true`, **not** a completed release. Exit `5` sets `rejected=true`; exit `6` or a usage/operational failure sets `failed=true` (and the Action fails). Downstream jobs should check `completed`/`pending` and retain recovery inputs. Key outputs include `plan-path`, `plan-id`, `run-id`, `state-path`, `journal-path`, `completed`, `pending`, `failed`, `rejected`, `exit-code`, `result-json`, and the opt-in recovery artifact ID, URL, and digest. `upload-evidence: 'true'` uploads the exported evidence bundle after apply/reconcile when a journal exists; it does not upload the journal itself. Use unique artifact names for multiple runs.

## OIDC

`oidc: required` only checks whether GitHub exposed OIDC request environment variables; it does **not** obtain an ID token. For npm trusted publishing, grant `id-token: write`, explicitly request a token with audience `npm:registry.npmjs.org`, and map it to the configured provider credential reference. See [npm authentication](../providers/npm/README.md#authentication).
