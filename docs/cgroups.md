# Cgroup Limits

This document explains how `mirage` applies resource limits with cgroup v2 and
why it uses `systemd-run` and direct cgroup file writes together.

For the broader runtime model, see [architecture.md](architecture.md). For
operator-facing command usage, see [usage.md](usage.md). For current guarantees
and caveats, see [isolation.md](isolation.md).

## Scope

Today Mirage supports a narrow cgroup v2 feature set:

- memory ceiling through `memory.max`
- swap disablement through `memory.swap.max` when available
- PID ceiling through `pids.max`

The project intentionally does not try to expose a full resource-management
surface.

## Why `systemd-run` And `cgroup2` Both Exist

Mirage uses both because they solve different problems.

- `systemd-run` creates a user-owned scope under the systemd user manager.
- `Delegate=yes` gives Mirage permission to manage a subtree below that scope.
- direct cgroup v2 file writes apply the actual kernel-enforced limits.

In other words:

- `systemd-run` gets Mirage into a delegated place in the cgroup tree
- cgroup v2 files set the memory and PID ceilings

If Mirage skipped `systemd-run`, an unprivileged process often would not have a
writable delegated subtree where it could create and manage child cgroups.

If Mirage skipped direct cgroup v2 writes, the current implementation would
create a scope but would not actually set `memory.max` or `pids.max`, because
it does not ask systemd to apply those properties on its behalf.

## Why Mirage Creates A Leaf Cgroup

Mirage does not run the workload directly in the delegated systemd scope.
Instead it creates a child cgroup inside that scope and moves its helper
process into that leaf before launching the sandbox workload.

That extra hop matters for cgroup v2 semantics:

- controllers must be enabled on the parent through `cgroup.subtree_control`
- workloads should live in child cgroups when the parent is acting as a domain
  that delegates controllers downward

This is why the implementation needs both:

1. a delegated systemd scope that Mirage is allowed to manage
2. a Mirage-owned leaf cgroup where it can place the helper and enforce limits

## Run Flow

When `--memory` or `--pids` is present, the high-level execution path is:

1. `mirage run` resolves the final config.
2. Mirage launches itself through `systemd-run --user --scope -p Delegate=yes`.
3. The delegated process re-enters Mirage as `__cgroup-exec`.
4. `__cgroup-exec` discovers its current cgroup v2 path.
5. It creates a child leaf cgroup under that delegated scope.
6. It moves itself into the leaf by writing its PID to `cgroup.procs`.
7. It enables needed controllers on the parent with `cgroup.subtree_control`.
8. It writes `memory.max`, `memory.swap.max`, and/or `pids.max` on the leaf.
9. It launches the normal namespace backend.
10. The workload inherits the leaf cgroup membership and limits.
11. On exit, Mirage cleans up the leaf and uses `cgroup.kill` if needed.

## Process Sketch

```text
host shell
  -> mirage run
       -> systemd-run --user --scope -p Delegate=yes -- mirage __cgroup-exec ...
            -> mirage __cgroup-exec
                 -> create leaf cgroup
                 -> move helper into leaf
                 -> write cgroup limits
                 -> unshare ...
                      -> mirage __backend-exec
                           -> exec workload
```

## Cgroup Tree Sketch

The exact systemd path depends on the machine, but the shape is roughly:

```text
/sys/fs/cgroup/
  user.slice/
    user-1000.slice/
      user@1000.service/
        app.slice/
          mirage-sandbox-demo.scope/          <- created by systemd-run
            cgroup.subtree_control            <- enabled by Mirage
            mirage-12345/                     <- created by Mirage
              cgroup.procs
              memory.max
              memory.swap.max
              pids.max
              ... workload process tree ...
```

The scope path is managed by systemd. The `mirage-<pid>` leaf is managed by
Mirage itself.

## What The Helper Actually Does

The cgroup helper has four main responsibilities:

1. Create the leaf cgroup.
2. Move itself into the leaf.
3. Apply requested limits.
4. Launch the namespace backend so the workload inherits those limits.

Cleanup matters too:

- on normal exit, Mirage tries to move the helper back to the parent and remove
  the leaf
- if removal fails because descendant processes are still present, Mirage tries
  `cgroup.kill` before removing the directory again

## Why The User Manager Requirement Exists

The docs require:

- `systemd-run`
- a working systemd user manager session

That requirement exists because Mirage depends on `systemd-run --user --scope`
to obtain delegated cgroup ownership without requiring a privileged daemon or a
root-owned setup step.

If the user manager is unavailable, cgroup-backed limits should be treated as
unsupported in that environment.

## Operator Impact

For users of `mirage run`, the important practical points are:

- `--memory` and `--pids` only work when `systemd-run --user` delegation works
- the resource model is currently limited to memory and PID count
- workloads inherit the limit automatically because the helper enters the leaf
  before starting the sandbox backend
- the feature is orthogonal to rootfs and network-policy choices

## Related Code

The current implementation is centered in:

- [`internal/runner/runner.go`](../internal/runner/runner.go)
- `buildDelegatedScopeCommand`
- `RunCgroupHelper`
- `enterCgroupLeaf`

These functions correspond directly to the sequence described above.
