# Usage

This document describes the current operator-facing CLI. For rootfs details,
see [rootfs.md](rootfs.md). For current isolation boundaries, see
[isolation.md](isolation.md). For internal runtime structure, see
[architecture.md](architecture.md).

## Host Prerequisites

- Linux
- Go `1.24.4` or newer if you build from source
- `mmdebstrap` on `PATH` when you use `mirage rootfs init`
- `unshare` on `PATH`
- `newuidmap` and `newgidmap` from the host `uidmap` package
- `ip` on `PATH`
- `iptables` and `ip6tables` on `PATH`
- `systemd-run` with a working user manager session when you use `--memory` or
  `--pids`

On Debian or Ubuntu:

```bash
sudo apt update
sudo apt install -y mmdebstrap util-linux uidmap iproute2 iptables systemd
```

Run `./bin/mirage doctor` after installation to verify the host environment.

## Build

```bash
go build -o ./bin/mirage ./cmd/mirage
```

You can also inspect the command surface without producing a binary:

```bash
go run ./cmd/mirage --help
```

## Command Surface

Current top-level commands:

- `mirage run`
- `mirage doctor`
- `mirage rootfs init`
- `mirage network-policy list`
- `mirage package`
- `mirage version`

Useful first commands:

```bash
./bin/mirage doctor
./bin/mirage network-policy list
```

Current operational note:

- use `sudo ./bin/mirage rootfs init ...` for generated rootfs work
- use `sudo ./bin/mirage run ...` for sandbox execution
- `./bin/mirage doctor` and `./bin/mirage network-policy list` remain normal
  non-`sudo` commands

## Common Flows

Generate and validate a basic rootfs:

```bash
sudo ./bin/mirage rootfs init --output /tmp/mirage/basic-rootfs
./bin/mirage doctor --rootfs /tmp/mirage/basic-rootfs --command /bin/ls
```

`mirage rootfs init` prints the exact bootstrap command and streams the
underlying tool output while it runs.

Allow Mirage to reuse a non-empty output directory only when you intend to
clear and rebuild the rootfs:

```bash
sudo ./bin/mirage rootfs init \
  --output /tmp/mirage/basic-rootfs \
  --allow-overwrite
```

Preview a run without executing it:

```bash
sudo ./bin/mirage run \
  --dry-run \
  --rootfs /tmp/mirage/basic-rootfs \
  --network-policy-file ./examples/network-policies/offline.yaml \
  -- /bin/ls /
```

Run a workload from a preset:

```bash
sudo ./bin/mirage run --preset-file ./examples/presets/openclaw-offline.yaml -- openclaw
```

Create a release bundle:

```bash
./bin/mirage package --output ./dist/mirage-linux-amd64.tar.gz --binary ./bin/mirage
```

## `mirage run`

General form:

```bash
mirage run [flags] -- <command> [args...]
```

Important flags:

- `--rootfs`
- `--network-policy-file`
- `--preset-file`
- `--ro-bind`
- `--rw-bind`
- `--env`
- `--run-as-root`
- `--cwd`
- `--hostname`
- `--stdout-log`
- `--stderr-log`
- `--memory`
- `--pids`
- `--dry-run`

Important behavior:

- `--` is required to separate Mirage flags from the workload command.
- `--preset-file` is exclusive with direct configuration flags such as
  `--rootfs`, `--network-policy-file`, bind mounts, `--cwd`, `--hostname`,
  `--memory`, and `--pids`.
- In the current operational model, invoke `mirage run` through `sudo`.
- The workload becomes sandbox PID 1. Mirage does not run a guest init system.
- Mirage starts the sandbox with an explicit managed environment. Host
  environment variables are not inherited unless you pass them with `--env`.
- By default, Mirage drops the workload to the non-root `mirage` user
  (`1000:1000`). That requires host `newuidmap` and `newgidmap`.

Example:

```bash
sudo ./bin/mirage run \
  --rootfs /tmp/mirage/basic-rootfs \
  --network-policy-file ./examples/network-policies/offline.yaml \
  -- /bin/sh
```

## Presets

`--preset-file` loads one YAML document that can bundle:

- `rootfs.path`
- `rootfs.required_commands`
- `networkPolicy` or `networkPolicyFile`
- `roBind`
- `rwBind`
- `env`
- `runAsRoot`
- `cwd`
- `hostname`
- `memory`
- `pids`
- `description`

Example:

```yaml
rootfs:
  path: /tmp/mirage/openclaw-rootfs
  required_commands:
    - node
networkPolicyFile: ../network-policies/offline.yaml
cwd: /workspace
description: Offline OpenClaw workflow preset
```

Notes:

- Use exactly one of `networkPolicy` or `networkPolicyFile`.
- Relative `networkPolicyFile` paths are resolved relative to the preset file.
- `rootfs.required_commands` is validated by `mirage doctor --preset-file ...`.

Validate a preset-managed rootfs:

```bash
./bin/mirage doctor --preset-file ./examples/presets/openclaw-offline.yaml
```

## Network Policy Use

Mirage exposes a policy-first network surface. The current bundled examples are:

- `./examples/network-policies/allow-all.yaml`: host network namespace passthrough
- `./examples/network-policies/offline.yaml`: dedicated namespace with loopback
  only
- `./examples/network-policies/block-local-egress.yaml`: dedicated namespace
  with a routed host uplink and local-network egress denies

List them with:

```bash
./bin/mirage network-policy list
```

Current backend behavior:

- allow-all policy -> host network stack
- deny-only IP/CIDR rules -> isolated network namespace with ordered rules
- egress allow semantics -> isolated namespace plus routed host uplink
- domain selectors -> explicit error

## Release Packaging

`mirage package` writes a release bundle containing:

- `bin/mirage`
- `share/mirage/network-policies`
- `share/mirage/presets`

If `--output` ends with `.tar.gz` or `.tgz`, Mirage writes a compressed
archive. Otherwise it writes an unpacked directory tree.

Example unpacked bundle:

```bash
./bin/mirage package --output ./dist/mirage-release --binary ./bin/mirage
```

After extraction:

```bash
sudo ./bin/mirage run --preset-file ./share/mirage/presets/openclaw-offline.yaml -- app
```

## Log Export

Mirage can mirror stdout and stderr to host-visible files while still writing
to the terminal:

```bash
sudo ./bin/mirage run \
  --rootfs /tmp/mirage/basic-rootfs \
  --network-policy-file ./examples/network-policies/allow-all.yaml \
  --stdout-log /tmp/app.out \
  --stderr-log /tmp/app.err \
  -- /bin/sh -c "printf 'out'; printf 'err' >&2"
```

## Related Docs

- [rootfs.md](rootfs.md)
- [isolation.md](isolation.md)
- [apps/openclaw.md](apps/openclaw.md)
- [apps/hermes.md](apps/hermes.md)
- [cgroups.md](cgroups.md)
- [architecture.md](architecture.md)
- [development.md](development.md)
