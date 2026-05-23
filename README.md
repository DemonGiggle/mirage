# mirage

`mirage` is a lightweight Linux sandbox launcher for running long-lived local tools such as OpenClaw inside an isolated execution environment without dragging in a full container stack.

The project target is pragmatic:

- Linux only
- CLI first
- low-friction setup
- root filesystem isolation
- process isolation
- simple network presets
- enough guardrails for daily agent use

It is intentionally not trying to become another Docker or Kubernetes clone.

## Problem Statement

OpenClaw and similar agent runtimes often need a middle ground:

- stronger isolation than "just run it on the host"
- less complexity than full containers or VMs
- network controls that default to deny
- bind mounts and writable paths that are explicit
- a way to observe denied or unexpected network access before freezing a preset

`mirage` aims to provide exactly that.

## Planned V1 Scope

- mount namespace isolation
- PID namespace isolation
- network namespace with simple presets
- chroot or pivot-root based rootfs isolation
- explicit read-only and read-write bind mounts
- cgroup v2 limits for memory, CPU, and PID count
- CLI preview mode for config validation
- warn-oriented network policy mode

## Planned Non-Goals For V1

- OCI compatibility
- image registry support
- multi-host orchestration
- polished domain-level egress policy
- generalized package management inside sandboxes
- a custom firewall DSL

## Why Go

Go is a good fit here because it gives us:

- a single static Linux binary
- straightforward syscall and namespace work
- easy integration with existing host binaries like `mount`, `unshare`, `nft`, and `ip`
- low runtime overhead

## Development

### Prerequisites

- Linux
- Go 1.24.4 or newer
- `unshare` available on `PATH` for namespace-backed runs
- `strace` available on the effective execution `PATH` for observed isolated
  networking and the isolated-network end-to-end tests
- `systemd-run` with a working user manager session for delegated `--memory` and `--pids`

### Build

Build the CLI binary:

```bash
go build -o ./bin/mirage ./cmd/mirage
```

Build every package in the repository:

```bash
go build ./...
```

### Run

After building the CLI, you can sanity-check the local binary with:

```bash
./bin/mirage doctor
```

You can also run the CLI directly from source:

```bash
go run ./cmd/mirage --help
```

When you use a custom `--rootfs`, that root filesystem has to contain the
command you are launching and any runtime files it needs. For quick local
sanity checks, `--rootfs /` is the simplest option.

When bind mounts target `--rootfs /`, `mirage` expects the guest mount point to
already exist on the host root. It will not create new host-side mountpoint
files or directories for you.

Bind mounts use `host:guest` pairs. The host path must be absolute and exist on
the host, and the guest path must be an absolute path inside the sandbox.

### Test

Run the full test suite:

```bash
go test ./...
```

The end-to-end isolated-network tests shell out to `strace`, so they require
`strace` to be resolvable from the effective execution environment. When you use
`--rootfs /`, that means the host `PATH`. When you use a custom `--rootfs`, the
runner still needs to be able to resolve `strace` from the command environment
that launches the observed workload.

The current observed implementation of `--net isolated` and `--warn net`
depends on that same `strace` availability.

### Formatting

CI checks that Go files are `gofmt`-formatted before running the tests:

```bash
gofmt -w $(find . -name '*.go' -print)
```

## Terminology

These terms will appear repeatedly in the project docs:

- `control plane`: the CLI-facing layer that parses flags, resolves presets, validates config, and decides what should happen
- `sandbox backend`: the execution engine that actually applies isolation primitives such as namespaces, rootfs switching, mounts, firewall rules, and cgroups
- `host passthrough`: a fallback mode where `mirage` still validates and orchestrates execution, but ultimately runs the target command on the host without namespace isolation
- `isolated process tree`: the full set of processes rooted at the sandbox entry command, including later child processes spawned by that workload
- `rootfs`: the filesystem tree that becomes the process view of `/` inside the sandbox
- `bind mount`: an explicit mapping of a host path into the sandbox, typically as read-only or read-write
- `network preset`: a reusable policy bundle such as `offline` or `openai` that sets a baseline egress stance
- `warn mode`: an observation mode that records denied or suspicious network attempts so the user can refine future presets
- `host log export`: sending workload stdout and stderr to host-visible files while still showing normal console output

