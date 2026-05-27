# Isolation Behavior

This document explains what `mirage` isolates today and where the current
boundary is weaker than the long-term design. For usage examples, see
[usage.md](usage.md). For implementation details, see
[architecture.md](architecture.md).

## Isolation Dimensions

There are three sources of behavior to keep separate:

- network mode or preset choice
- runner behavior: namespaces, mounts, and cgroups
- rootfs choice: how strong the filesystem and `/proc` view are

Most confusion comes from mixing those together.

## Stable Network Contract

Mirage now presents only two stable network modes:

| Mode | Network behavior |
| --- | --- |
| `host` | No network namespace isolation; the workload uses the host network stack |
| `none` | Dedicated network namespace with no network access |

The built-in `offline` and `openclaw-offline` presets both resolve to
`network: none`.

Anything more granular than `host` / `none` is intentionally deferred to future
design work.

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

That is why `mirage run --rootfs / --preset offline -- ps aux` can still show
the host process list.

## Current Guarantees

Today you can rely on:

- namespace-backed process-tree execution on Linux
- explicit bind-mount application
- stable `host` / `none` network mode selection through presets or inline flags
- delegated cgroup v2 memory and PID limits
- delegated unified cgroup v2 exposure for `--runtime-mode init`
- init-mode-only managed runtime mounts for `/dev`, `/sys`, and `/run`
- host-side log export

## Current Limitations

Today you should assume:

- `--rootfs /` exposes the host root as the sandbox root
- `--rootfs /` does not provide a fresh procfs view
- rootfs handoff still ends with `chroot`, not `pivot_root`
- guest init cgroup support is limited to unified cgroup v2 with a delegated
  host systemd scope and a dedicated rootfs
- future firewall, diagnostics, and preset redesign work is not implemented yet

## Practical Guidance

- use `--rootfs /` only for quick local checks and simple host-root-based runs
- use a dedicated rootfs when filesystem separation or proc visibility matters
- treat presets as convenience defaults, not full sandbox profiles
- choose the runtime mode deliberately:
  - `direct` for one-shot commands where Mirage owns the foreground workload
  - `init` for guest-init-style sandboxes that need a dedicated rootfs, a
    broader runtime mount contract, and host-side lifecycle tracking
- for `init` mode, prefer `mirage sandbox start/status/stop/logs` over ad hoc
  backgrounding so the host-visible state and logs stay coherent
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
