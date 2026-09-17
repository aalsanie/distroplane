# Contributing to Distroplane

Distroplane is developed with a small-core, protocol-first architecture. Contributions must preserve the provider-neutral boundaries and dependency discipline.

## Development requirements

- Go 1.27.1
- no new external Go module dependency without an explicit architecture decision
- `gofmt` clean
- `go vet ./...` clean
- `go test ./...` clean
- `go test -race ./...` clean
- at least 90% statement coverage for every first-party Go package and repository-wide

Run the repository checks before opening a pull request:

```sh
./scripts/check-dependencies.sh
./scripts/check-architecture.sh
./scripts/check-coverage.sh
go vet ./...
go test -race ./...
```

## Developer Certificate of Origin

Distroplane uses the Developer Certificate of Origin 1.1 rather than a CLA. Sign off each commit with:

```sh
git commit -s
```

The sign-off certifies that you have the right to submit the contribution under the project's Apache-2.0 license. See <https://developercertificate.org/> for the DCO text.

## Architecture changes

Do not hide architecture changes inside implementation work. Changes to provider isolation, the zero-dependency core target, persistence semantics, protocol boundaries, or security invariants must be proposed and documented explicitly before implementation.
