# Distroplane

Distroplane is a lightweight release distribution control plane for deterministic, isolated, verifiable publishing across package ecosystems.

## Status

Distroplane is in early development. The current repository baseline establishes the engineering, testing, security, and release foundations; provider and distribution functionality will follow incrementally.

## Design principles

- small, provider-neutral core
- out-of-process providers
- standard-library-first Go implementation
- deterministic plans and digest-based artifact identity
- append-only execution history
- idempotency-aware execution and reconciliation
- least-privilege credential isolation
- local-first operation with no required hosted service

## Development

Distroplane targets Go 1.27.1.

```sh
go test ./...
go test -race ./...
./scripts/check-coverage.sh
./scripts/check-dependencies.sh
./scripts/check-architecture.sh
```

Build release binaries with:

```sh
./scripts/build.sh
```

On Windows PowerShell:

```powershell
./scripts/build.ps1
```

## License

Apache License 2.0. See [LICENSE](LICENSE).
