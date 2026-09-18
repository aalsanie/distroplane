# GitHub Actions integration

Distroplane ships a composite GitHub Action at the repository root. The Action is a thin wrapper around the released CLI: provider planning, publication, reconciliation, credential handling, and evidence semantics remain in Distroplane and its out-of-process providers.

## Basic workflow

A workflow can plan and apply without embedding provider-specific publication commands:

```yaml
permissions:
  contents: read

jobs:
  distribute:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7

      - id: plan
        uses: aalsanie/distroplane@v0.1.0
        with:
          command: plan
          config: distroplane.json

      - id: apply
        uses: aalsanie/distroplane@v0.1.0
        with:
          command: apply
          config: distroplane.json
          plan: ${{ steps.plan.outputs.plan-path }}
          journal: .distroplane/run.journal
          upload-evidence: 'true'
```

Use a released tag or an immutable commit for the Action itself. When the Action is invoked from a `v...` tag, that tag is also used to resolve release binaries. When the Action is pinned by commit, pass `version` explicitly.

The installer downloads `SHA256SUMS`, the matching CLI binary, and by default every provider binary listed for the same release/OS/architecture. Every downloaded executable is SHA-256 verified before execution. Installed provider binaries are added to `PATH`, so an official provider configuration does not need a workflow step that invokes or installs that provider manually.

The `binary` input bypasses release installation and is intended for repository development and smoke tests.

## Commands and outputs

The Action supports `plan`, `apply`, and `reconcile`. It always invokes the CLI in JSON mode and exposes:

- `plan-id` and `plan-path`;
- `run-id`, `completed`, and `pending`;
- the native Distroplane `exit-code`;
- `result-json` for the complete normalized result;
- evidence path/digest and GitHub artifact metadata when evidence is requested.

Distroplane exit code `4` is a normal asynchronous state. The Action reports `pending=true` and succeeds so a later workflow or scheduled job can reconcile it. Rejected, failed, invalid, and operational outcomes still fail the Action after evidence upload has had a chance to run.

## Evidence artifacts

Set either `evidence-path` or `upload-evidence: 'true'` on `apply` or `reconcile`. With upload enabled, the Action exports the P13 evidence bundle and uploads it with the pinned GitHub artifact action.

```yaml
- id: apply
  uses: aalsanie/distroplane@v0.1.0
  with:
    command: apply
    config: distroplane.json
    plan: ${{ steps.plan.outputs.plan-path }}
    journal: .distroplane/run.journal
    evidence-path: .distroplane/evidence.json
    upload-evidence: 'true'
    evidence-artifact-name: release-evidence
```

Optional `attestations` are newline-delimited `NAME=URI` references and are passed to `distroplane evidence`. Distroplane does not implement a parallel signing system; CI-native attestation/signing can use the exported evidence digest and artifact.

## Credentials

Credentials remain environment variables resolved by Distroplane. The Action accepts only reference-to-environment mappings, never secret values:

```yaml
- uses: aalsanie/distroplane@v0.1.0
  env:
    NPM_TOKEN: ${{ secrets.NPM_TOKEN }}
  with:
    command: apply
    config: distroplane.json
    plan: ${{ steps.plan.outputs.plan-path }}
    journal: .distroplane/run.journal
    credential-mappings: |
      npm-publish=NPM_TOKEN
```

Prefer GitHub environments for targets with different trust boundaries. Put each credential boundary in its own job/environment and give that job only the secrets and permissions it needs. Distroplane still supports local multi-target execution; job isolation is a CI security recommendation, not a core semantic requirement.

## OIDC

OIDC remains a GitHub workflow primitive. The Action does not mint, persist, or proxy OIDC tokens. Set `oidc: required` when a provider/job expects GitHub OIDC; the Action then fails early unless GitHub exposed its native OIDC request environment.

```yaml
jobs:
  publish:
    environment: production
    permissions:
      contents: read
      id-token: write
    steps:
      - uses: actions/checkout@v7
      - uses: aalsanie/distroplane@v0.1.0
        with:
          command: apply
          oidc: required
          config: distroplane.json
          plan: .distroplane/plan.json
          journal: .distroplane/run.journal
```

The P14 smoke workflow includes both the denied case (no `id-token: write`) and the granted case.

## Plan and artifact handoff

When planning and applying in separate jobs, upload the persisted plan together with the exact release artifacts and restore them before apply. The repository smoke workflow exercises this handoff on Linux, macOS, and Windows, then separately downloads the evidence artifact produced by the Action.

A provider-specific publish command should not appear in workflow YAML. Provider configuration belongs in Distroplane configuration; provider implementations remain isolated executables speaking the protocol.
