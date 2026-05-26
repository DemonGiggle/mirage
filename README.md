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
- explicit direct-workload and init-oriented runtime modes
- chroot-based rootfs handoff when using a non-`/` rootfs
- a shared V1 rootfs template schema with curated built-in templates
- read-only and read-write bind mounts
- built-in network presets and local preset files
- observed network enforcement for `--net isolated`
- stdout and stderr export to host-visible log files
- delegated cgroup v2 memory and PID limits
- tracked sandbox lifecycle commands for init-mode runs (`sandbox start/status/stop/logs`)

The runtime modes target different operator shapes:

- **direct exec**: one foreground workload becomes sandbox PID 1
- **guest init**: a guest init entrypoint becomes sandbox PID 1, and Mirage can
  track the sandbox through a named host-side lifecycle entry

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

Generate a runnable rootfs from the built-in basic template:

```bash
./bin/mirage rootfs init --template basic --output /srv/mirage/basic-rootfs
```

The built-in templates are:

- `basic`: minimal shell and inspection tools
- `node`: `basic` plus Node.js, npm, npx, `/workspace`, and CA trust material
- `python`: `basic` plus Python, pip, `/workspace`, and CA trust material
- `openclaw`: compatibility OpenClaw template used by current presets
- `openclaw-chat-only`: `node` plus locale/tzdata runtime data and `openssl`
- `openclaw-work`: `openclaw-chat-only` plus common shell, archive, patch, JSON, and search tooling
- `openclaw-developer`: `openclaw-work` plus VCS, editors, Python, SQLite, and build-toolchain entrypoints
- `openclaw-admin`: `openclaw-developer` plus networking, process, and capability tools
- `openclaw-root`: `openclaw-admin` plus package-management, tracing, debugging, namespace, and filesystem tools
- `openclaw-systemd`: `openclaw` plus guest `systemd` tooling, systemd
  directories, and an empty `/etc/machine-id`

See [docs/usage.md](docs/usage.md#rootfs-templates) for the exact prepared
layout behind each template.

Validate that rootfs before trying to run inside it:

```bash
./bin/mirage doctor --rootfs /srv/mirage/basic-rootfs --command /bin/ls
```

Run a simple command with the built-in offline preset:

```bash
./bin/mirage run --rootfs / --preset offline -- /bin/echo hello
```

Run a guest init entrypoint as sandbox PID 1:

```bash
./bin/mirage run \
  --rootfs /srv/mirage/systemd-rootfs \
  --net host \
  --runtime-mode init \
  -- /usr/lib/systemd/systemd
```

Init mode currently targets a narrow guest-systemd contract: unified cgroup v2,
a delegated host `systemd-run --user --scope` leaf, and guest-visible
`/sys/fs/cgroup` exposure inside dedicated rootfs runs. `--rootfs /` is not
part of that init-mode contract. Init-mode runs also get a managed `/dev`, a
read-only `/sys`, runtime state directories under `/run`, and a `container=mirage`
environment hint for guest init processes.

Track a long-lived init-mode sandbox from the host:

```bash
./bin/mirage sandbox start \
  --name openclaw \
  --rootfs /srv/mirage/systemd-rootfs \
  --service-unit openclaw.service \
  -- /usr/bin/systemd

./bin/mirage sandbox status --name openclaw
./bin/mirage sandbox logs --name openclaw --lines 100
./bin/mirage sandbox stop --name openclaw
```

This tracked lifecycle stays intentionally small:

- one named sandbox maps to one host-side user-systemd scope
- logs are surfaced through Mirage-managed stdout/stderr and launch log files
- live namespace entry and journal extraction are not yet general-purpose host
  commands

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
