# File and command compatibility

This document describes the externally observable contracts of the stable
v0.9.0 implementation and the rules for preserving them in later CLI releases.
The checked-in configuration and journal replay fixtures are executable
regression tests, not a file migration mechanism.

| Interface | Stable format | Compatibility rule |
| --- | --- | --- |
| Release configuration | JSON schema `"1"` | Continue reading unchanged; new schemas require explicit opt-in, never implicit rewrite |
| Saved distribution plan | Plan schema `"1"` / provider protocol `"1"` | Plan ID, artifact digests, and provider executable digests must remain bound to original intent |
| Provider subprocess protocol | `"1"` | Preserve request correlation, operation capabilities, and strictly bounded input/output |
| Journal | Framed journal schema `2` | Verify plan binding, record sequence and durable prefix before replay; do not silently create a new journal |
| Legacy journal | Schema `1` | Unsupported: fail explicitly without migration or new publication |
| Evidence bundle | Schema `"1"` | Maintain journal-derived identity; do not interpret external review as public catalog availability |
| CLI structured output | `outputSchemaVersion: "1"` | Preserve existing fields and types; only additive compatible fields without schema bump |
| Exit status | `0` success; `1` runtime; `2` usage; `3` invalid; `4` pending; `5` rejected; `6` failed | Keep distinct statuses, including pending versus published |
| Low-level CLI | `version`, `validate`, `plan`, `apply`, `status`, `reconcile`, `retry`, `evidence` | Preserve existing flags, outputs, and advanced explicit paths |
| GitHub Action | Existing low-level commands, credential mappings, outputs | New commands and fields must not remove or reinterpret previous inputs/outputs |

## Recovery compatibility

Plan identity is semantic but remains bound to the artifact bytes and the
provider executables that produced the plan. A missing or mismatched provider,
corrupt journal, or unsupported historical format must fail without publication.
Do not silently reuse a current binary for an unfinished plan that was bound
to a different digest. A fully verified journal-v2 recovery can resume on a
newer CLI only if the original provider bytes remain available.

A side effect is never automatically retried after an unknown post-dispatch
outcome. Reconcile is read-only with respect to external publication. An absent
npm registry version alone does not authorize republishing; explicit, durable,
one-shot operator authorization is required.

The publication states `PUBLISHED`, `WAITING_EXTERNAL`, `REJECTED`,
`FAILED`, `BLOCKED`, and `CANCELLED` are not interchangeable.
A Homebrew/WinGet pull request and SDKMAN vendor API acceptance provide
different evidence than consumer-facing package availability.

## Fixture scope

- `internal/config/testdata/compatibility/`: legacy configuration syntax for
  npm, SDKMAN state/legacy, Homebrew formula/JVM, and WinGet MSI/ZIP.
- `internal/journal/testdata/compatibility/journal-v2/`: persisted event
  sequences for published, pending, blocked, rejected, failed, cancelled,
  and ambiguous execution, replayed through framing and reducer.
- `cmd/distroplane/testdata/compatibility/`: stable output-field fixture and
  end-to-end low-level CLI/recovery checks using only a local fake provider.

Validation: `go test ./...` with the toolchain declared by `go.mod`.
The fixtures intentionally use no production registry credentials or remote writes.
