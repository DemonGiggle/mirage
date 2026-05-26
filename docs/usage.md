# Usage

This document describes how to run `mirage`. For implementation details, see
[architecture.md](architecture.md). For current guarantees and caveats, see
[isolation.md](isolation.md).

## Host Prerequisites

- Linux
- Go 1.24.4 or newer if building from source
- `unshare` on `PATH` for namespace-backed execution
- `strace` on `PATH` for `--net isolated`, `--warn net`, and related tests
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

Validate a rootfs before running inside it:

```bash
./bin/mirage doctor --rootfs /srv/mirage/basic-rootfs --command /bin/ls
```

List presets:

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
  --net host \
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
- `--net`: raw network mode override
- `--runtime-mode`: `direct` (default) or `init`
- `--ro-bind`: read-only `host:guest` bind mount
- `--rw-bind`: read-write `host:guest` bind mount
- `--cwd`: working directory inside the sandbox
- `--stdout-log` and `--stderr-log`: host-visible log export targets
- `--memory` and `--pids`: delegated cgroup v2 limits

`--runtime-mode direct` keeps the current one-command model: the requested
workload becomes sandbox PID 1. `--runtime-mode init` is for guest init systems
such as `systemd`; the requested init binary becomes sandbox PID 1 directly
instead of being wrapped by Mirage. Because the current isolated-network path is
implemented by an observation wrapper, init mode currently requires `--net host`
or `--net none` without `--warn net`.

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

## Rootfs Guidance

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

## Rootfs Templates

`mirage` now defines a rootfs template model that is intentionally separate from
network presets.

A V1 rootfs template describes:

- template `version`, `name`, and `description`
- directories that should exist in the generated rootfs
- binaries copied either from an explicit host absolute path or from host `PATH`
- whether each binary should bring along its shared-library dependency closure
- runtime trees copied recursively from the host into the rootfs
- runtime files copied from the host into the rootfs

Built-in V1 templates currently include:

- `basic`
- `node`
- `python`
- `openclaw`
- `openclaw-chat-only`
- `openclaw-work`
- `openclaw-developer`
- `openclaw-admin`
- `openclaw-root`
- `openclaw-systemd`

Every built-in template currently prepares the same baseline runtime layout:

- runtime directories: `/proc`, `/tmp`, and `/run`
- common runtime files: `/etc/hosts`, `/etc/resolv.conf`, and `/etc/nsswitch.conf`
- declared binaries copied into the rootfs together with their ELF dependency
  closures
- shebang interpreters copied when a declared command is a script wrapper
- generated files written directly by Mirage for small rootfs assets such as an
  empty `/etc/machine-id`

For systemd-oriented rootfs validation, use:

```bash
./bin/mirage doctor \
  --rootfs /srv/mirage/openclaw-systemd-rootfs \
  --runtime-mode init \
  --service-unit openclaw.service
```

That checks the guest init binary, required runtime paths, `/etc/machine-id`,
and whether the requested service unit is present at
`/etc/systemd/system/<name>` or `/usr/lib/systemd/system/<name>`.

### What `rootfs init --template` prepares

| Template | What it prepares | Good starting point for |
| --- | --- | --- |
| `basic` | Shell and inspection basics: `/bin/sh`, `/bin/ls`, `/bin/cat`, `/bin/mkdir`, `/bin/pwd`, `/bin/rm`, `/bin/true`, `/bin/false`, and `/usr/bin/env` | Sanity checks, simple shell commands, and minimal rootfs runs |
| `node` | Everything from `basic`, plus `/workspace`, `/etc/ssl/certs`, `node`, `npm`, `npx`, and common CA bundle files when present on the host | Node.js-oriented tooling and HTTPS-capable Node workloads |
| `python` | Everything from `basic`, plus `/workspace`, `/etc/ssl/certs`, `python3`, `pip3`, and common CA bundle files when present on the host | Python-oriented tooling and HTTPS-capable Python workloads |
| `openclaw` | Compatibility OpenClaw template: everything from `node`, plus `/home`, `bash`, and `git` | Backward-compatible OpenClaw local agent work and current `openclaw-*` presets |
| `openclaw-chat-only` | Everything from `node`, plus locale/tzdata runtime data and `openssl` | Minimal OpenClaw chat-oriented runs that need Node.js, TLS, and locale/timezone data |
| `openclaw-work` | Everything from `openclaw-chat-only`, plus shell, archive, patching, JSON, and search tooling | OpenClaw work sessions with common Unix utilities |
| `openclaw-developer` | Everything from `openclaw-work`, plus VCS, editors, Python, SQLite, and common build-toolchain entrypoints | OpenClaw development-oriented sessions |
| `openclaw-admin` | Everything from `openclaw-developer`, plus networking, process, and capability utilities | OpenClaw troubleshooting and host/network administration tasks |
| `openclaw-root` | Everything from `openclaw-admin`, plus package-management, tracing, debugging, namespace, and filesystem tools | Privileged or recovery-oriented OpenClaw sessions |
| `openclaw-systemd` | Everything from `openclaw`, plus guest `systemd` tooling, systemd unit directories, `/var/lib/systemd`, `/var/log/journal`, `/etc/passwd`, `/etc/group`, and an empty `/etc/machine-id` | OpenClaw guest service runs managed by guest `systemd` |

