# Isolation Behavior

This document explains what `mirage` isolates today and where the current
boundary is weaker than the long-term design. For usage examples, see
[usage.md](usage.md). For implementation details, see
[architecture.md](architecture.md). For a focused review of the current
network-model usability gaps, see
[network-model-review.md](network-model-review.md).

## Isolation Dimensions

There are three sources of behavior to keep separate:

- preset choice: mainly affects network stance
- runner behavior: creates namespaces and applies mounts
- rootfs choice: determines how strong the filesystem and `/proc` view are

Most confusion comes from mixing those together.

## Built-In Preset Matrix

Built-in presets currently change network behavior only. Process, mount, UTS,
and IPC namespaces come from the Linux runner, not from individual presets.

| Preset | Network isolation | Process namespace | Mount namespace | UTS namespace | IPC namespace | Rootfs isolation | `/proc` view |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `offline` | Yes: separate netns with no network | Yes | Yes | Yes | Yes | Depends on `--rootfs` | Fresh sandbox `/proc` only when `--rootfs` is not `/` |
| `github` | Partial: separate netns with observed allow-list for `github.com:443` | Yes | Yes | Yes | Yes | Depends on `--rootfs` | Fresh sandbox `/proc` only when `--rootfs` is not `/` |
| `openai` | Partial: separate netns with observed allow-list for OpenAI endpoints | Yes | Yes | Yes | Yes | Depends on `--rootfs` | Fresh sandbox `/proc` only when `--rootfs` is not `/` |
| `openclaw-offline` | Yes: separate netns with no network | Yes | Yes | Yes | Yes | Depends on `--rootfs` | Fresh sandbox `/proc` only when `--rootfs` is not `/` |
| `openclaw-openai` | Partial: separate netns with observed allow-list for OpenAI and GitHub endpoints | Yes | Yes | Yes | Yes | Depends on `--rootfs` | Fresh sandbox `/proc` only when `--rootfs` is not `/` |

## What Each Column Really Means

### Network isolation

- `offline` and `openclaw-offline` use a separate network namespace with no
  network access
- `github`, `openai`, and `openclaw-openai` use the current observed isolated
  mode with a narrow allow-list
- `host` mode is available as a raw flag, but it is not a built-in preset

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
- network mode selection through presets or inline flags
- delegated cgroup v2 memory and PID limits
- delegated unified cgroup v2 exposure for `--runtime-mode init`
- init-mode-only managed runtime mounts for `/dev`, `/sys`, and `/run`
- host-side log export

## Current Limitations

Today you should assume:

- `--rootfs /` exposes the host root as the sandbox root
- `--rootfs /` does not provide a fresh procfs view
- rootfs handoff still ends with `chroot`, not `pivot_root`
- isolated networking is still observation-driven rather than a full firewall
  model with routable allow-listed egress
- guest init cgroup support is limited to unified cgroup v2 with a delegated
  host systemd scope and a dedicated rootfs

## Practical Guidance

- use `--rootfs /` only for quick local checks and simple host-root-based runs
- use a dedicated rootfs when filesystem separation or proc visibility matters
- treat presets as network policy helpers, not full sandbox profiles
- choose the runtime mode deliberately:
  - `direct` for one-shot commands where Mirage owns the foreground workload
  - `init` for guest-init-style sandboxes that need a dedicated rootfs, a
    broader runtime mount contract, and host-side lifecycle tracking
- for `init` mode, prefer `mirage sandbox start/status/stop/logs` over ad hoc
  backgrounding so the host-visible state and logs stay coherent
- use this document as the source of truth for current behavior, and
  [roadmap.md](roadmap.md) for what is still planned

## Guest-Init-Specific Caveats

Compared with direct-exec sandboxes, guest-init sandboxes currently add:

- a managed `/dev`
- runtime state directories under `/run`, including `/run/systemd/system`
- a delegated cgroup-aware host scope for lifecycle control
- a `container=mirage` environment hint

But they still have important limits:

- they require a dedicated rootfs and do not support `--rootfs /`
- they are incompatible with the current observed isolated-network path
- host-visible logs come from Mirage-managed stdout/stderr and launch files, not
  from a general live guest journal API
- Mirage still does not provide a generic `exec into running sandbox` command
