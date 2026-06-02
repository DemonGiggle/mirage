# Isolation Behavior

This document describes the current isolation boundary Mirage actually provides.
It is the operator-facing source of truth for guarantees and caveats.

## Read The Axes Separately

Most confusion comes from mixing three independent choices:

- rootfs choice
- network policy choice
- optional cgroup limits

A strong network policy does not strengthen a weak rootfs choice, and a strong
rootfs choice does not imply a network namespace.

## Process Tree

Each `mirage run` invocation creates one sandbox process tree.

- the requested workload becomes sandbox PID 1
- child processes stay inside the same PID namespace
- Mirage does not run a guest init system

## Rootfs Boundary

Mirage has two practical rootfs modes.

### `--rootfs /`

- the sandbox uses the host root as `/`
- Mirage does not prepare a new root mount layout
- the host `/proc` mount stays visible

This mode is convenient, but it is not a strong filesystem isolation boundary.

### Dedicated non-`/` rootfs

- Mirage prepares runtime mountpoints under that rootfs
- Mirage mounts a fresh procfs
- Mirage provides managed `/dev`, `/dev/shm`, and `/dev/pts`
- handoff finishes with `chroot`

This is the preferred mode when filesystem separation or proc visibility
matters.

## Network Boundary

Mirage exposes only policy-first network inputs.

| Policy shape | Runtime behavior |
| --- | --- |
| allow-all policy | workload uses the host network stack |
| deny-only IP/CIDR rules | dedicated network namespace with ordered loopback, ingress, and egress rules |
| egress allow semantics with IP/CIDR selectors | dedicated network namespace plus a routed host uplink |
| domain selectors | explicit unsupported error |

The policy file decides which backend Mirage chooses. See
[network-rule-model.md](network-rule-model.md) for the schema and
[routed-networking.md](routed-networking.md) for the routed uplink details.

## Credentials and Environment

By default:

- the workload runs as the non-root `mirage` user (`1000:1000`)
- Mirage synthesizes matching passwd and group entries at runtime
- host environment variables are not inherited automatically

Use `--run-as-root` only when the guest workload actually needs root.

## Current Guarantees

Today Mirage reliably provides:

- Linux namespace-backed process-tree execution
- explicit bind mounts
- policy-file or preset-driven network selection
- managed runtime mounts for dedicated rootfs runs
- delegated cgroup v2 memory and PID limits when configured
- host-visible stdout and stderr log export

## Current Limitations

Today you should assume:

- `--rootfs /` exposes the host filesystem as the guest root
- `--rootfs /` does not provide a fresh procfs view
- dedicated rootfs handoff still ends with `chroot`, not `pivot_root`
- allow-all policy intentionally uses host network passthrough
- domain-backed selectors fail closed because the runtime does not enforce them

## Practical Guidance

- Use `--rootfs /` only for quick local checks.
- Use a dedicated rootfs for anything you expect to be repeatable or reviewable.
- Prefer `--network-policy-file` or `--preset-file` over ad hoc experimentation
  when sharing commands with other operators.
- Treat this document as the current behavior reference, not the design target.
