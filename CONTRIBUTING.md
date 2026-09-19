# Contributing to Distroplane

Distroplane keeps package-specific behavior in separate provider executables. Keep the core provider-neutral and prefer the Go standard library. See [architecture and protocol](docs/architecture.md) for the current boundaries.

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

See [building from source](docs/release-verification.md#build-from-source) for CLI/provider binaries. CI also exercises the Action, native OS builds, parser fuzzing, and benchmarks; the workflow files contain the exact commands.

## Developer Certificate of Origin

Distroplane uses the Developer Certificate of Origin 1.1 rather than a CLA. Sign off each commit with:

```sh
git commit -s
```

The sign-off certifies that you have the right to submit the contribution under the project's Apache-2.0 license. See <https://developercertificate.org/> for the DCO text.

## Architecture changes

Do not hide architecture changes inside implementation work. Changes to provider isolation, the zero-dependency core target, persistence semantics, protocol boundaries, or security invariants must be proposed and documented explicitly before implementation.
