# Development

This document is for contributors working on `mirage` itself. For user-facing
operation, see [usage.md](usage.md).

## Prerequisites

- Linux
- Go 1.24.4 or newer
- `unshare` on `PATH`
- `strace` on `PATH` for isolated-network behavior and related tests
- `systemd-run` with a working user manager session for delegated `--memory`
  and `--pids`

## Build

Build the CLI:

```bash
go build -o ./bin/mirage ./cmd/mirage
```

Build every package:

```bash
go build ./...
```

## Test

Run the full test suite:

```bash
go test ./...
```

The end-to-end isolated-network tests shell out to `strace`, so the effective
execution environment must be able to resolve it.

## Formatting

Format Go files with:

```bash
gofmt -w $(find . -name '*.go' -print)
```

## Repository Layout

- `cmd/mirage`: CLI entrypoint
- `cmd/probe-*`: single-purpose probe binaries used by end-to-end tests
- `e2e`: end-to-end CLI coverage
- `internal/cli`: argument parsing and command dispatch
- `internal/runner`: namespace runner, mounts, cgroups, and execution handoff
- `internal/spec`: config structures, presets, and validation
- `docs/usage.md`: user-facing command guide
- `docs/isolation.md`: current isolation matrix and caveats
- `docs/architecture.md`: internal design and run flow
- `docs/roadmap.md`: staged implementation plan

## Probe Tools

The repository includes narrow probe binaries intended to test a single
isolation property at a time:

- `probe-file-read`: attempts to read one file path
- `probe-file-write`: attempts to write one file path
- `probe-env-read`: reads one environment variable
- `probe-http-get`: performs one outbound HTTP GET
- `probe-list-procs`: lists visible numeric `/proc` entries
- `probe-readlink`: reads one symlink target
- `probe-tcp-connect`: attempts one outbound TCP connection
- `probe-spawn-child`: spawns one child process and reports the relationship

These probes are meant to stay small, obvious, and easy to reason about when a
test fails.
