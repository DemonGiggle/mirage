# Usage

This document describes how to run `mirage`. For implementation details, see
[architecture.md](architecture.md). For current guarantees and caveats, see
[isolation.md](isolation.md). For rootfs choice, built-in templates, and
guest-init rootfs validation, see [rootfs.md](rootfs.md).

## Host Prerequisites

- Linux
- Go 1.24.4 or newer if building from source
- `unshare` on `PATH` for namespace-backed execution
- `systemd-run` with a working user manager session for delegated `--memory`
  and `--pids`

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

Inspect the current built-in preset set:

```bash
./bin/mirage preset list
```

Preview a run without executing it:

```bash
./bin/mirage run --dry-run --rootfs / --preset offline -- /bin/echo hello
```

Preview an init-oriented run where the guest entrypoint must become PID 1:

```bash
./bin/mirage run \
  --rootfs /srv/mirage/systemd-rootfs \
  --preset allow-all \
  --runtime-mode init \
  -- /usr/lib/systemd/systemd
```

Start a tracked guest-systemd sandbox with host-visible logs:

```bash
./bin/mirage sandbox start \
  --name openclaw \
  --rootfs /srv/mirage/systemd-rootfs \
  --service-unit openclaw.service
```

Inspect, stop, or read logs from a tracked sandbox:

```bash
./bin/mirage sandbox status --name openclaw
./bin/mirage sandbox logs --name openclaw --lines 100
./bin/mirage sandbox stop --name openclaw
```

## Command Pattern

The general form is:

```bash
mirage rootfs init --template <name> --output <path>
mirage sandbox <start|status|stop|logs> [flags]
mirage run [sandbox options...] -- command [args...]
```

Common options include:

- `--rootfs`: root filesystem for the sandbox
- `--preset`: built-in or file-backed preset name
- `--network-policy-file`: standalone `networkPolicy` YAML file
- `--runtime-mode`: `direct` (default) or `init`
- `--ro-bind`: read-only `host:guest` bind mount
- `--rw-bind`: read-write `host:guest` bind mount
- `--env`: explicit sandbox environment variable in `KEY=VALUE` form
- `--cwd`: working directory inside the sandbox
- `--stdout-log` and `--stderr-log`: host-visible log export targets
- `--memory` and `--pids`: delegated cgroup v2 limits

`--runtime-mode direct` keeps the current one-command model: the requested
workload becomes sandbox PID 1. `--runtime-mode init` is for guest init systems
such as `systemd`; the requested init binary becomes sandbox PID 1 directly
instead of being wrapped by Mirage. Network behavior is resolved from either a
preset or `--network-policy-file`. The current backend supports two concrete
policy shapes:

- allow-all policy -> host namespace passthrough
- isolated deny-only policy -> dedicated network namespace

Richer allow rules or deferred selectors such as domain-based egress fail
explicitly instead of silently degrading.

Init mode currently defines a narrow guest cgroup contract:

- unified cgroup v2 only
- dedicated rootfs only; `--rootfs /` is not supported for init mode
- Mirage always enters a delegated host `systemd-run --user --scope` leaf with
  `Delegate=yes` before launching guest init
- Mirage unshares a cgroup namespace for init-mode runs
- when using a dedicated rootfs, Mirage bind-mounts the guest-visible cgroup
  tree at `/sys/fs/cgroup`
- init mode reserves `/sys/fs/cgroup` for that guest cgroup mount, so user bind
  mounts cannot target that path

Init mode also manages a broader runtime mount contract for guest init systems:

| Guest path | Mode in init runs |
| --- | --- |
| `/proc` | Fresh procfs mount |
| `/run` | tmpfs, plus `/run/lock` and `/run/systemd` |
| `/tmp` | tmpfs |
| `/dev` | dedicated tmpfs with basic device nodes, `/dev/pts`, `/dev/shm`, and standard fd symlinks |
| `/sys` | guest-private tmpfs skeleton, remounted read-only after setup |
| `/sys/fs/cgroup` | fresh delegated cgroup2 mount |

Because Mirage owns those runtime paths in init mode, user bind mounts cannot
target them or their managed subpaths.

Mirage does not inherit arbitrary host environment variables into the sandboxed
workload. The managed sandbox environment starts from an explicit `PATH`, adds
any `--env KEY=VALUE` entries you provide, and adds `container=mirage` for
init-mode runs unless you override `container` yourself.

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