## Command Model

The first CLI pass is centered around a few simple verbs:

```bash
mirage run \
  --rootfs /srv/mirage/openclaw-rootfs \
  --ro-bind /home/gigo/.openclaw/workspace:/workspace \
  --rw-bind /home/gigo/.openclaw/media:/media \
  --net none \
  --warn net \
  --cwd /workspace \
  -- openclaw gateway --port 18789
```

```bash
mirage run \
  --rootfs /srv/mirage/openclaw-rootfs \
  --preset openai \
  --warn net \
  --allow-host github.com:443 \
  -- openclaw gateway --port 18789
```

```bash
mirage doctor
mirage preset list
mirage run --dry-run --rootfs / --preset offline -- echo hello
mirage run --rootfs / --net host --stdout-log /tmp/app.out --stderr-log /tmp/app.err -- /bin/sh -c "printf 'out'; printf 'err' >&2"
mirage run --rootfs /srv/rootfs --preset-file ./presets.json --preset team-openai -- app
```

## Preset Files

`mirage` can merge built-in presets with a local JSON preset file:

```json
{
  "presets": [
    {
      "name": "team-openai",
      "network": "isolated",
      "allow_hosts": ["api.openai.com:443", "github.com:443"],
      "description": "Team preset for OpenAI-backed agent work"
    }
  ]
}
```

Use it with `mirage preset list --preset-file ./presets.json` or
`mirage run --preset-file ./presets.json --preset team-openai -- ...`.

Built-in OpenClaw-oriented presets now include:

- `openclaw-offline`
- `openclaw-openai`

## Network Philosophy

For the early versions, network policy should stay simple:

- `none`: no network access
- `isolated`: separate netns with observed connect-attempt policy enforcement
- `host`: no network namespace isolation

Presets provide a better first experience than raw firewall rule authoring. The project should eventually support a workflow like this:

1. run with a restrictive preset
2. enable warn mode
3. observe blocked or attempted accesses
4. save or refine an allow list
5. promote it into a reusable preset

That is much friendlier than forcing users to handcraft packet filter rules on day one.

## Repository Layout

- `cmd/mirage`: CLI entrypoint
- `cmd/probe-*`: single-purpose sandbox probe binaries for escape and isolation testing
- `e2e`: end-to-end CLI tests
- `internal/cli`: argument parsing and command dispatch
- `internal/runner`: host-side execution and log export bridge
- `internal/spec`: sandbox config structures and validation
- `docs/architecture.md`: implementation direction
- `docs/roadmap.md`: staged plan

## Probe Tools

The repository also carries a small probe suite meant to run inside `mirage`.

- `probe-file-read`: attempts to read exactly one file path
- `probe-file-write`: attempts to write exactly one file path
- `probe-env-read`: reads exactly one environment variable
- `probe-http-get`: performs exactly one outbound HTTP GET
- `probe-list-procs`: lists visible numeric `/proc` entries
- `probe-readlink`: reads exactly one symlink target
- `probe-tcp-connect`: attempts exactly one outbound TCP connection
- `probe-spawn-child`: spawns one child process and reports the parent/child relationship

These are intentionally narrow. Each probe should test one isolation property, fail loudly, and stay easy to reason about in end-to-end tests.

## Current Status

This repository now has:

- preset-aware config parsing and validation
- dry-run preview output
- host-side stdout/stderr log export
- a first Linux namespace runner for isolated process-tree execution
- explicit rootfs runtime layout preparation for `/proc`, `/tmp`, and `/run`
- read-only and read-write bind mount enforcement in the namespace backend
- cgroup v2 enforcement for memory and PID limits via delegated systemd user scopes
- observed network policy enforcement for isolated-mode connect attempts
- host-side warn-mode recording for observed network connect attempts
- end-to-end CLI tests for preview and log export

What is still missing is the fuller sandbox backend: pivot-root style rootfs
handoff and routable isolated networking are still planned rather than
enforced.
