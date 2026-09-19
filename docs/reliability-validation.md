# Reliability validation

The repository keeps failure behavior executable. The tests below are the release gate for interruption, corruption, concurrency, and unsafe retry behavior.

| Scenario | Verification |
| --- | --- |
| Journal write stops mid-record / disk exhaustion | `internal/journal/fault_test.go`, `writer_test.go` |
| Read-only journal storage | `internal/journal/filesystem_unix_test.go` |
| Artifact removed or changed after planning | `internal/planner/load_validation_test.go` and CLI execution tests |
| Provider hangs | `internal/providerhost/client_test.go` |
| Provider emits excessive stderr/stdout | `internal/providerhost/client_test.go` |
| Provider creates child processes | `internal/providerhost/process_tree_test.go` |
| Provider crashes after dispatch | `internal/providerhost/driver_test.go` |
| Remote transport outage / lost response | provider HTTP/Git test suites for npm, SDKMAN, Homebrew, and WinGet |
| Runner or command cancellation | `internal/executor/preparation_test.go`, `scale_test.go` |
| Journal truncation | `internal/journal/frame_test.go`, `writer_test.go` |
| Journal corruption | `internal/journal/frame_test.go`, `writer_test.go` |
| Plan tampering | `internal/planner/load_test.go`, `load_validation_test.go` |
| Provider binary replaced after planning | `internal/planner/provider_digest_test.go`, `internal/providerhost/driver_test.go` |
| Wall-clock movement | `internal/journal/fault_test.go` |
| Cancellation storm | `internal/executor/scale_test.go` |
| Long-running external review/pending state | `internal/executor/scale_test.go` and GitHub Action asynchronous reconciliation smoke test |
| 1,000 targets | `internal/planner/scale_test.go` |
| 10,000-operation DAG scheduling | `internal/executor/scale_test.go` |
| 100,000 journal events | `internal/journal/reducer_test.go` and journal benchmarks |
| Bounded concurrency | executor concurrency tests and 1,000-operation benchmark |
| Bounded provider output | provider-host limited stdout/stderr writers and tests |

## Retry invariant

The central safety invariant is: **a side effect with an unknown post-dispatch result is reconciled before another publication attempt**.

Tests assert this for direct executor failures, provider-process crashes, timeouts, cancellation, npm connection loss, SDKMAN transport ambiguity, Homebrew lost push/PR confirmation, and WinGet lost push/PR confirmation.

## Scale behavior

Planning and scheduling allocate work proportional to the immutable target/operation set. Provider execution concurrency is bounded by the configured executor limit. Provider stdout, stderr, protocol message size, configuration size, plan size, and journal record size have explicit bounds.

Waiting-external runs do not keep provider goroutines resident. A resume performs reconciliation from persisted state; repeated reconciliation does not replay the original publish operation.

## Cross-platform behavior

CI executes tests and native builds on Linux, macOS, and Windows. Platform-sensitive coverage includes:

- path separator handling through `filepath`;
- absolute provider resolution and PATH lookup;
- provider cancellation and process termination;
- journal file locking;
- directory synchronization on Unix and the documented Windows behavior;
- atomic plan persistence using filesystem links and durable file writes;
- UTF-8 protocol/config validation and invalid-byte normalization for diagnostics.

Release binaries are produced for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64, and windows/arm64.
