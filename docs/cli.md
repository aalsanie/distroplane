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

`status` reads only the plan and journal. Target states include `PLANNED`, `READY`, `RUNNING`, `BLOCKED`, `WAITING_EXTERNAL`, `PUBLISHED`, `REJECTED`, `FAILED`, and `CANCELLED`.

A `BLOCKED` operation means a required credential could not be prepared **before provider dispatch**. The run stays pending (exit code `4`) without consuming its publication retry budget. Restore the credential mapping and value, then call `apply` again with the **same plan and journal**. Only the blocked operations resume; already-published operations are not dispatched again. Repeating `apply` without fixing the credential checks the missing prerequisite once per invocation and does not spin. The journal records the non-secret error code. Reconciliation does not publish or unblock credential-blocked work.\n\nUse `reconcile` for pending or ambiguous operations. It observes external state and does not dispatch new publication work. If reconciliation unblocks operations that have not started, run `apply` again.

## Ambiguous npm publication recovery

An npm publish request can reach the registry even when its response is lost. Distroplane records an ambiguous side effect and reconciles it without republishing. An npm reconciliation showing the package version **absent** is not proof that the first request will never complete; automatic retry is therefore prohibited.

After verifying registry state independently, an operator who accepts the residual risk can durably authorize **one** retry:

```sh
distroplane reconcile --config distroplane.json --plan PLAN.json --journal run.journal \\
  --credential npm-publish=NPM_TOKEN
distroplane retry --plan PLAN.json --journal run.journal \\
  --operation TARGET:publish --confirm-plan PLAN_ID --acknowledge-duplicate-risk
distroplane apply --config distroplane.json --plan PLAN.json --journal run.journal \\
  --credential npm-publish=NPM_TOKEN
```

Use the **exact global operation ID** shown by `distroplane status --json` (the placeholder `TARGET:publish` is illustrative) and the immutable plan ID from `distroplane plan`. The `retry` command never calls a provider or publishes. It only works for an ambiguously dispatched **npm publish** operation whose most recent successful reconciliation explicitly found the version absent. It fails closed for missing/failed reconciliation, other providers, ordinary pending work, cancelled/completed runs, wrong plans, stale attempts, or repeat authorization. An authorization is journaled before the next `apply`, and if that next publish is ambiguous again, another successful reconciliation and new explicit authorization are required.

**Risk:** npm metadata can be eventually consistent and a previously timed-out request can still finish. Check the registry directly and use this override only when authorized by your release process. The npm provider checks the package version again before uploading and rejects conflicting content; neither check is an exactly-once guarantee.

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
