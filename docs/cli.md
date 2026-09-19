# CLI workflow

[Install Distroplane and the providers you need](release-verification.md), build your release artifacts, and create a [configuration](configuration.md).

```text
artifacts + configuration → plan → apply → status
                                    ↕
                          reconcile when pending → evidence
```

## Plan and apply

```sh
distroplane validate --config distroplane.json
distroplane plan --config distroplane.json
```

`validate` checks configuration structure. `plan` hashes the artifacts and provider executables, asks each provider to plan its work, and saves an immutable JSON plan. Planning needs no publication credentials for the official providers.

By default, plans are saved under `.distroplane/plans/` beside the configuration. `--state-dir PATH` changes that directory; a relative value is also resolved from the configuration directory. `plan --json` returns the exact saved path as `path` and its identity as `planId`.

Review the saved plan. Use its path in place of `PLAN.json` below:

```sh
distroplane apply --config distroplane.json --plan PLAN.json --journal run.journal \
  --credential npm-publish=NPM_TOKEN --concurrency 4
```

This example expects an npm target with the `npm-publish` credential reference and `NPM_TOKEN` already supplied by your shell or CI. Repeat `--credential REF=ENV` as needed. On PowerShell, put the command on one line instead of using `\` continuation.

Apply executes the saved intent. Editing provider configuration after planning does not edit that intent: generate and review a new plan for changed work. The configuration supplied at execution still selects provider executables for the saved target IDs.

Apply creates a run ID and journal when needed, or resumes the run in an existing journal. `--run NAME` optionally selects the ID for a new run and must match an existing journal's ID. The default concurrency is four; `--concurrency 0` selects that default.

## Inspect and reconcile

```sh
distroplane status --plan PLAN.json --journal run.journal
distroplane reconcile --config distroplane.json --plan PLAN.json --journal run.journal \
  --credential npm-publish=NPM_TOKEN
```

`status` reads the plan and journal without contacting providers. Target states include `PLANNED`, `READY`, `RUNNING`, `WAITING_EXTERNAL`, `PUBLISHED`, `REJECTED`, `FAILED`, and `CANCELLED`.

`WAITING_EXTERNAL` means publication needs another observation: a pull request may be awaiting review, or a request may have lost its response. Run `reconcile` later with the same plan and journal. It observes pending or ambiguous operations and does not dispatch new publication work. It requires an existing journal and does not recheck targets already recorded as published.

Reconcile does not run newly unblocked operations. Run `apply` again if the plan still has unstarted work after reconciliation. Neither command is a background polling service.

## Export evidence

```sh
distroplane evidence --plan PLAN.json --journal run.journal --output evidence.json
```

The bundle records the release, artifact hashes, provider versions, plan and run IDs, each target's known state, provider evidence, and a journal digest. Pending or failed runs can also be exported. Export succeeding does not mean publication succeeded.

Omit `--output` to write the bundle to stdout. With `--output evidence.json --json`, stdout contains a summary including the file path and its SHA-256 digest. Optional references can be attached with `--attestation NAME=URI` and `--journal-reference URI`; these are links, not signatures or verified attestations.

## Automation and exit codes

All lifecycle commands accept `--json`. Results go to stdout; errors go to stderr. `distroplane version --json` reports the installed version and build metadata.

| Code | Meaning |
| --- | --- |
| `0` | Command succeeded; for apply/status/reconcile, all operations published |
| `1` | Operational error, such as a provider process or journal failure |
| `2` | Invalid command syntax or flags |
| `3` | Invalid configuration, plan, run, or changed artifact |
| `4` | Run still pending |
| `5` | Rejected operation |
| `6` | Failed or cancelled operation/run |

For mixed results, failure takes precedence over rejection, then pending. Inspect the JSON `targets` and `operations` arrays; `completed` alone is not a success indicator. Shells using `set -e` need to handle code `4` explicitly. The GitHub Action treats it as a successful step with `pending=true`.

## Preserve and recover a run

Keep the exact plan, journal, artifacts, provider binaries, and configuration used by the run. Artifact paths in saved plans are absolute. Restore files at the same paths, on the same OS/architecture, when moving execution to another job. npm also stores its absolute tarball path in the provider payload and needs that file during reconciliation.

After an interrupted process, inspect `status`, then resume with the same `apply` command and journal. An unfinished dispatched operation is reconciled before any safe retry. An explicit cancellation records the run as cancelled; reconciliation can still resolve dispatched work, but apply does not restart unstarted work in that cancelled run.

An incomplete final journal record can be recovered from its valid prefix; `status` reports a truncated tail. Corruption in a complete record is rejected. Keep a copy and investigate rather than editing or deleting history. Starting a new journal discards the prior run's recovery knowledge.

There is no automatic rollback, journal migration command, or switch that forces publication through an ambiguous result. Keep a release's matching CLI and providers available until its runs finish.
