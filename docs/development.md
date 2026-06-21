# Development

This document is for contributors working on Mirage itself.

## Prerequisites

- Linux
- Go `1.24.4` or newer
- `unshare` on `PATH`
- `newuidmap` and `newgidmap` on `PATH`
- `systemd-run` when testing delegated `--memory` and `--pids`

For full operator prerequisites, see [usage.md](usage.md).

## Build

Build the CLI:

```bash
go build -o ./bin/mirage ./cmd/mirage
```

Build all packages:

```bash
go build ./...
```

## Test

Run the unit and integration suites:

```bash
go test ./...
```

Some end-to-end tests require namespace, uidmap, and local socket capabilities
that may be unavailable in restricted environments.

## Releases

Push a version tag like `v0.1.0` to publish a GitHub release. The release
workflow:

- runs `go test ./...`
- builds `./bin/mirage`
- runs `./bin/mirage package --output ./dist/mirage-<version>-linux-<arch>.tar.gz --arch <arch>` for `x86_64`, `arm64`, `arm32`, and `riscv64`
- uploads the package archive plus `SHA256SUMS.txt` to the GitHub release page

## Formatting

Format Go files with:

```bash
gofmt -w $(find . -name '*.go' -print)
```

## Repository Layout

- `cmd/mirage`: CLI entrypoint
- `cmd/probe-*`: small single-purpose test binaries
- `e2e`: end-to-end coverage
- `examples`: bundled example policies and presets exported by `mirage package`
- `internal/cli`: subcommand parsing and command dispatch
- `internal/rootfs`: template loading, generation, and validation
- `internal/runner`: namespace backend, mounts, network, cgroups, and final exec
- `internal/spec`: preset and network-policy data structures and validation
- `docs/`: user and technical documentation

## Probe Tools

The probe binaries each target one isolation property so failures stay small
and obvious:

- `probe-env-read`
- `probe-file-read`
- `probe-file-write`
- `probe-http-get`
- `probe-list-procs`
- `probe-open-ptmx`
- `probe-readlink`
- `probe-spawn-child`
- `probe-spawn-many`
- `probe-tcp-connect`
