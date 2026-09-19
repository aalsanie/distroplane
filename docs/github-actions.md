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
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1

      - id: plan
        uses: aalsanie/distroplane@v0.5.0
        with:
          command: plan
          config: distroplane.json

      - id: apply
        uses: aalsanie/distroplane@v0.5.0
        with:
          command: apply
          config: distroplane.json
          plan: ${{ steps.plan.outputs.plan-path }}
          journal: .distroplane/run.journal
          upload-evidence: 'true'
```

Use a released tag or an immutable commit for the Action itself. When the Action is invoked from a `v...` tag, that tag is also used to resolve release binaries. When the Action is pinned by commit, pass `version` explicitly.

The installer downloads `SHA256SUMS`, the matching CLI binary, and by default every provider binary listed for the same release/OS/architecture. Every downloaded executable is SHA-256 verified before execution. Versioned release assets are installed under canonical executable names (`distroplane` and `distroplane-provider-<name>`) and added to `PATH`, so provider discovery works without provider-specific installation commands in workflow YAML.

The `binary` input bypasses release installation and is intended for repository development and smoke tests.

## Commands and outputs

The Action supports `plan`, `apply`, and `reconcile`. The optional `concurrency` input is forwarded to apply/reconcile and defaults to the executor's normal bounded-concurrency behavior. Set `upload-plan: 'true'` when the immutable plan should be handed to another job as a GitHub artifact. The Action always invokes the CLI in JSON mode and exposes:

- `plan-id`, `plan-path`, and uploaded plan artifact metadata when requested;
- `run-id`, `completed`, and `pending`;
- the native Distroplane `exit-code`;
- `result-json` for the complete normalized result;
- evidence path/digest and GitHub artifact metadata when evidence is requested.

Distroplane exit code `4` is a normal asynchronous state. The Action reports `pending=true` and succeeds so a later workflow or scheduled job can reconcile it. Rejected, failed, invalid, and operational outcomes still fail the Action after evidence upload has had a chance to run.


## Compatibility

The beta integration has an explicit compatibility contract:

| Action major | Distroplane CLI | Provider protocol | Support |
| --- | --- | --- | --- |
| `v0` | `0.5.x` beta line | `1` candidate | Supported beta integration |

The Action resolves the exact CLI version from a versioned Action ref by default. When the Action is pinned by immutable commit, pass `version` explicitly. Cross-major Action/CLI compatibility is not promised; protocol major mismatches remain rejected by Distroplane before provider side effects.

GitHub Actions is optional. The same `plan`, `apply`, `status`, and `reconcile` CLI workflow remains available locally and in other CI systems.

## Evidence artifacts

Set either `evidence-path` or `upload-evidence: 'true'` on `apply` or `reconcile`. With upload enabled, the Action exports the normalized evidence bundle and uploads it with the pinned GitHub artifact action.

```yaml
- id: apply
  uses: aalsanie/distroplane@v0.5.0
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
- uses: aalsanie/distroplane@v0.5.0
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

Prefer GitHub environments for targets with different trust boundaries. Put each credential boundary in its own job/environment and give that job only the secrets and permissions it needs. Start with `permissions: contents: read`; add `id-token: write` only to jobs whose provider requires OIDC. Distroplane still supports local multi-target execution; job isolation is a CI security recommendation, not a core semantic requirement.

When credentials must be isolated, use target-scoped Distroplane configuration/plan pairs in separate jobs rather than giving one job every provider secret. For example:

```yaml
jobs:
  npm:
    environment: npm-production
    permissions:
      contents: read
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - id: plan
        uses: aalsanie/distroplane@v0.5.0
        with:
          command: plan
          config: distroplane.npm.json
      - uses: aalsanie/distroplane@v0.5.0
        env:
          NPM_TOKEN: ${{ secrets.NPM_TOKEN }}
        with:
          command: apply
          config: distroplane.npm.json
          plan: ${{ steps.plan.outputs.plan-path }}
          journal: .distroplane/npm.journal
          credential-mappings: npm-publish=NPM_TOKEN

  vendor:
    environment: vendor-production
    permissions:
      contents: read
      id-token: write
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - id: plan
        uses: aalsanie/distroplane@v0.5.0
        with:
          command: plan
          config: distroplane.vendor.json
      - uses: aalsanie/distroplane@v0.5.0
        env:
          VENDOR_TOKEN: ${{ secrets.VENDOR_TOKEN }}
        with:
          command: apply
          oidc: required
          config: distroplane.vendor.json
          plan: ${{ steps.plan.outputs.plan-path }}
          journal: .distroplane/vendor.journal
          credential-mappings: vendor-publish=VENDOR_TOKEN
```

A single multi-target plan remains supported when the workflow intentionally accepts a shared credential boundary.

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
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: aalsanie/distroplane@v0.5.0
        with:
          command: apply
          oidc: required
          config: distroplane.json
          plan: .distroplane/plan.json
          journal: .distroplane/run.journal
```

The Action smoke workflow includes both the denied case (no `id-token: write`) and the granted case.


## Release-candidate, approval, and asynchronous reconciliation

A release-candidate workflow can trigger on candidate tags, for example:

```yaml
on:
  push:
    tags:
      - 'v*-rc*'
```

A production workflow can keep build, approval, execution, and reconciliation as separate boundaries without putting provider publication commands in YAML:

1. Build the release candidate and preserve the exact artifacts.
2. Run `plan` with `upload-plan: 'true'`; hand the immutable plan and release artifacts to the next job.
3. Put the apply job behind a protected GitHub environment with required reviewers. That environment is the manual approval boundary.
4. Run `apply`. Exit code `4` is exposed as `pending=true` and does not fail the Action.
5. Preserve the plan, journal, and immutable release artifacts for a later scheduled or manually dispatched reconciliation job.
6. Run `reconcile` and upload the resulting evidence bundle.

The approval job should use only `contents: read` unless a target needs more. Add `id-token: write` only to a job whose provider actually consumes GitHub OIDC. Third-party workflow actions should be pinned by immutable commit; the repository smoke workflow demonstrates this for checkout, artifact upload/download, and setup actions.

A protected apply job can therefore look like:

```yaml
apply:
  needs: plan
  environment: distribution-production # configure required reviewers in GitHub
  permissions:
    contents: read
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      with:
        persist-credentials: false
    - uses: actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c # v8.0.1
      with:
        name: distroplane-plan
        path: .distroplane/plan
    - id: plan-file
      shell: bash
      run: echo "path=$(find .distroplane/plan -type f -name '*.json' -print -quit)" >> "$GITHUB_OUTPUT"
    - id: apply
      uses: aalsanie/distroplane@v0.5.0
      with:
        command: apply
        version: 0.5.0
        config: distroplane.json
        plan: ${{ steps.plan-file.outputs.path }}
        journal: .distroplane/run.journal
        upload-evidence: 'true'
```

If `steps.apply.outputs.pending == 'true'`, persist the journal and exact release artifacts and invoke `command: reconcile` in a later workflow run. The smoke workflow exercises a pending apply followed by reconciliation and evidence export.

## Plan and artifact handoff

When planning and applying in separate jobs, set `upload-plan: 'true'` and restore that Action-produced plan artifact together with the exact release artifacts before apply. The repository smoke workflow exercises this handoff on Linux, macOS, and Windows, then separately downloads the evidence artifact produced by the Action.

A provider-specific publish command should not appear in workflow YAML. Provider configuration belongs in Distroplane configuration; provider implementations remain isolated executables speaking the protocol.
