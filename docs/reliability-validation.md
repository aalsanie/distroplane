# Reliability and validation

Core safety invariant: **an operation whose side effect may have been dispatched, but whose result is unknown, must be reconciled before publication is attempted again.** The host journals dispatch before releasing the provider request. It does not promise exactly-once delivery.

## Tested failure boundaries

| Boundary | Validation |
| --- | --- |
| Pre-dispatch preparation | Missing credentials remain `BLOCKED`; restoration resumes the same plan/journal without spending the publication retry budget |
| Post-dispatch uncertainty | Provider crash, timeout, cancellation, lost response, and ambiguous npm publication require reconciliation rather than an automatic republish |
| Durable state | Truncated tail recovery; complete-record corruption rejection; concurrent writer locking; disk/write/sync failure cases |
| Immutable intent | Changed artifacts or provider binaries, tampered plans, and cross-plan journal replay are rejected |
| Executor scheduling | Dependency ordering, bounded concurrency, lease behavior, cancellation, and quiescence |
| Protocol and subprocesses | Message/output size limits, process termination, credentials, and response validation |

CI includes Linux/macOS/Windows tests and builds, race detection, fuzz smoke tests, provider integration tests, and GitHub Action smoke tests. Scale fixtures cover 1,000 targets, 10,000 operations, and 100,000 journal events; these are test workloads, not supported service-level guarantees.

## Operational limits

A `WAITING_EXTERNAL` result is not proof of failure or success. Reconciliation is read-only. Only the npm-specific, journaled operator authorization described in the [CLI guide](cli.md#explicit-retry-of-an-ambiguous-npm-publish) permits a subsequent attempt following a reconciled absent observation; absence alone is insufficient.

Checksummed journals detect accidental corruption but are not tamper-evident against a filesystem attacker. Provider responses and exported evidence are not independently attested. See the [security model](security-model.md) and [historical performance baseline](performance-baseline.md).
