# Production readiness and acceptance evidence

This is the evidence index for **engineering checks before the v1.0.0 release rehearsal**. Passing a pull-request CI run is necessary but not sufficient to authorize a general-availability release. Verify exact commit, workflow attempt, native runner, and artifact identity at review time. The release owner records any accepted limitation and independent acceptance separately; no unchecked item is inferred complete from this document.

## Cross-platform executable and installation evidence

| Platform | Existing executable validation | Additional release-owner check |
| --- | --- | --- |
| Linux amd64 | CI release archive build, packaged CLI/four provider smoke and direct installer; native release workflow | Fresh public release install, Linuxbrew formula audit/install/uninstall |
| Linux arm64 | CI native installer tests; native release workflow | Fresh public archive/direct installer on ARM64, Linuxbrew support decision |
| macOS amd64 | Native release workflow (`macos-15-intel`); archived formula verification | Clean Intel Homebrew install, audit, uninstall and provider discovery |
| macOS arm64 | CI macOS JVM formula integration; native release workflow | Clean Apple Silicon Homebrew install, audit, uninstall and provider discovery |
| Windows amd64 | CI native MSI install/repair/upgrade/uninstall and packaged provider smoke | Released MSI installation in fresh shell and WinGet **local manifest** install |
| Windows arm64 | CI native ARM64 MSI lifecycle test and release workflow | Released ARM64 MSI installation and WinGet **local manifest** install |

A compile-only build is **not** evidence of execution. Homebrew installation of a controlled JVM formula is not a clean consumer install of the public Distroplane formula. Local WinGet manifests do not demonstrate Microsoft catalog availability. Keep the exact job URL/commit for every completed native run and request written waivers for hardware or package-channel gaps. A future release workflow cannot attest to an unreleased v1.0.0 asset.

## Engineering acceptance matrix

| Gate | Existing validation to review | Remaining release-specific evidence |
| --- | --- | --- |
| install matrix | `.github/workflows/ci.yml` release-build, Linux arm64 and MSI native lifecycle; `.github/workflows/release.yml` 6-architecture native installation | Fresh real release install on all supported channels and hardware; capture native-vs-emulated provenance |
| crash and duplicate safety | `internal/journal/fault_test.go`, `internal/executor/ambiguous_retry_test.go`, `internal/runstate/crash_test.go`, `cmd/distroplane/{blocked_recovery,resume,retry,publish}_test.go` | Verify exact CI test outcomes and interruption/changed-digest cases on the release candidate; no implicit npm retry |
| tampering | `internal/providerretention/retention_faults_test.go`, `internal/runstate/safety_test.go`, `scripts/test_install_linux.py`, `scripts/test_verify_release_stage.py`, `scripts/test_action_recovery.py`, `maven-plugin/src/test` | Review permission, PATH shadowing, archive escapes, cache poisoning and output redaction on native hosts |
| provider semantics | `providers/{npm,sdkman,homebrew,winget}/*_test.go`, `internal/config/testdata/compatibility/`, `cmd/distroplane/release_artifact_test.go` | Verify planning/apply/reconcile/failure cases for each destination; PR creation/merge and API acceptance must not be called public availability |
| Action and Maven | `.github/workflows/action-smoke.yml`, `.github/workflows/maven-plugin.yml`, `.github/workflows/maven-central-consumer.yml` | Preserve exact approval, pending, restore and run-ID evidence; use sacrificial credentials only with approval |
| incident response | [Security model](security-model.md), [incident procedure](security-incident-response.md), `SECURITY.md` | Named human owners, private tabletop record, credential rotation/withdrawal signoff |
| performance | [Historical performance baseline](performance-baseline.md), `.github/workflows/ci.yml` benchmark job, `scripts/compare-benchmarks.py` | Inspect uploaded per-commit sample/environment; accept/reject material regressions; rerun on comparable hardware where needed |
| docs and examples | `scripts/verify-readiness.py`, [CLI](cli.md), [configuration](configuration.md), [release verification](release-verification.md), [Action](github-actions.md), published Maven example | Execute consumer examples with tested public versions; validate all referenced paths/credentials, and check external channel availability |
| independent onboarding | Task protocol below | **Independent human evaluations remain required** on macOS, Windows, Linux |

No line in this matrix asserts an external test, incident drill, or usability evaluation has occurred. GitHub workflow results, artifact digests and human signoffs must be linked by the release owner before marking a gate accepted.

## Independent developer evaluation protocol

Recruit developers who did not implement Distroplane, one or more on each of macOS, Windows and Linux, using clean user environments. Use a controlled nonproduction registry/tap/review repository and predefined prebuilt artifacts; never provide production tokens to evaluators. Give participants only the README and the linked canonical guides; do not reveal provider binary locations, immutable plan paths or journal locations in advance.

For each participant capture **OS/architecture, install method, user role, task outcome, elapsed time, commands used, unexpected manual filesystem operations, errors, and any required hint**. Ask them to complete these tasks independently:

1. Fresh installation and `distroplane version`/provider discovery.
2. Initialize configuration and publish one prebuilt artifact to a test destination.
3. Configure a two-destination release and review a plan without publishing accidentally.
4. Recover a run blocked by a missing credential, preserving the same run identity.
5. Inspect and resume or reconcile a pending external review without creating a second release.

**Pass:** Normal install and release are possible using documented commands, with no requirement to type provider binary, plan, or journal paths. Capture first successful publish duration after artifacts/credentials are ready; distinguish environment failures from documentation failures. Any participant blocked by undocumented required steps produces a reproducible issue and either a fix/retest or an explicit release-owner scope decision. Independent evaluations cannot be replaced by synthetic CLI fixtures or the implementer's own walkthrough.

## Performance review protocol

The baseline commit is `47e40cbc21605c7ddaa84fe62a8535b52f789eea`; its results and caveats are in [performance-baseline.md](performance-baseline.md). The benchmark CI records cold CLI startup, 1,000-target planning, 10,000-operation scheduling, and 100,000-event journal replay/reduction as well as other existing workload samples. `scripts/compare-benchmarks.py` creates a machine-readable report; ratios exceeding 2x are flagged for **human investigation**, not automatically accepted or waived. Go runtime, CPU, filesystem, guest load, warmup count, and sample count must be reviewed before assigning causality. Check runtime peak memory and goroutine counts separately in the existing 1,000-operation executor sample. No service-level target or release approval is invented by the reporting script.

## Final handoff to release rehearsal

Before beginning the final release rehearsal, attach the exact-commit CI and benchmark URLs, native platform evidence, external-channel availability, a signed incident drill, the independent developer results, and any accepted limitations. The release owner must explicitly decide whether each production-readiness acceptance criterion is satisfied. The v1.0.0 tag remains subject to a separate release rehearsal and approval.
