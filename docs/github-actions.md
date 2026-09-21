# GitHub Actions

Start with the [README quick start](../README.md#github-actions-quick-start). The Action runs `plan`, `apply`, or `reconcile`; artifacts must already exist and configuration must name their actual paths.

## Plan and apply

These steps follow your artifact build and configuration setup:

```yaml
- id: plan
  uses: aalsanie/distroplane@v0.9.0-rc.2
  with:
    command: plan
    config: distroplane.json

- id: apply
  uses: aalsanie/distroplane@v0.9.0-rc.2
  env:
    NPM_TOKEN: ${{ secrets.NPM_TOKEN }}
  with:
    command: apply
    config: distroplane.json
    plan: ${{ steps.plan.outputs.plan-path }}
    journal: run.journal
    credential-mappings: npm-publish=NPM_TOKEN
    upload-evidence: 'true'
```

The credential reference must match your provider configuration. `plan` does not need publication secrets. `apply` and `reconcile` resolve the plan's declared references from the environment; `credential-mappings` accepts one `REF=ENV` mapping per line, never a secret value.

Use separate configuration/plan pairs and jobs when providers need different secrets or approvals. Give each job only its required credentials. There is no Action input that selects a subset of targets from a plan.

## Release installation and pinning

Every invocation installs the selected release unless `binary` is supplied. The installer downloads `SHA256SUMS`, the CLI, and all checksum-listed provider executables for the runner's OS/architecture. It verifies SHA-256 before executing each download, installs canonical names such as `distroplane-provider-npm`, and adds their directory to `PATH` for the current and later steps.

- At `aalsanie/distroplane@v0.9.0-rc.2`, omitting `version` selects release `v0.9.0-rc.2`.
- When pinning the Action to a commit, explicitly set `version: 0.9.0-rc.2`. A commit or branch is not a binary release version; the installer does not resolve it to a release.
- `version` accepts an exact release version with or without leading `v`. There is no `latest` lookup or version-range resolution.

```yaml
- uses: aalsanie/distroplane@d309048ea96601523e4759b127355b239235e4af
  with:
    version: 0.9.0-rc.2
    command: plan
```

A commit pin fixes the Action code. A release tag selects separately downloaded binaries; checksum verification still trusts that release's checksum file. The installer does not verify signatures or attestations. See [release verification](release-verification.md).

Linux, macOS, and Windows runners with x64 or ARM64 are accepted. The composite Action requires PowerShell 7 (`pwsh`), provided on standard GitHub-hosted runners. Self-hosted runners must provide it and any provider prerequisites, such as Git.

## Reviewed plan handoff

To put approval between planning and publishing, use a plan job without publication secrets and an apply job attached to a protected environment with required reviewers.

In the plan job, enable `upload-plan: 'true'` on the plan step. For the npm quick start, also preserve the configuration and tarball:

```yaml
- uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7.0.1
  with:
    name: release-inputs
    path: |
      distroplane.json
      *.tgz
    if-no-files-found: error
```

The apply job restores both artifacts. This job assumes the plan job is named `plan`, used the default `distroplane-plan` artifact name, and packed its tarball at the workspace root as in the README:

```yaml
apply:
  needs: plan
  environment: npm-production
  permissions:
    contents: read
  runs-on: ubuntu-latest
  steps:
    - uses: actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c # v8.0.1
      with:
        name: release-inputs
        path: .
    - uses: actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c # v8.0.1
      with:
        name: distroplane-plan
        path: reviewed-plan
    - id: reviewed
      shell: pwsh
      run: |
        $plans = @(Get-ChildItem reviewed-plan -File -Filter '*.json')
        if ($plans.Count -ne 1) { throw 'Expected one reviewed plan' }
        "path=$($plans[0].FullName)" >> $env:GITHUB_OUTPUT
    - id: apply
      uses: aalsanie/distroplane@v0.9.0-rc.2
      env:
        NPM_TOKEN: ${{ secrets.NPM_TOKEN }}
      with:
        command: apply
        plan: ${{ steps.reviewed.outputs.path }}
        journal: run.journal
        credential-mappings: npm-publish=NPM_TOKEN
        upload-evidence: 'true'
    - uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7.0.1
      if: always()
      with:
        name: distribution-journal
        path: run.journal
        if-no-files-found: warn
```

Keep both jobs on the same OS/architecture and workspace path. Plans contain absolute artifact paths and provider digests; npm also embeds the tarball path in its planned action. Restore the same bytes at the same paths and install the same provider release. Rebuilding an artifact or regenerating the plan after approval changes what was reviewed.

For custom providers, preserve their exact binaries too. Built-in plan upload includes only the plan. Evidence upload includes only the evidence bundle: neither feature preserves the journal or release artifacts.

## Waiting and reconciliation

A CLI exit code of `4` makes the Action succeed with `pending=true`. This means work remains; it is not confirmation that everything was published. Other nonzero codes fail the Action after optional evidence export/upload. For mixed outcomes, inspect `result-json` as well as `pending`.

Save the plan, inputs, and journal before the runner disappears. Restore them in a later job or workflow, then run:

```yaml
- id: reconcile
  uses: aalsanie/distroplane@v0.9.0-rc.2
  env:
    NPM_TOKEN: ${{ secrets.NPM_TOKEN }}
  with:
    command: reconcile
    plan: reviewed-plan/PLAN.json # the original saved plan file
    journal: run.journal
    credential-mappings: npm-publish=NPM_TOKEN
    upload-evidence: 'true'
    evidence-artifact-name: reconciled-evidence
```

Use the credentials required by your actual target. Reconciliation observes pending/ambiguous work; it does not publish new content or periodically recheck completed targets. Schedule later runs yourself. See the [CLI recovery guidance](cli.md#preserve-and-recover-a-run) before retrying an interrupted run.

## OIDC

`oidc: required` checks that GitHub supplied both `ACTIONS_ID_TOKEN_REQUEST_URL` and `ACTIONS_ID_TOKEN_REQUEST_TOKEN`. It fails before invoking the CLI if either is missing. Grant `permissions: id-token: write` to that job.

This is a permission-environment check, not authentication. The Action does not request a JWT, choose an audience, or forward GitHub's request credentials to providers. For npm trusted publishing, your workflow must obtain an ID token and map it to the provider's configured reference. The [npm guide](../providers/npm/README.md#trusted-publishing) shows that step. `oidc: required` by itself does not enable trusted publishing.

## Inputs and outputs

| Input | Default / purpose |
| --- | --- |
| `command` | Required: `plan`, `apply`, or `reconcile` |
| `config`, `state-dir` | `distroplane.json`, `.distroplane` |
| `plan`, `journal` | Plan required for execution; journal defaults to `.distroplane/run.journal` |
| `concurrency` | `0` selects the executor default of four |
| `run-id` | Optional ID for apply; must match an existing journal |
| `upload-plan`, `plan-artifact-name` | `false`, `distroplane-plan` |
| `evidence-path` | Optional output path after apply/reconcile |
| `upload-evidence`, `evidence-artifact-name` | `false`, `distroplane-evidence`; enabling upload also exports evidence |
| `attestations` | Newline-separated `NAME=URI` links attached to evidence |
| `repository` | `aalsanie/distroplane`; release asset source |
| `install-providers` | `true`; disable only when supplying providers yourself |
| `binary` | Local CLI path; skips all release installation and checksum checks |

Outputs include `plan-id`, `plan-path`, `run-id`, `completed`, `pending`, `exit-code`, `result-json`, `oidc-available`, and evidence path/digest. Upload steps also expose `plan-artifact-*` and `evidence-artifact-*` IDs, URLs, and digests. A run can be complete with failures: check `exit-code` and per-target state.

Use distinct artifact names for multiple uploads in one workflow run. `attestations` only adds references; the Action does not create or verify attestations.
