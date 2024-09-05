# Distroplane

Distroplane is a lightweight release distribution control plane for deterministic, isolated, verifiable publishing across package ecosystems.

## Status

Early foundation: a tested Go CLI with release build tooling. Distribution providers and publishing workflows are planned, not yet implemented.

## Development

Requires Go 1.22 or newer.

```sh
go test ./...
go run ./cmd/distroplane version --json
```

Build cross-platform release binaries and SHA256 checksums using `./scripts/build.sh`, or `./scripts/build.ps1` in Windows PowerShell.

## License

Apache License 2.0. See [LICENSE](LICENSE).