For the built-in template catalog, the rootfs schema, `rootfs init` behavior,
and guest-init rootfs validation flows, see [rootfs.md](rootfs.md).

## Tracked Sandbox Lifecycle For Guest `systemd`

For guest-systemd flows, Mirage now exposes a small tracked-sandbox model on top
of the existing `run` command:

- `mirage sandbox start` launches an init-mode sandbox in the background
- `mirage sandbox status` reports the host-side user-systemd scope state
- `mirage sandbox logs` reads the tracked stdout/stderr launch logs
- `mirage sandbox stop` requests a clean stop through the tracked user-systemd
  scope and escalates only if the scope does not stop in time

This model is intentionally narrow:

- it is for `--runtime-mode init` sandboxes only
- it tracks one named sandbox per state entry under the user's local Mirage
  state directory
- it does **not** add a long-lived Mirage daemon or a multi-sandbox scheduler
- it does **not** yet provide live `systemctl exec`-style namespace entry into
  a running guest

Operationally, the lifecycle is:

1. prepare a dedicated rootfs with guest `systemd` and a service unit
2. run `mirage sandbox start --name ... --service-unit ...`
3. use `mirage sandbox status` to confirm the tracked scope is active
4. use `mirage sandbox logs` to inspect init stdout/stderr or launch failures
5. use `mirage sandbox stop` to terminate the sandbox cleanly from the host

The tracked sandbox commands default guest init stdout/stderr into files under
the sandbox's local state directory so logs remain available after stop or boot
failure. `sandbox status` also reports the launch log path, which is where
Mirage's own launch-time failures are recorded when the background start does
not reach a stable running scope.

For guest-systemd-oriented rootfs validation, a minimal operator flow is:

```bash
./bin/mirage doctor \
  --rootfs /srv/mirage/systemd-rootfs \
  --runtime-mode init \
  --command /usr/bin/systemd \
  --service-unit openclaw.service
```

That preflight verifies the init entrypoint, required init runtime paths,
`/etc/machine-id`, and the presence of the requested unit file before launch.

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
  --rootfs /srv/mirage/rootfs \
  --ro-bind /home/gigo/project:/workspace \
  --rw-bind /home/gigo/project-tmp:/workspace/.tmp \
  --cwd /workspace \
  --preset offline \
  -- /bin/sh
```

## Presets

`mirage` supports:

- built-in presets such as `allow-all`, `offline`, and `openclaw-offline`
- local YAML preset files merged with the built-ins

Example preset file:

```yaml
presets:
  - name: team-offline
    networkPolicy:
      version: 1
      loopback:
        default: allow
      ingress:
        default: deny
        rules: []
      egress:
        default: deny
        rules: []
    rootfs:
      template: openclaw-developer
      required_commands:
        - node
      recommended_cwd: /workspace
    description: Team preset for local-only agent work
```

Use it with:

```bash
./bin/mirage preset list --preset-file ./presets.yaml
./bin/mirage run --rootfs /srv/rootfs --preset-file ./presets.yaml --preset team-offline -- app
```

Presets are a convenience surface on top of the same `networkPolicy` object
model. Prefer `--network-policy-file` when you want the policy document itself
to be the reviewable artifact; prefer presets when you also want rootfs hints or
working-directory defaults.

For the exact isolation behavior of each built-in preset, see
[isolation.md](isolation.md).

Rootfs expectation metadata stays optional and separate from the actual rootfs
path. A preset can recommend a rootfs template or required commands, while
`--rootfs` still selects the concrete filesystem tree to validate or execute.

## Network Usage

The current network philosophy is intentionally narrow and policy-first:

- use `--preset offline` when the workload should not reach non-loopback network
- use `--preset allow-all` when the workload truly needs the host network stack
- use `--network-policy-file` for a reviewable standalone policy document
- expect richer allow rules, ingress allow defaults, and domain-backed egress to
  fail explicitly until a stronger enforcement backend exists

Example:

```bash
./bin/mirage run \
  --rootfs /srv/mirage/rootfs \
  --preset allow-all \
  -- app
```

## Log Export

You can tee workload output into host-visible files while preserving console
output:

```bash
./bin/mirage run \
  --rootfs / \
  --preset allow-all \
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
- [architecture.md](architecture.md): internal implementation model
- [development.md](development.md): build, tests, and contributor workflow