Notes:

- `basic` is the smallest built-in template and the best first choice when you
  just want a runnable rootfs for commands like `/bin/ls` or `/bin/sh`.
- `node`, `python`, and all `openclaw*` templates intentionally add a writable
  `/workspace` layout because those flows commonly mount or use project trees there.
- the leveled `openclaw-*` templates compose strictly from the previous level
  plus the current level's additions.
- `openclaw` remains the backward-compatible OpenClaw template used by existing
  presets, while the leveled templates let you opt into narrower or broader
  tool surfaces explicitly.
- `openclaw-systemd` seeds the directory structure needed for guest `systemd`,
  but Mirage does not ship an opinionated `openclaw.service` body. The expected
  placement for an operator-provided unit is `/etc/systemd/system/openclaw.service`.
- The OpenClaw presets currently recommend the `openclaw` template and expect
  `node` to be present, so `mirage doctor --preset openclaw-openai --rootfs ...`
  can check that expectation directly.

Schema shape:

```json
{
  "version": "v1",
  "name": "basic",
  "description": "Small runnable base rootfs with shell and core inspection tools.",
  "directories": [
    {"path": "/proc", "mode": 493},
    {"path": "/tmp", "mode": 1023},
    {"path": "/run", "mode": 493}
  ],
  "binaries": [
    {
      "target_path": "/bin/sh",
      "lookup_name": "sh",
      "copy_dependencies": true
    },
    {
      "target_path": "/usr/bin/env",
      "host_path": "/usr/bin/env",
      "copy_dependencies": true
    }
  ],
  "runtime_trees": [
    {"host_path": "/usr/share/zoneinfo", "target_path": "/usr/share/zoneinfo", "optional": true}
  ],
  "runtime_files": [
    {"host_path": "/etc/hosts", "target_path": "/etc/hosts"},
    {"host_path": "/etc/resolv.conf", "target_path": "/etc/resolv.conf"}
  ]
}
```

This schema is the shared input model for upcoming rootfs generation and
rootfs-aware diagnostics. It remains distinct from network presets, which still
only describe runtime policy defaults.

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
./bin/mirage rootfs init --template openclaw-systemd --output /srv/mirage/openclaw-systemd-rootfs
cp ./openclaw.service /srv/mirage/openclaw-systemd-rootfs/etc/systemd/system/openclaw.service

./bin/mirage doctor \
  --rootfs /srv/mirage/openclaw-systemd-rootfs \
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

- built-in presets such as `offline`, `github`, and `openai`
- local preset files merged with the built-ins

Example preset file:

```json
{
  "presets": [
    {
      "name": "team-openai",
      "network": "isolated",
      "allow_hosts": ["api.openai.com:443", "github.com:443"],
      "rootfs": {
        "template": "openclaw",
        "required_commands": ["node"],
        "recommended_cwd": "/workspace"
      },
      "description": "Team preset for OpenAI-backed agent work"
    }
  ]
}
```

Use it with:

```bash
./bin/mirage preset list --preset-file ./presets.json
./bin/mirage run --rootfs /srv/rootfs --preset-file ./presets.json --preset team-openai -- app
```

For the exact isolation behavior of each built-in preset, see
[isolation.md](isolation.md).

Rootfs expectation metadata stays optional and separate from the actual rootfs
path. A preset can recommend a rootfs template or required commands, while
`--rootfs` still selects the concrete filesystem tree to validate or execute.

## Network Usage

The current network philosophy is intentionally narrow:

- use `offline` or `--net none` when the workload should not reach the network
- use `openai` or `github` when only a small allow-list is needed
- use `--warn net` to record attempted access while refining a preset

Example:

```bash
./bin/mirage run \
  --rootfs /srv/mirage/rootfs \
  --preset openai \
  --warn net \
  -- app
```

## Log Export

You can tee workload output into host-visible files while preserving console
output:

```bash
./bin/mirage run \
  --rootfs / \
  --net host \
  --stdout-log /tmp/app.out \
  --stderr-log /tmp/app.err \
  -- /bin/sh -c "printf 'out'; printf 'err' >&2"
```

## Related Docs

- [isolation.md](isolation.md): exact current behavior and caveats
- [architecture.md](architecture.md): internal implementation model
- [development.md](development.md): build, tests, and contributor workflow
