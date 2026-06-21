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

## Internal Self Re-exec

Mirage's runtime setup is split across multiple execution phases inside the
same binary.

In practice, `mirage run` does not go straight from CLI parsing to workload
execution in a single process. Instead, Mirage may launch itself again with
internal helper subcommands so each phase can run in the right host or
namespace context.

The current helper phases are:

- `__cgroup-exec` for delegated cgroup v2 setup when `--memory` or `--pids`
  is requested
- `__backend-exec` for namespace setup, mount preparation, network backend
  setup, rootfs handoff, and the final workload `exec`

That means the effective process flow can look like:

1. `mirage run ...`
2. optional `mirage __cgroup-exec ...`
3. `unshare ... mirage __backend-exec ...`
4. final `exec` of the workload

This is still a single Mirage invocation from the user's point of view. The
extra Mirage processes are internal runtime helpers, not separate user-facing
commands.

Mirage uses this pattern because the setup phases have conflicting process
context requirements, so they cannot all be done cleanly in one long-running
process.

The main constraints are:

- cgroup delegation must happen after entering the delegated `systemd-run`
  scope, because the helper needs to discover and modify the cgroup subtree it
  is actually running in
- user-namespace setup changes the process identity and capability model, so
  later steps must run only after that transition has completed
- some UID/GID mappings are written by the parent while the child is paused,
  which requires a distinct pre-exec launcher process and a separate child
  process waiting to continue
- mount namespace setup, network configuration, and `chroot` preparation must
  happen before the workload starts, but after the namespace topology has been
  established
- the final workload handoff uses `exec`, which replaces the current process
  image entirely, so any Mirage logic that still needs to run must happen in an
  earlier helper phase

In short, Mirage is not re-executing itself for stylistic reasons. It is doing
so because each phase needs a different combination of cgroup placement,
namespace state, identity mapping, and pre-`exec` control, and those
requirements do not fit into a single uninterrupted process phase.

## Runtime Construction

For a dedicated non-`/` rootfs, the backend currently builds the sandbox in
this order:

1. create namespaces
2. prepare mount propagation when needed
3. mount procfs, tmpfs-backed runtime paths, and managed `/dev`
4. apply read-only and read-write bind mounts
5. hand off with `chroot`
6. execute the workload

That `chroot` handoff gives the workload the selected rootfs as its normal
path-based filesystem view, but Mirage does not claim the same rootfs model as
a full container runtime with a more complete root filesystem switch.

## Current Rootfs Tradeoff

Mirage uses a dedicated mount namespace and prepares a dedicated rootfs tree,
including a fresh `/proc`, tmpfs-backed runtime paths, and managed parts of
`/dev`. The final filesystem handoff currently uses `chroot`.

This gives Mirage a practical and lightweight rootfs boundary for local tools,
but it is not the same model used by a full container runtime that performs a
more complete root filesystem switch.

Current implications:

- Mirage should not be treated as providing container-runtime-equivalent rootfs
  isolation.
- The filesystem boundary is simpler, but also less complete, than a fuller
  container rootfs handoff.
- The main risk areas are bind mounts, symlink handling, mount layout, and
  unexpected host path visibility if Mirage gets those details wrong.
- Mirage is intended for practical sandboxing of local tools and agents, not as
  a hardened container security boundary.

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

1. launch Mirage through `systemd-run --scope -p Delegate=yes`
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
