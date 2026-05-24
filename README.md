# mirage

`mirage` is a lightweight Linux sandbox launcher for running local tools inside
an explicit execution envelope without adopting a full container stack.

The project target is pragmatic:

- Linux only
- CLI first
- namespace-based isolation
- explicit rootfs and bind-mount control
- simple network presets
- enough guardrails for daily agent and developer workflows

`mirage` is not trying to become another Docker or Kubernetes clone.

## What It Solves

Tools such as OpenClaw often need a middle ground:

- stronger isolation than running directly on the host
- less setup than a VM or full container platform
- network controls that default to deny
- explicit writable paths
- a path from ad hoc runs to reusable presets

## Current Scope

Today the project includes:

- namespace-backed process-tree execution on Linux
- chroot-based rootfs handoff when using a non-`/` rootfs
- read-only and read-write bind mounts
- built-in network presets and local preset files
- observed network enforcement for `--net isolated`
- stdout and stderr export to host-visible log files
- delegated cgroup v2 memory and PID limits

Important caveat:

- `--rootfs /` is intentionally convenient for local testing, but it does not
  provide the same filesystem and `/proc` behavior as a dedicated rootfs

For exact behavior and current limitations, see
[docs/isolation.md](docs/isolation.md).

## Quick Start

Build the CLI:

```bash
go build -o ./bin/mirage ./cmd/mirage
```

Check the local environment:

```bash
./bin/mirage doctor
```

Run a simple command with the built-in offline preset:

```bash
./bin/mirage run --rootfs / --preset offline -- /bin/echo hello
```

Run with a dedicated rootfs and explicit mounts:

```bash
./bin/mirage run \
  --rootfs /srv/mirage/rootfs \
  --ro-bind /home/gigo/workspace/project:/workspace \
  --rw-bind /home/gigo/workspace/project-tmp:/tmp/work \
  --preset openai \
  --cwd /workspace \
  -- app
```

## Documentation Map

- [docs/usage.md](docs/usage.md): installation assumptions, command patterns,
  presets, and common run examples
- [docs/isolation.md](docs/isolation.md): current isolation matrix, guarantees,
  and known caveats
- [docs/architecture.md](docs/architecture.md): control-plane and backend
  design, namespace setup, and run flow
- [docs/development.md](docs/development.md): build, test, formatting, and repo
  layout for contributors
- [docs/roadmap.md](docs/roadmap.md): staged implementation plan

## Status

The current backend is useful, but still transitional:

- rootfs handoff still ends with `chroot`, not `pivot_root`
- isolated networking is enforced through observed connect attempts rather than
  a full routable firewall-backed model
- proc and mount hardening around `--rootfs /` is intentionally documented as a
  current limitation rather than a solved property

The implementation details behind those limitations are documented in
[docs/architecture.md](docs/architecture.md), while the operator-visible impact
is documented in [docs/isolation.md](docs/isolation.md).
