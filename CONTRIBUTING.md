# Contributing to Distroplane

Distroplane is intentionally small and architecture-driven. Contributions should preserve the provider-neutral core, minimal dependency surface, and explicit correctness guarantees.

## Before contributing

- Keep changes focused and reviewable.
- Do not add external Go dependencies without an explicit architectural decision explaining why the standard library is insufficient.
- Do not introduce provider-specific concepts into the core domain.
- Add or update tests with every behavior change.
- Keep every first-party package containing executable statements at or above 90% statement coverage.
- Run formatting, vet, tests, race tests, and the same validation required by CI.

## Developer Certificate of Origin

Distroplane uses the Developer Certificate of Origin 1.1 instead of a CLA.

Every commit must include a `Signed-off-by` trailer certifying that you have the right to contribute the change under the project's license:

```text
Signed-off-by: Your Name <you@example.com>
```

Git can add this automatically for a commit:

```sh
git commit -s
```

The full certificate is available in [DCO](DCO).

## License

By contributing, you agree that your contribution is licensed under the Apache License 2.0.
