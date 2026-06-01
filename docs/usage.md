# Usage

This document describes how to run `mirage`. For implementation details, see
[architecture.md](architecture.md). For current guarantees and caveats, see
[isolation.md](isolation.md). For rootfs choice, built-in templates, and
validation guidance, see [rootfs.md](rootfs.md).

## Host Prerequisites

- Linux
- Go 1.24.4 or newer if building from source
- `unshare` on `PATH` for namespace-backed execution
- `ip` on `PATH` for isolated network namespace setup
- `iptables` and `ip6tables` on `PATH` for non-host `networkPolicy`
  enforcement
- `systemd-run` with a working user manager session for delegated `--memory`
  and `--pids`

The exact package names vary by distribution. Run `./bin/mirage doctor` after
installing them to confirm the local environment.

## Build

Build the CLI binary:

```bash
go build -o ./bin/mirage ./cmd/mirage
```

You can also run the CLI directly from source:

```bash
go run ./cmd/mirage --help
```

## Common Commands

Environment check:

```bash
./bin/mirage doctor
```

Generate a runnable rootfs from a built-in template:

```bash
./bin/mirage rootfs init --template basic --output /srv/mirage/basic-rootfs
```

Reuse an existing non-empty rootfs output path only when you explicitly want
Mirage to overwrite generated files:

```bash
./bin/mirage rootfs init \
  --template basic \
  --output /srv/mirage/basic-rootfs \
  --allow-overwrite
```

Validate a rootfs before running inside it:

```bash
./bin/mirage doctor --rootfs /srv/mirage/basic-rootfs --command /bin/ls
```

Preview a run without executing it:

```bash
./bin/mirage run --dry-run --preset-file ./examples/presets/openclaw-offline.yaml -- /bin/echo hello
```

## Command Pattern

The general form is:

```bash
mirage rootfs init --template <name> --output <path>
mirage run [sandbox options...] -- command [args...]
```

Common options include:

- `--rootfs`: root filesystem for the sandbox
- `--preset-file`: single preset YAML file that can bundle rootfs, network
  policy, bind mounts, cwd, hostname, memory, and PID limits
- `--network-policy-file`: standalone `networkPolicy` YAML file
- `--ro-bind`: read-only `host:guest` bind mount
- `--rw-bind`: read-write `host:guest` bind mount
- `--env`: explicit sandbox environment variable in `KEY=VALUE` form
- `--run-as-root`: keep the workload as root instead of Mirage's default non-root `mirage` user
- `--cwd`: working directory inside the sandbox
- `--stdout-log` and `--stderr-log`: host-visible log export targets
- `--memory` and `--pids`: delegated cgroup v2 limits

`--memory` and `--pids` are not implemented as plain process ulimits. Mirage
launches a delegated user scope through `systemd-run`, then creates a child
cgroup and writes the real cgroup v2 limit files there. See
[cgroups.md](cgroups.md) for the exact flow and the cgroup tree sketch.

`mirage run` always uses the direct one-command model: the requested workload
becomes sandbox PID 1. Network behavior is resolved from either a preset file
or `--network-policy-file`. The current backend supports three
runtime paths:

- allow-all policy -> host namespace passthrough
- deny-only policy -> dedicated network namespace with ordered loopback,
  ingress, and egress allow/deny enforcement
- policy with egress allow semantics -> dedicated network namespace plus a
  routed host uplink with ordered rule enforcement

Deferred selectors such as domain-based egress still fail explicitly instead of
silently degrading.

Mirage does not inherit arbitrary host environment variables into the sandboxed
workload. The managed sandbox environment starts from an explicit `PATH` and
adds any `--env KEY=VALUE` entries you provide.

By default, Mirage drops the workload to the non-root `mirage` user (UID/GID
1000) and synthesizes matching `/etc/passwd` and `/etc/group` entries at
runtime. Use `--run-as-root` only when the workload actually needs root inside
the sandbox.

## Rootfs Workflows

When you use a custom `--rootfs`, that root filesystem must contain:

- the command you want to launch
- any runtime libraries or files it needs
- any target paths for bind mounts inside the guest tree

For quick local sanity checks, `--rootfs /` is the simplest option. It is also
the weakest rootfs mode. See [isolation.md](isolation.md) for the exact
tradeoffs.

When bind mounts target `--rootfs /`, `mirage` expects the guest path to
already exist on the host root. It will not create new host-side mountpoints in
that mode.

For the built-in template catalog, the rootfs schema, and `rootfs init`
behavior, see [rootfs.md](rootfs.md).

## Bind Mounts

Bind mounts use `host:guest` pairs.

Rules:

- host paths must be absolute
- guest paths must be absolute
- host paths must already exist
- guest `/` is not a valid bind target

Example:

```bash
./bin/mirage run \
  --preset-file ./examples/presets/openclaw-offline.yaml \
  -- /bin/sh
```

## Presets

`mirage` accepts a single preset document through `--preset-file`. The preset
file can bundle the same configuration you would otherwise pass with several
flags, including rootfs path, bind mounts, working directory, hostname, memory,
PID limits, and network policy.

Example preset file:

```yaml
rootfs:
  path: /srv/mirage/openclaw-rootfs
  template: openclaw-developer
  required_commands:
    - node
networkPolicyFile: ../network-policies/offline.yaml
cwd: /workspace
description: Team preset for local-only agent work
```

Use it with:

```bash
./bin/mirage run --preset-file ./examples/presets/openclaw-offline.yaml -- app
```

Inside a preset file, use exactly one of:

- `networkPolicy`: inline policy details
- `networkPolicyFile`: reference a standalone policy YAML file

When `--preset-file` is used, Mirage rejects overlapping direct flags such as
`--rootfs`, `--network-policy-file`, `--cwd`, bind mounts, hostname, memory,
and PID limits. Move that configuration into the preset file instead.

## Network Usage

The current network philosophy is intentionally narrow and policy-first:

- use `./examples/network-policies/offline.yaml` when the workload should not
  reach non-loopback network
- use `./examples/network-policies/allow-all.yaml` when the workload truly
  needs the host network stack
- use `./examples/network-policies/block-local-egress.yaml` when the workload
  should keep public egress while denying common local-network ranges
- use `--network-policy-file` for a reviewable standalone policy document
- expect domain-backed egress selectors to fail explicitly until the runtime can
  materialize them safely

Example:

```bash
./bin/mirage run \
  --rootfs /srv/mirage/rootfs \
  --network-policy-file ./examples/network-policies/allow-all.yaml \
  -- app
```

## Log Export

You can tee workload output into host-visible files while preserving console
output:

```bash
./bin/mirage run \
  --rootfs / \
  --network-policy-file ./examples/network-policies/allow-all.yaml \
  --stdout-log /tmp/app.out \
  --stderr-log /tmp/app.err \
  -- /bin/sh -c "printf 'out'; printf 'err' >&2"
```

## Related Docs

- [rootfs.md](rootfs.md): rootfs selection, template catalog, and generation
  details
- [applications.md](applications.md): application-oriented setup flows such as
  OpenClaw installation and launch
- [isolation.md](isolation.md): exact current behavior and caveats
- [cgroups.md](cgroups.md): how delegated systemd scopes and cgroup v2 limits
  are applied
- [architecture.md](architecture.md): internal implementation model
- [development.md](development.md): build, tests, and contributor workflow
