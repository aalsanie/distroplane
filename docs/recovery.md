# Recover a release

Check the existing release and reconcile an uncertain destination outcome:

```sh
distroplane runs
distroplane status
distroplane resume --reconcile-only
distroplane resume
```

When several runs exist, select the intended one with `--release ID` and `--run ID`. Keep the original `.distroplane/` state, built artifacts, and provider binaries. A pending external review can require another observation later; a lost response is not permission to publish again.

For a script, use `--json` and inspect the command's exit code: `0` complete, `1` operational error, `2` usage, `3` invalid input, `4` pending, `5` rejected, `6` failed/cancelled. `distroplane <command> --help` lists options.

## Explicit retry of an ambiguous npm publish

After reconciling the original run, independently check the registry. If it reports the version absent and you accept the duplicate-publish risk, authorize a single retry:

```sh
distroplane retry --plan PLAN.json --journal RUN.journal --operation OP_ID --confirm-plan PLAN_ID --acknowledge-duplicate-risk
```

The authorization records the decision. Continue through the original plan and journal with `distroplane apply`. This exception is specific to npm; other providers require their own reconciliation outcome.

For advanced integrations, `distroplane plan`, `apply`, `reconcile`, and `evidence` expose the saved plan, journal, and evidence.
