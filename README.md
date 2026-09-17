# Distroplane

A lightweight release distribution control plane for deterministic, isolated, verifiable publishing across package ecosystems.

> **Status:** early development. The repository is establishing the production foundation before the provider protocol and release engine are implemented.

## Purpose

Distroplane turns release intent into a deterministic plan, executes distribution targets through isolated provider processes, reconciles asynchronous external state, and records verifiable evidence of what happened.

It is designed to work naturally in GitHub Actions while remaining a local-first, CI-neutral command-line tool.

## Engineering principles

- small provider-neutral core;
- out-of-process providers;
- standard-library-first Go implementation;
- deterministic planning before side effects;
- append-only execution history;
- idempotency-aware execution and reconciliation;
- least-privilege credential exposure;
- no required daemon, database, container runtime, or hosted service.

## Development

Distroplane requires Go 1.27.1 or later in the Go 1.27 line.

```sh
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/distroplane
```

The project enforces at least 90% statement coverage for every first-party package that contains executable statements and at least 90% repository-wide coverage.

## License

Apache License 2.0. See [LICENSE](LICENSE).
