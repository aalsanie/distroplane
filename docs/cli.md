# CLI

Install the CLI and required providers, build your artifacts, and create a [configuration](configuration.md).

## Plan and apply

```sh
distroplane validate --config distroplane.json
distroplane plan --config distroplane.json
distroplane apply --config distroplane.json --plan PLAN.json --journal run.journal \
  --credential npm-publish=NPM_TOKEN
```

`validate` checks core configuration structure. `plan` also reads artifacts, discovers providers, validates provider configuration, hashes artifacts/provider executables, and saves an immutable plan under `.distroplane/plans/` by default. Planning does not require publication credentials for the official providers.

Review the plan before applying it. Apply verifies the saved artifact and provider digests, then executes or resumes the journaled run. The execution configuration must still contain the saved target IDs and select matching providers.

Credential flags map a planned reference to an environment-variable name: `--credential REF=ENV`. Secret values stay in the environment.

## Status and reconciliation

```sh
distroplane status --plan PLAN.json --journal run.journal
distroplane reconcile --config distroplane.json --plan PLAN.json --journal run.journal \
  --credential npm-publish=NPM_TOKEN
```

`status` reads only the plan and journal. Target states include `PLANNED`, `READY`, `RUNNING`, `WAITING_EXTERNAL`, `PUBLISHED`, `REJECTED`, `FAILED`, and `CANCELLED`.

Use `reconcile` for pending or ambiguous operations. It observes external state and does not dispatch new publication work. If reconciliation unblocks operations that have not started, run `apply` again.

## Evidence

```sh
distroplane evidence --plan PLAN.json --journal run.journal --output evidence.json
```

Evidence can be exported from completed, pending, or failed runs. It describes recorded state; a successful export does not mean publication succeeded. `--attestation NAME=URI` and `--journal-reference URI` attach references only; they are not signatures or verified attestations.

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Successful command; lifecycle operations are fully published |
| `1` | Operational error |
| `2` | Invalid command or flags |
| `3` | Invalid configuration, plan, run, or changed artifact |
| `4` | Run still pending |
| `5` | Rejected operation |
| `6` | Failed or cancelled operation/run |

Failure takes precedence over rejection, then pending. All lifecycle commands accept `--json`; errors go to stderr.

## Recovery

Preserve the exact plan, journal, artifacts, provider binaries, and configuration until the run finishes. Saved artifact paths are absolute, and npm also stores its tarball path in provider payload.

After interruption, inspect `status` and resume with the same plan and journal. An unfinished dispatched operation is reconciled before a safe retry. A cancelled run can reconcile dispatched work, but `apply` does not restart undispatched work in that cancelled run.

A truncated final journal record is recoverable from its valid prefix; corruption in a complete record is rejected. Starting a new journal discards the prior run's recovery state.
