# Isolation Behavior

This document explains what `mirage` isolates today and where the current
boundary is weaker than the long-term design. For usage examples, see
[usage.md](usage.md). For implementation details, see
[architecture.md](architecture.md). For the future rule-first network design,
see [network-rule-model.md](network-rule-model.md).

## Isolation Dimensions

There are three sources of behavior to keep separate:

- network policy or preset-file choice
- runner behavior: namespaces, mounts, and cgroups
- rootfs choice: how strong the filesystem and `/proc` view are

Most confusion comes from mixing those together.

## Current Network Surface

Mirage now exposes only policy-first network inputs:

| Surface | Network behavior |
| --- | --- |
| `--network-policy-file ./examples/network-policies/allow-all.yaml` or allow-all `networkPolicy` | No network namespace isolation; the workload uses the host network stack |
| `--network-policy-file ./examples/network-policies/offline.yaml` or isolated deny-only `networkPolicy` | Dedicated network namespace with no non-loopback network access |
| richer allow rules or deferred selectors | Explicit unsupported error |

That should still be read as a narrow implementation slice, not as the complete
rule-engine target. Anything more granular than those currently-supported policy
shapes remains intentionally deferred.

## What Mirage Isolates Today

### Process namespace

The runner creates a PID namespace for sandboxed runs. Child processes spawned
by the workload stay inside that namespace.

This does not automatically guarantee a fresh `/proc` view. That depends on the
rootfs layout and proc mount setup.

### Rootfs isolation

- with `--rootfs /`, the sandbox uses the host root as its `/`
- with a non-`/` rootfs, the runner prepares runtime mountpoints and hands off
  with `chroot`

This makes `--rootfs /` a convenience mode, not a strong filesystem boundary.

### `/proc` view

This is the most important current caveat:

- a non-`/` rootfs gets a fresh proc mount prepared under that rootfs
- `--rootfs /` does not remount proc, so tools such as `ps` can still inspect
  the host procfs mount

That is why `mirage run --rootfs / --network-policy-file ./examples/network-policies/offline.yaml -- ps aux`
can still show the host process list.

## Current Guarantees

Today you can rely on:

- namespace-backed process-tree execution on Linux
- explicit bind-mount application
- policy-first network selection through preset files or standalone policy files
- delegated cgroup v2 memory and PID limits
- delegated unified cgroup v2 exposure for tracked guest-systemd sandboxes
- guest-systemd-only managed runtime mounts for `/dev`, `/sys`, and `/run`
- host-side log export

## Current Limitations

Today you should assume:

- `--rootfs /` exposes the host root as the sandbox root
- `--rootfs /` does not provide a fresh procfs view
- rootfs handoff still ends with `chroot`, not `pivot_root`
- guest init cgroup support is limited to unified cgroup v2 with a delegated
  host systemd scope and a dedicated rootfs
- the current network backend only supports allow-all host passthrough and
  isolated deny-only policies; richer policy enforcement is not implemented yet

## Practical Guidance

- use `--rootfs /` only for quick local checks and simple host-root-based runs
- use a dedicated rootfs when filesystem separation or proc visibility matters
- prefer explicit `--network-policy-file` or `--preset-file` in operator flows
  so network behavior stays reviewable
- use `mirage run` for one-shot commands where Mirage owns the foreground
  workload
- use `mirage sandbox start/status/stop/logs` for guest-init-style sandboxes
  that need a dedicated rootfs, a broader runtime mount contract, and host-side
  lifecycle tracking
- use this document as the source of truth for current behavior, and
  [roadmap.md](roadmap.md) for deferred work

## Guest-Init-Specific Caveats

Compared with direct-exec sandboxes, guest-init sandboxes currently add:

- a managed `/dev`
- runtime state directories under `/run`, including `/run/systemd/system`
- a delegated cgroup-aware host scope for lifecycle control
- a `container=mirage` environment hint

But they still have important limits:

- they require a dedicated rootfs and do not support `--rootfs /`
- host-visible logs come from Mirage-managed stdout/stderr and launch files, not
  from a general live guest journal API
- Mirage still does not provide a generic `exec into running sandbox` command
