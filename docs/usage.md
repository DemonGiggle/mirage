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

## Command Pattern

The general form is:

```bash
mirage rootfs init --template <name> --output <path>
mirage run [sandbox options...] -- command [args...]
```

Common options include:

- `--rootfs`: root filesystem for the sandbox
- `--preset`: built-in or file-backed preset name
- `--net`: raw network mode override
- `--ro-bind`: read-only `host:guest` bind mount
- `--rw-bind`: read-write `host:guest` bind mount
- `--cwd`: working directory inside the sandbox
- `--stdout-log` and `--stderr-log`: host-visible log export targets
- `--memory` and `--pids`: delegated cgroup v2 limits

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
- runtime files copied from the host into the rootfs

Built-in V1 templates currently include:

- `basic`
- `node`
- `python`
- `openclaw`

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
  "runtime_files": [
    {"host_path": "/etc/hosts", "target_path": "/etc/hosts"},
    {"host_path": "/etc/resolv.conf", "target_path": "/etc/resolv.conf"}
  ]
}
```

This schema is the shared input model for upcoming rootfs generation and
rootfs-aware diagnostics. It remains distinct from network presets, which still
only describe runtime policy defaults.

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
