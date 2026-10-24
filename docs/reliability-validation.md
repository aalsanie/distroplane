# Reliability validation

The repository tests interruption, corruption, concurrency, and unsafe retry behavior. The central invariant is:

**A side effect with an unknown post-dispatch result is reconciled before another publication attempt.**

Credential preparation failures suspend a run as `BLOCKED` before dispatch and can be safely resumed with the original plan/journal after credentials are corrected. Blocked checks never publish, do not consume publication retry attempts, and never turn ambiguous post-dispatch outcomes into an automatic retry.\n\nCoverage includes provider crashes/timeouts after dispatch, cancellation, lost remote responses, repeated pending reconciliation, journal truncation/corruption, changed artifacts/provider binaries, plan tampering, and rejection of cross-plan journal replay (including completed publications and evidence).

## Scale and bounds

CI exercises:

- 1,000-target planning;
- 10,000-operation DAG scheduling;
- 100,000 journal events;
- cancellation storms and goroutine release;
- bounded provider stdout/stderr and protocol messages;
- Linux, macOS, and Windows tests/builds;
- race detection and fuzz smoke tests.

Execution concurrency is bounded. Waiting-external runs do not keep provider goroutines resident, and reconciliation does not replay the original publish operation.

## Durability

Journal fault tests cover interrupted writes, disk exhaustion, read-only storage, clock skew, truncated tails, and corrupted complete frames. Writers synchronize successful appends and hold an OS file lock.

Platform-sensitive tests cover provider process termination, path handling, locking, and release-artifact execution. See the [performance baseline](performance-baseline.md) for recorded measurements.
