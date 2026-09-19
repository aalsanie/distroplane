# Distroplane

Distroplane is a lightweight release distribution control plane for deterministic, isolated, verifiable publishing across package ecosystems.

## Status

Distroplane is pre-1.0. The current release-candidate line includes deterministic planning, crash-safe execution and reconciliation, evidence export, npm/SDKMAN/Homebrew/WinGet providers, and the GitHub Action integration.

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

## GitHub Actions

The repository includes a provider-neutral composite Action for `plan`, `apply`, and `reconcile`. It resolves checksum-verified release binaries, exposes normalized JSON outputs, supports native GitHub OIDC permission checks, and can upload release evidence artifacts.

See [docs/github-actions.md](docs/github-actions.md) for usage and CI security guidance.

Release safety and verification are documented in [docs/security-model.md](docs/security-model.md), [docs/reliability-validation.md](docs/reliability-validation.md), and [docs/release-verification.md](docs/release-verification.md).

## License

Apache License 2.0. See [LICENSE](LICENSE).
