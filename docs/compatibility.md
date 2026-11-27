# Compatibility

The [version inventory](../protocol/schema/v1/compatibility.json) describes the implemented machine interfaces.

| Interface | Supported version |
| --- | --- |
| Provider protocol | 1 |
| Configuration | 1 (generic), 2 (official frontend) |
| Saved plan | 1 (read), 2 (read/write) |
| Journal | 2 |
| Evidence | 1 |
| CLI JSON output | 1 |

The inventory retains the legacy scalar `schemaVersions.config` for existing readers; `supportedConfigVersions` lists all supported configuration versions. `schemaVersions.plan` identifies the current writer; `supportedPlanVersions` lists readable versions.

## Reader behavior

- Protocol readers tolerate unknown optional fields, but reject unsupported protocol versions, operations and capabilities. Required capabilities must be advertised. Correlation, message sizes, duplicate keys and operation dependencies are checked at runtime. Validate the [schema](../protocol/schema/v1/protocol.schema.json) against the request or response definition for the known message direction.
- Configuration envelopes and official v2 options reject unknown fields; v1 provider configuration is opaque. The [v2 schema](../schemas/config-v2.schema.json) describes structural input. Reference integrity, literal paths, URL safety, provider-specific semantics and artifact checks also require CLI/provider validation.
- Saved plans and journal records reject unsupported versions, unknown structural fields, duplicate JSON keys and invalid identities. Provider payloads and evidence are opaque JSON. Saved-plan identity and idempotency keys are verified rather than regenerated. Complete corrupt journal frames are never repaired; an incomplete final frame may be removed only after the durable history is verified for execution.
- CLI JSON and evidence readers should accept unknown additive fields in supported versions, reject unsupported schema versions and unknown normalized states, and inspect the exit status. JSON errors go to stderr. The `init`, `doctor`, `publish`, `status` and `resume` envelopes are distinct; do not infer success from a nonempty JSON object. See [exit codes](cli.md#exit-codes).

The nine normalized states are `PLANNED`, `READY`, `RUNNING`, `BLOCKED`, `WAITING_EXTERNAL`, `PUBLISHED`, `REJECTED`, `FAILED` and `CANCELLED`. `BLOCKED` is a pre-dispatch prerequisite failure, not a provider result or an authorization to republish. A provider can report only `WAITING_EXTERNAL`, `PUBLISHED` or `REJECTED`. For mixed targets, inspect every operation's reconciliation requirement independently of the aggregate state. A zero-operation completed run has no publication dispatch, even though the target aggregate is `PUBLISHED`. The optional state-output flag `legacyRetryPending` identifies unsupported retry authorization requiring manual recovery; it does not change the exit code. Consumers must not infer publication from a completed flag alone, and must verify that a publish envelope's plan identity and counts match its nested run.

### Publication review

Protocol 1 plan responses may include ordered `reviewFields` (zero to 16 plain label/value objects). Labels are unique, nonempty and at most 80 Unicode code points; values are nonempty and at most 512. The compact JSON representation is limited to 8192 UTF-8 bytes. Invalid UTF-8, replacement characters, control/format characters, line/paragraph separators, surrounding whitespace, duplicate labels and unknown entry keys are rejected. Providers must supply nonsecret fields derived from their actual planned action, not arbitrary configuration dumps. The runtime enforces these constraints beyond the structural schema.

New saved plans use schema 2 and include review fields in the semantic plan ID. Schema 1 plans retain their original hash, are read without review fields, and are not migrated. Older clients may ignore the optional wire field but cannot read schema 2 saved plans; do not downgrade an unfinished run. An existing schema 1 plan is not replaced by a new preview for the same release. New provider planning refuses review values that cannot be represented within the bounds; repository review values must not contain embedded query or fragment data. Summary-less providers produce a limited preview, not a synthesized destination description.

## Upgrades and recovery

Keep the original configuration, artifacts, immutable plan, journal, catalog and retained provider binaries. The compatibility suite builds v0.9.3 from its tagged source and exercises recovery using its saved plan, journal and retained provider bytes. Legacy zero-timeout operations retain their identity and use a finite runtime budget. The manual retry authorization available in v0.9.3 has been disabled because absence does not prove an earlier publication cannot complete. See [recovery guidance](cli.md#ambiguous-publication-recovery) for retired retry grants.

Unsupported formats or damaged history require non-mutating refusal. Preserve the original files and use a compatible, supported recovery tool for the recorded format if available; never replan an unfinished release, substitute provider bytes or downgrade to bypass a safety refusal. Journal v1 is unsupported. Do not assume compatibility with other pre-1.0 versions. Before downgrading, verify that the destination CLI supports every saved format and required safety behavior.

## Support policy

Documented 1.x machine interfaces remain compatible throughout 1.x. Non-security deprecations are announced for at least two minor releases and 90 days, whichever is longer, with removal only in a major release. A narrowly reviewed security exception may narrow unsafe behavior where no compatible mitigation exists; affected versions, the behavior change, justification and safe migration or recovery instructions must be published. A passing build alone does not establish compatibility.
