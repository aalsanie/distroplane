# Contributing

Keep provider behavior in provider executables, the core provider-neutral, and the Go module dependency-free.

Run Go 1.27.3 checks:

```sh
./scripts/check-dependencies.sh
./scripts/check-architecture.sh
./scripts/check-coverage.sh
go vet ./...
go test -race ./...
```

Sign commits under the [Developer Certificate of Origin](https://developercertificate.org/) with `git commit -s`.

Changes to protocol, state persistence, or recovery boundaries need tests and updates to the relevant schema or [security boundaries](docs/security-model.md). Provider-specific behavior belongs with the corresponding provider.
