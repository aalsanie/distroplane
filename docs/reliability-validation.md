# Reliability and validation

Core safety invariant: **an operation whose side effect may have been dispatched, but whose result is unknown, must be reconciled before publication is attempted again.** The host journals dispatch before releasing the provider request. It does not promise exactly-once delivery.

## Tested failure boundaries

| Boundary | Required recovery | Regression tests |
| --- | --- | --- |
| Plan save, directory sync | No partial plan or unconfirmed save; retry identical bytes | `TestStoreErrors`, `TestStoreRetriesUncertainDirectorySync` |
| Claim before journal creation | Preserve reservation; reject replacement | `TestCatalogClaimProcessCrash`, `TestCatalogClaimDirectorySyncFailurePreservesReservation` |
| Journal append/sync, tail, lock | Poison failed writer; recover partial tail only; reject complete corruption | `TestWriterPoisonsAfterPartialWriteOrSyncFailure`, `TestWriterRecoversTruncatedTailAndResumesSequence`, `TestWriterLockReleasedAfterForcedProcessExit`, `TestReadRejectsCorruption` |
| Credentials, preparation, process start | No dispatch on missing prerequisite or failed permission | `TestPreparationFailureDoesNotDispatch`, `TestExecuteRecoversProviderStartBeforeDispatch`, `TestExecutorAbortsApplyOnDispatchJournalFailure` |
| Lease acquire/renew/expire | One authority; observe expired dispatched attempt | `TestExecuteLeaseContentionDoesNotStartAttempt`, `TestExecuteRenewsLongRunningLease`, `TestExpiredLeaseAfterDispatchReconcilesWithoutReapply` |
| External effect, response, observation/result | Reconcile original attempt; never republish on uncertainty | `TestExecuteRecoversDispatchedAttemptByReconciling`, `TestExecuteRecoversEveryInterruptedObservationBoundary` |
| Completion | No new attempt | `TestExecuteResumeCompletedRunIsNoop` |
| Changed intent, inaccessible retained state | Reject mutation; preserve original identity | `TestPublishApprovalRefusesChangesAfterPreview`, `TestResolveRejectsTamperedAndMissingProviderSnapshots`, `TestWindowsRetainedProviderSharingViolations`, `TestWindowsCatalogSharingViolationsPreserveOriginalRun` |

For full candidate qualification, dispatch the CI workflow on the candidate ref and approve its existing environment gate. All jobs, including both Java adapter matrices, must pass; path-filtered skips do not qualify a release. Windows recovery coverage is recorded separately from the required Linux statement, line and branch gates.

## Operational limits

A `WAITING_EXTERNAL` result is not proof of failure or success. Reconciliation is read-only. New ambiguous-publication retry authorization is disabled; absence alone is insufficient. See [recovery guidance](cli.md#ambiguous-publication-recovery).

An expired or released reconciliation lease closes the interrupted observation without changing its outstanding outcome or evidence. Recovery acquires a new lease and observes the same attempt; duplicate reconciliation starts under the same authority remain invalid. Preserve invalid journals for investigation rather than discarding complete records.

Checksummed journals detect accidental corruption but are not tamper-evident against a filesystem attacker. Provider responses and exported evidence are not independently attested. See the [security model](security-model.md) and [historical performance baseline](performance-baseline.md).
