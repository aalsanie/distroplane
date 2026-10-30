# CLI

Distroplane distributes **already built artifacts**. Installing the CLI also installs the four providers.

## Publish

```sh
distroplane init
distroplane doctor
distroplane publish
```

`init` asks for a release ID, destination, and artifact if not supplied. Missing provider-specific details must be provided by you. `doctor` checks your configuration and local readiness. `publish` previews the immutable plan and asks for explicit approval **before** any publication.

In scripts, supply all `init` metadata with flags (`distroplane init --help`). For noninteractive publishing, use `--yes` only after reviewing the plan; `--dry-run` never publishes.

## Check and resume

```sh
distroplane status
distroplane resume
```

A pending release may require external review; `resume` uses its original plan and journal and does not silently start a new release. To inspect external state without dispatching new operations, use `distroplane resume --reconcile-only`. If several runs exist, select one using `--release` and `--run`.

**Keep `.distroplane/` and your original artifact files until the release is settled.** Never delete the journal to try publication again. Distroplane will not automatically retry a potentially completed publish.

## Other commands

| Command | Purpose |
| --- | --- |
| `distroplane validate` | Validate configuration |
| `distroplane plan` | Save an immutable plan without publishing |
| `distroplane runs` | List stored releases |
| `distroplane evidence --plan PATH --journal PATH` | Export release evidence |
| `distroplane apply` / `reconcile` | Advanced plan/journal execution or read-only destination checks |
| `distroplane retry` | Explicitly authorize a single reconciled, absent npm retry |

`distroplane help` lists all flags. Commands use `distroplane.json` by default; use `--config PATH` for another file.

## Explicit retry of an ambiguous npm publish

A lost npm response is **not** proof that publication failed. Reconcile the existing run, check the registry yourself, and use `retry` only after accepting the duplicate-publish risk:

```sh
distroplane retry --plan PLAN.json --journal RUN.journal --operation OP_ID --confirm-plan PLAN_ID --acknowledge-duplicate-risk
```

The authorization does not itself publish. A later `apply` continues the **same** plan and journal. Retries are rejected for other providers or without a current absent observation.

## Exit codes

`0` success · `1` operational error · `2` invalid options · `3` invalid input · `4` pending · `5` rejected · `6` failed/cancelled.

[Configuration](configuration.md) · [Security model](security-model.md)
