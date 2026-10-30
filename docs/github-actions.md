# GitHub Actions integration

The composite Action calls the same CLI used locally. It supports `publish`, `resume`, `plan`, `apply`, and `reconcile`; [`action.yml`](../action.yml) defines the complete input/output contract. See the [single-job example](../README.md#github-actions).

## Installation and version binding

An invocation without `binary` installs the verified unified CLI/provider release archive for the runner's OS/architecture, checking its `SHA256SUMS` entry, safe members, embedded package identity, and executable hashes. Old standalone releases retain checksum-verified compatibility. The Action requires PowerShell 7; release targets are Linux, macOS, and Windows on amd64/arm64. Homebrew and WinGet providers additionally require Git.

The published CLI version is `0.9.2`; the newer Action publishing interface is available on `main` until it is included in a later immutable release tag. Use a reviewed commit SHA with `version: 0.9.2` for a pinned workflow. Older `@v0.9.0` tags do not include later one-command publication and recovery inputs. A supplied `binary` skips managed installation and verification.

## One-step publishing

Until an immutable release tag includes the current Action interface, use a reviewed `@main` revision with an explicit published binary `version`; prefer pinning the Action to an audited commit SHA in production. Do not assume older tags expose current inputs.

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
- uses: aalsanie/distroplane@main
  env:
    NPM_TOKEN: ${{ secrets.NPM_TOKEN }}
  with:
    version: 0.9.2
    command: reconcile
    plan: reviewed-plan/PLAN.json
    journal: run.journal
    credential-mappings: npm-publish=NPM_TOKEN
    upload-evidence: 'true'
```

Replace the example paths with the restored paths. Reconciliation never publishes; run `apply` afterward if new undispatched work is ready.

## Outputs and pending runs

Exit code `4` becomes a successful Action step with `pending=true`, **not** a completed release. Exit `5` sets `rejected=true`; exit `6` or a usage/operational failure sets `failed=true` (and the Action fails). Downstream jobs should check `completed`/`pending` and retain recovery inputs. Key outputs include `plan-path`, `plan-id`, `run-id`, `state-path`, `journal-path`, `completed`, `pending`, `failed`, `rejected`, `exit-code`, `result-json`, and the opt-in recovery artifact ID, URL, and digest. `upload-evidence: 'true'` uploads the exported evidence bundle after apply/reconcile when a journal exists; it does not upload the journal itself. Use unique artifact names for multiple runs.

## npm trusted publishing with GitHub OIDC

Use a GitHub-hosted runner, register the exact repository/workflow as a trusted publisher on npmjs.com, and grant `id-token: write` in the publishing job. `oidc: npm` is an explicit authentication mode: it requests a **fresh** GitHub ID token with audience `npm:registry.npmjs.org` and maps it only to the specified trusted-publishing npm credential reference. It requires exactly one matching npm destination with `authentication.mode: trusted-publishing` and the official HTTPS npm registry.

`oidc: required` retains its previous behavior: it only checks runner permission and **does not** acquire or forward a token. `oidc: none` (default) makes no token request. Do not combine `oidc: npm` with a manual mapping or `credentialSources` entry for the same credential reference.

The following examples use the existing `distroplane.json` in the project and an already-built release tarball. The configuration's declared credential references and artifacts must match the example. Before running against npm, configure a trusted publisher for the workflow filename in npm package settings.

### npm token publication

```yaml
name: Release npm package
on:
  workflow_dispatch:
permissions:
  contents: read
jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6
      - id: publish
        uses: aalsanie/distroplane@main
        env:
          NPM_TOKEN: ${{ secrets.NPM_TOKEN }}
        with:
          version: 0.9.2
          command: publish
          approve: 'true'
          config: distroplane.json
          credential-mappings: npm-publish=NPM_TOKEN
      - if: ${{ steps.publish.outputs.completed == 'true' }}
        run: echo "Published"
```

Configure the npm destination with `authentication.mode: token` and `authentication.credential: npm-publish`. A pending result is not completed publication.

### npm OIDC trusted publication

```yaml
name: Trusted npm release
on:
  workflow_dispatch:
permissions:
  contents: read
jobs:
  publish:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      id-token: write
    steps:
      - uses: actions/checkout@v6
      - id: publish
        uses: aalsanie/distroplane@main
        with:
          version: 0.9.2
          command: publish
          approve: 'true'
          config: distroplane.json
          oidc: npm
          npm-oidc-ref: npm-publish
      - if: ${{ steps.publish.outputs.completed == 'true' }}
        run: echo "Published"
```

This destination must use `authentication.mode: trusted-publishing`. The Action fetches a new ID token on each invocation, including later `resume` or `reconcile` calls. The GitHub OIDC request bearer is never supplied to a provider. Neither token is stored in a plan, journal, recovery bundle, or Action output. A trusted-publisher setup on npmjs.com is required; local CLI invocation does not gain OIDC acquisition automatically.

### Multi-destination release

```yaml
name: Publish npm and Homebrew
on:
  workflow_dispatch:
permissions:
  contents: read
jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6
      - id: publish
        uses: aalsanie/distroplane@main
        env:
          NPM_TOKEN: ${{ secrets.NPM_TOKEN }}
          HOMEBREW_GITHUB_TOKEN: ${{ secrets.HOMEBREW_GITHUB_TOKEN }}
        with:
          version: 0.9.2
          command: publish
          approve: 'true'
          config: distroplane.json
          credential-mappings: |
            npm-publish=NPM_TOKEN
            homebrew-publish=HOMEBREW_GITHUB_TOKEN
          upload-recovery: 'true'
      - if: ${{ steps.publish.outputs.pending == 'true' }}
        run: echo "External review pending; retain recovery artifact"
      - if: ${{ steps.publish.outputs.completed == 'true' }}
        run: echo "All destinations completed"
```

Use two configured destinations with credential references `npm-publish` and `homebrew-publish`. This Action does not make an external Homebrew PR merge automatically count as completed publication.

### Reviewed plan and cross-job recovery

The [verified cross-job recovery](#verified-cross-job-recovery) workflow is exercised by the repository's `.github/workflows/action-smoke.yml` on Linux, macOS, and Windows. Job A runs `command: plan` with `upload-recovery: 'true'`, and exposes `plan-id`. After explicit approval, job B uses `command: publish`, `confirm-plan` set to that exact ID, and `restore-recovery: 'true'`. A pending run is resumed in a subsequent job with the original plan/run identity and no blind republish. The runner's workspace path, OS, and architecture must be identical for this handoff.
