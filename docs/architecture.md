# Architecture

This document describes how `mirage` is structured internally. For operator
usage, see [usage.md](usage.md). For the exact user-visible isolation behavior,
see [isolation.md](isolation.md).

## Design Goals

`mirage` should make it easy to launch an application inside a narrow,
repeatable execution envelope with:

- isolated process tree
- explicit filesystem exposure
- optional network isolation
- host-visible logs
- constrained resources

The goal is practical local sandboxing for developer and agent workflows, not
defense against a determined kernel-level adversary.

## Core Terms

- `control plane`: the CLI-facing layer that parses flags, resolves presets,
  validates options, and builds the final run specification
- `sandbox backend`: the Linux-specific execution layer that applies
  namespaces, rootfs setup, mounts, network behavior, and cgroups
- `rootfs`: the filesystem tree presented as `/` to the sandboxed process
- `rootfs template`: a reusable description of files, directories, and binaries
  that should exist in a generated rootfs
- `bind mount`: an explicit mapping from a host path into the sandbox, either
  read-only or read-write
- `network preset`: a named policy bundle that sets the default network stance
- `warn mode`: an observation mode that records attempted network access for
  later review

## Mental Model

The intended model is simple:

1. the CLI resolves a final config
2. the runner creates the requested isolation context
3. the workload executes inside that context
4. optional logs and observation records are persisted on the host

`mirage` is therefore a thin control plane in front of normal Linux isolation
primitives, not a custom container platform.

## High-Level Components

### CLI and Spec Resolution

The CLI is responsible for:

- parsing command-line flags
- loading built-in and file-backed presets
- resolving built-in rootfs templates where rootfs-oriented commands need them
- merging inline overrides
- validating incompatible or incomplete settings
- producing dry-run output

This layer decides what should happen. It does not enforce the sandbox itself.

### Runner

The runner is responsible for:

- creating user, PID, mount, UTS, and IPC namespaces
- creating a network namespace when requested
- preparing runtime mounts such as `/proc`, `/tmp`, and `/run`
- applying bind mounts
- performing rootfs handoff
- entering delegated cgroup v2 limits when configured
- executing the final command

### Observation and State

The current implementation can persist:

- warn-mode network observations
- host-visible stdout and stderr logs

This state is intentionally plain and local rather than hidden behind a daemon.

## Current Runtime Construction

The backend currently builds the sandbox in this order:

1. create namespaces with `unshare`
2. prepare mount propagation when a separate mount layout is needed
3. mount `proc`, `tmpfs`, and `run` under a non-`/` rootfs
4. apply read-only and read-write bind mounts
5. hand off into the rootfs with `chroot`
6. execute the workload directly or under observed-network instrumentation

That sequencing explains an important current limitation:

- when `--rootfs /` is used, `mirage` does not create a fresh rootfs mount
  layout, so the host root remains visible and the existing `/proc` mount stays
  in place

The operator-visible consequences are documented in
[isolation.md](isolation.md).

## Namespace Model

One `mirage run` invocation corresponds to one isolated process tree.

The workload root process and any later child processes should inherit the same
namespace boundary automatically. This is the main reason the implementation
uses standard Linux namespace setup rather than a host-side subprocess wrapper.

## Network Model

The current network modes are intentionally small:

- `host`: no network namespace isolation
- `none`: separate network namespace with no network access
- `isolated`: separate network namespace with observed connect-attempt
  enforcement

The `isolated` implementation is currently observation-driven rather than a
full routable firewall model. That is why the project still treats network
architecture as incomplete rather than finished.

## Rootfs Direction

The longer-term rootfs direction remains:

- define reusable rootfs templates that stay separate from network presets
- prepare a dedicated rootfs
- mount required runtime paths explicitly
- apply bind mounts
- switch root with `pivot_root` where practical

Current state:

- non-`/` rootfs runs get a prepared runtime layout
- handoff still finishes with `chroot`
- `--rootfs /` remains a convenience mode, not a strong rootfs boundary

## Cgroup Direction

The backend currently supports delegated cgroup v2 limits for:

- memory
- PID count

This keeps the resource model narrow and useful without introducing a full
resource-management layer.

## Run Flow

```mermaid
flowchart TD
    A["mirage run ... -- command"] --> B[Parse CLI flags]
    B --> C[Load presets]
    C --> D[Merge inline overrides]
    D --> E{Config valid?}
    E -- no --> F[Exit with validation error]
    E -- yes --> G[Prepare namespaces and rootfs]
    G --> H[Apply mounts and cgroup limits]
    H --> I[Configure network behavior]
    I --> J[Launch workload]
    J --> K{Warn mode enabled?}
    K -- yes --> L[Persist observations]
    K -- no --> M[Wait for exit]
    L --> M
```

## Relationship To Other Docs

- [usage.md](usage.md) explains how to invoke the CLI
- [isolation.md](isolation.md) explains what isolation properties users should
  expect today
- [roadmap.md](roadmap.md) tracks the remaining implementation work
