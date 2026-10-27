# CLI and recovery

Build artifacts first, install the CLI and matching provider binaries, then create a [configuration](configuration.md).

## One-command publish

```sh
distroplane publish --config distroplane.json
distroplane publish --config distroplane.json --yes --json
distroplane publish --config distroplane.json --dry-run --json
```

`publish` creates and persists the immutable plan, shows its ID, artifacts, digests, targets, and operation count, then requires an exact interactive `publish PLAN_ID` confirmation before any provider operation is dispatched. Noninteractive automation must pass `--yes` or `--confirm-plan PLAN_ID`. `--dry-run` produces a preview and saved plan but never claims a run or publishes. Credential mappings (`--credential REF=ENV`) and `--concurrency` are forwarded to the existing executor. JSON output contains `plan`, `approved`, `journalPath`, and `run` (the latter two after execution).

Each immutable plan is bound to a single durable journal under `.distroplane/runs/`. Reissuing `publish` for an already claimed run, or for a changed plan with the same release ID, fails closed rather than starting another publication. Inspect the journal with `status` and use the existing `reconcile`/`apply` recovery commands as appropriate; `publish` does not silently resume or authorize retries. No credentials are embedded in the persisted plan.

## Publish

```sh
distroplane validate --config distroplane.json
distroplane plan --config distroplane.json
distroplane apply --config distroplane.json --plan PLAN.json --journal run.journal --credential npm-publish=NPM_TOKEN
```

`validate` checks core JSON structure; `plan` additionally checks artifacts and provider-specific intent, then writes a content-addressed plan under `.distroplane/plans/` unless `--state-dir` is set. Use the returned plan path in subsequent commands. Planning has no publication side effects or credential requirement for the official providers.

Review the plan before applying. `apply` rechecks artifact and provider digests, then executes or resumes the journaled run. The execution configuration must retain the planned target IDs and matching provider selections. `--credential REF=ENV` resolves a declared credential from an environment variable at execution time; do not put secret values in a plan.

## Inspect and reconcile

```sh
distroplane status --plan PLAN.json --journal run.journal --json
distroplane reconcile --config distroplane.json --plan PLAN.json --journal run.journal --credential npm-publish=NPM_TOKEN
distroplane evidence --plan PLAN.json --journal run.journal --output evidence.json
```

`status` and `evidence` replay local state without querying the destination. `reconcile` checks dispatched or externally pending operations and never publishes new content. If reconciliation leaves undispatched operations ready, invoke `apply` again.

| State | Operator interpretation |
| --- | --- |
| `PLANNED`, `READY`, `RUNNING` | Not yet completed |
| `BLOCKED` | A prerequisite (notably credentials) failed before provider dispatch; repair and rerun `apply` with the same plan/journal |
| `WAITING_EXTERNAL` | External state is pending or uncertain; reconcile rather than blindly republishing |
| `PUBLISHED` | Provider-reported success; consult provider evidence for its strength |
| `REJECTED`, `FAILED`, `CANCELLED` | Inspect error/status before deciding on another run |

A blocked credential check does not consume the publication attempt budget, and repeatedly invoking `apply` without restoring the credential does not spin. A cancelled run may reconcile previously dispatched work, but `apply` does not restart its undispatched operations.

## Explicit retry of an ambiguous npm publish

If an npm publish request was dispatched and its result was lost, a missing version on reconciliation does **not** prove that the original request cannot still complete. Distroplane will not republish automatically. After independently checking the registry and accepting the duplicate-publication risk, an operator can authorize exactly one retry:

```sh
distroplane reconcile --config distroplane.json --plan PLAN.json --journal run.journal --credential npm-publish=NPM_TOKEN
distroplane retry --plan PLAN.json --journal run.journal --operation OPERATION_ID --confirm-plan PLAN_ID --acknowledge-duplicate-risk
distroplane apply --config distroplane.json --plan PLAN.json --journal run.journal --credential npm-publish=NPM_TOKEN
```

Use the global operation ID from `status --json` and the immutable plan ID. Authorization is durable but does not itself publish. It is allowed only for an ambiguous npm `publish` operation with a successful, latest reconciliation that found the version absent; it is rejected for other providers, stale observations, repeated authorization, or completed/cancelled runs. A new ambiguous outcome requires a fresh reconciliation and authorization.

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Command successful; lifecycle run is fully published when applicable |
| `1` | Operational error |
| `2` | Invalid command or flags |
| `3` | Invalid configuration/plan/run or changed artifact |
| `4` | Work remains pending |
| `5` | Rejected operation |
| `6` | Failed or cancelled run/operation |

Failure takes precedence over rejection, then pending. Lifecycle commands support `--json`; errors use stderr.

## Preserve recovery state

Keep the exact plan, journal, configuration, artifacts, and provider binaries until completion. Artifact paths are absolute, and some provider payloads contain additional local paths. Resume with the **same** plan and journal after an interruption; a new journal loses the previous run's recovery history. An incomplete final journal frame can be discarded on reopen, but a corrupted complete frame is rejected.

See [release verification](release-verification.md) for compatibility with journals created by release candidates.
