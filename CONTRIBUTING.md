# Contributing to Distroplane

Keep the core provider-neutral, package-specific behavior in separate provider executables, and the Go module free of external dependencies unless an architecture decision explicitly changes that constraint. See [architecture](docs/architecture.md).

## Checks

Use Go 1.27.3 and run:

```sh
./scripts/check-dependencies.sh
./scripts/check-architecture.sh
./scripts/check-coverage.sh
go vet ./...
go test -race ./...
```

CI also runs formatting, fuzz smoke tests, native Linux/macOS/Windows tests and builds, release-build verification, benchmarks, and GitHub Action smoke tests.

## DCO

Sign off each commit under the [Developer Certificate of Origin 1.1](https://developercertificate.org/):

```sh
git commit -s
```

## Architecture changes

Changes to provider isolation, dependency policy, persistence formats, protocol boundaries, or recovery/security invariants require corresponding tests and documentation. Update the canonical guide for the affected behavior: [CLI](docs/cli.md), [configuration](docs/configuration.md), [architecture](docs/architecture.md), [security](docs/security-model.md), or the destination-specific provider README. Do not duplicate implementation contracts across pages.
