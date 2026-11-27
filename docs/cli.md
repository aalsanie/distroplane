# CLI

Distroplane distributes **already built artifacts**. Installing the CLI also installs the four providers.

## Publish

```sh
distroplane init
distroplane doctor
distroplane publish
```

`init` asks for a release ID, destination, and artifact if not supplied. Choose a numbered artifact or enter its path. Required destination metadata includes a short example and is checked as you enter it. Invalid answers can be corrected without restarting. Press Ctrl-C to cancel without writing configuration. Optional advanced fields use `--set FIELD=VALUE`; use `--credential REF=ENV` to select an environment variable name, never its value. `doctor` checks your configuration and local readiness. `publish` previews the immutable plan and asks for explicit approval **before** any publication.

In scripts, supply all `init` metadata with flags (`distroplane init --help`). For noninteractive publishing, use `--yes` only after reviewing the plan; `--dry-run` never publishes.

## Check and resume

```sh
distroplane status
distroplane resume
```

A pending release may require external review; `resume` uses its original plan and journal and does not silently start a new release. To inspect external state without dispatching new operations, use `distroplane resume --reconcile-only`. If several runs exist, select one using `--release` and `--run`.

For a saved plan, `doctor` verifies the exact retained provider bytes before running provider diagnostics. An installation upgrade does not replace the saved binding. If multiple plans exist for the configured release, select the original plan with `doctor --plan PATH`.

Provider discovery (`Describe`) and planning calls are limited to 10 and 60 seconds respectively; `doctor` retains its shorter diagnostic timeout. New plans record a 120-second operation timeout when the provider omits one. Explicit operation timeouts and earlier caller deadlines are honored. Older zero-timeout plans retain their saved identity and use a 120-second runtime budget per attempt, including preparation and provider execution. Direct provider calls without a deadline also have a 120-second process limit. Bounded cleanup may extend elapsed time. A timeout after possible publication dispatch requires reconciliation, not another publish.

**Keep `.distroplane/` and your original artifact files until the release is settled.** Never delete the journal to try publication again. Distroplane will not automatically retry a potentially completed publish.

## Other commands

| Command | Purpose |
| --- | --- |
| `distroplane validate` | Validate configuration |
| `distroplane plan` | Save an immutable plan without publishing |
| `distroplane runs` | List stored releases |
| `distroplane evidence --plan PATH --journal PATH` | Export release evidence |
| `distroplane apply` / `reconcile` | Advanced plan/journal execution or read-only destination checks |
| `distroplane retry` | Refuse new ambiguous-publication retry authorization |

`distroplane help` lists all flags. Commands use `distroplane.json` by default; use `--config PATH` for another file.

## Ambiguous publication recovery

New manual retry authorizations are disabled. Absence at reconciliation does not prove an earlier publication cannot complete. Keep the original plan, retained provider, artifacts and journal; inspect status and reconcile the destination without republishing.

Existing journal-v2 authorization records remain readable. An unfinished run with an unconsumed historical grant refuses execution; preserve its files and seek manual recovery. Do not use an older CLI to bypass the refusal. Completed or already-dispatched historical attempts remain inspectable, and outstanding dispatched effects can be reconciled.

## Exit codes

`0` success · `1` operational error · `2` invalid options · `3` invalid input · `4` pending · `5` rejected · `6` failed/cancelled.

[Configuration](configuration.md) · [Security model](security-model.md)

### Setup metadata

Choose a built artifact and its existing destination; setup does not create external accounts, package identifiers or licenses. npm reads package identity during planning. For SDKMAN, provide candidate, version and public download URL; authentication mode and platform are optional `--set` fields. Homebrew requires tap repository, manifest path, name, description, homepage, version, public artifact URL and installation layout fields; pull-request mode also requires the destination repository. WinGet requires fork repository, package identifier, version, publisher, name, actual license, description, public installer URL, architecture and pull-request repository; ZIP additionally requires nested installer type and relative file path. A single ZIP member can be entered as a plain path; advanced portable layouts retain the JSON-array `--set winget.nested-files=VALUE` form. Use `--set` for optional nonsecret settings and `--credential REF=ENV` for explicitly scoped credential sources. The setup summary lists environment names, never their values. Doctor's optional `--remote` probes HTTPS TCP reachability only, not account authorization or publication acceptance.
