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
mirage run --dry-run --preset offline -- echo hello
```

## Network Philosophy

For the early versions, network policy should stay simple:

- `none`: no network access
- `isolated`: separate netns with explicit allow rules
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
- `internal/cli`: argument parsing and command dispatch
- `internal/spec`: sandbox config structures and validation
- `docs/architecture.md`: implementation direction
- `docs/roadmap.md`: staged plan

## Current Status

This repository currently contains the initial design skeleton and CLI scaffolding. The execution backend is not implemented yet.

