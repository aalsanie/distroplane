# CLI and recovery

## Local readiness checks

Run `distroplane doctor --config distroplane.json` before planning or publishing. The command reads configuration, local artifact bytes, provider executables, credential environment **presence** (not values), Git availability where needed, and the existing run catalog. Every result has a stable check ID and `PASS`, `WARN`, `FAIL`, or `SKIP` severity and an actionable next step. Use `--json` for one machine-readable document; failures use exit code 3 and warnings alone do not block publication. The optional `--network` only checks connectivity to fixed public registry endpoints; it never sends tokens, verifies account permissions, or publishes. Without this flag remote access is explicitly `SKIP`.

Configuration schema v1 continues to be supported by existing low-level and high-level commands; there is no implicit migration or rewrite. Schema v2 is recommended for new configurations. Generate a separate new file with `init --output PATH` and compare its target intent before adopting it. Unsupported custom-provider options cannot be converted automatically and must be reviewed explicitly.

## Diagnose and recover

Exit codes remain 0 success, 1 operational error, 2 usage error, 3 invalid input or selection, 4 pending external work, 5 rejected, and 6 failed or cancelled. JSON diagnostics use the unchanged `outputSchemaVersion` and `error.kind` fields and are written to stderr. An interrupted publication can have already dispatched an external side effect: inspect `status` and `resume --reconcile-only` before allowing another attempt.

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

## Resume and automatic status

```sh
distroplane status --config distroplane.json --json
distroplane status --config distroplane.json --release RELEASE_ID --run RUN_ID --json
distroplane resume --config distroplane.json --json
distroplane resume --config distroplane.json --release RELEASE_ID --credential REF=ENV
distroplane resume --config distroplane.json --reconcile-only --json
```

`status` selects a **uniquely identified, already-started run** from the durable catalog without contacting providers. It does not require the original configuration or release artifacts, provided the retained catalog, plan, journal, and planned provider executable remain available. The explicit `--plan` / `--journal` form remains supported. A selection with no run, multiple matches, an unreadable catalog, a reserved run without its journal, or a corrupt journal fails closed; it never chooses the newest run.

`resume` selects the same immutable plan, run ID, and journal and invokes the existing executor. It never rebuilds a plan, creates a new publication run, or grants an ambiguous retry. Dispatched/uncertain operations follow the existing reconciliation policy. Use `--reconcile-only` to inspect external state without starting any undispatched publication. A completed run is reported read-only. A normal resume still verifies artifact and provider-executable integrity before performing work. Missing or changed artifacts block execution; reconciliation-only can remain available for an existing run.

**Retention policy:** Distroplane does not automatically delete plans, run catalogs, journals, release artifacts, or provider binaries—at completion, after failure, or on resume. Keep all of them until external settlement and the operator's independent retention decision; retain unresolved, ambiguous, cancelled, and pending runs for recovery. Do not manually remove or replace a journal to restart publication: a reserved run with missing durable history requires explicit manual investigation. All recovery commands preserve the original plan/run identity.

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
