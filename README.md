# Distroplane

Distroplane is a lightweight release distribution control plane for deterministic, isolated, verifiable publishing across package ecosystems.

## Status

Early foundation: a small Go CLI with a provider-neutral design. Distribution providers and publishing workflows are planned, not yet implemented.

## Development

Requires Go 1.22 or newer.

```sh
go run ./cmd/distroplane help
go run ./cmd/distroplane version
go run ./cmd/distroplane version --json
```

The version command exposes version, commit, and build date metadata. Release builds can inject these values at link time.

## License

Apache License 2.0. See [LICENSE](LICENSE).
