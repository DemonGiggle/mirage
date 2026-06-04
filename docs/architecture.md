# Architecture

This document explains how the current Mirage implementation is put together.
It focuses on the control plane, runtime backend, and the current execution
order.

## Design Goals

Mirage is designed for practical local sandboxing:

- one direct workload per invocation
- explicit rootfs and bind-mount exposure
- reviewable network policy input
- optional cgroup-backed memory and PID limits
- host-visible logs

It is not intended to be a full container platform or a kernel-hardening
project.

## Core Terms

- `control plane`: CLI parsing, preset loading, validation, and config
  resolution
- `runner`: the Linux backend that applies namespaces, mounts, network setup,
  cgroups, and final `exec`
- `rootfs`: the filesystem tree used as guest `/`
- `preset file`: a YAML bundle of rootfs, network, bind mount, and runtime
  defaults

## Main Components

### CLI and Spec Resolution

The CLI layer is responsible for:

- parsing subcommands and flags
- loading preset files
- loading standalone network policy files
- validating mutually exclusive inputs
- printing dry-run summaries and plan notes

This layer decides what should happen. It does not enforce the sandbox itself.

### Rootfs Layer

The rootfs layer:

- bootstraps Debian rootfs trees with `mmdebstrap`
- writes the minimal guest apt policy file
- validates generated rootfs paths and command resolution for `mirage doctor`

### Runner

The runner is responsible for:

- user, PID, mount, UTS, and IPC namespaces
- network backend selection from the resolved policy
- runtime mount preparation for dedicated rootfs runs
- bind mounts
- delegated cgroup entry when limits are requested
- final rootfs handoff and workload `exec`

## Current Execution Model

One `mirage run` maps to one direct workload process tree.

The high-level sequence is:

1. parse flags
2. load preset and network policy data
3. validate the final config
4. print a run preview
5. enter the runtime backend
6. prepare namespaces, mounts, and optional cgroup limits
7. configure the selected network backend
8. `exec` the workload as sandbox PID 1

## Runtime Construction

For a dedicated non-`/` rootfs, the backend currently builds the sandbox in
this order:

1. create namespaces
2. prepare mount propagation when needed
3. mount procfs, tmpfs-backed runtime paths, and managed `/dev`
4. apply read-only and read-write bind mounts
5. hand off with `chroot`
6. execute the workload

For `--rootfs /`, Mirage skips the dedicated root mount layout. That keeps the
host root and host procfs visible inside the sandbox.

## Network Backend Selection

Mirage chooses the network backend from the resolved policy:

- allow-all policy -> host network namespace passthrough
- deny-only IP/CIDR rules -> isolated network namespace with ordered filter
  rules
- egress allow semantics -> isolated namespace plus a routed host uplink
- domain selectors -> explicit error

The public surface is intentionally policy-first even though the underlying
runtime still has multiple concrete backends.

## Resource Limits

Mirage currently supports delegated cgroup v2 limits for:

- memory
- PID count

The runtime path is:

1. launch Mirage through `systemd-run --user --scope -p Delegate=yes`
2. re-enter Mirage as a cgroup helper
3. create a Mirage-managed leaf cgroup
4. write `memory.max`, `memory.swap.max`, and `pids.max`
5. launch the normal namespace backend from that leaf

See [cgroups.md](cgroups.md) for the exact rationale and execution sketch.

## Persisted State

Mirage does not use a daemon or long-lived metadata store. The current
implementation persists only:

- host-visible stdout logs
- host-visible stderr logs
- generated rootfs trees and packaged release assets

## Related Docs

- [usage.md](usage.md)
- [rootfs.md](rootfs.md)
- [isolation.md](isolation.md)
- [cgroups.md](cgroups.md)
- [network-rule-model.md](network-rule-model.md)
- [routed-networking.md](routed-networking.md)
