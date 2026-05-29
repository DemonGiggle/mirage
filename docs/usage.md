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

Preview a run without executing it:

```bash
./bin/mirage run --dry-run --preset-file ./examples/presets/openclaw-offline.yaml -- /bin/echo hello
```

Start a tracked guest-systemd sandbox with host-visible logs:

```bash
./bin/mirage sandbox start \
  --name openclaw \
  --rootfs /srv/mirage/systemd-rootfs \
  --network-policy-file ./examples/network-policies/allow-all.yaml \
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
- `--preset-file`: single preset YAML file that can bundle rootfs, network
  policy, bind mounts, cwd, hostname, memory, and PID limits
- `--network-policy-file`: standalone `networkPolicy` YAML file
- `--ro-bind`: read-only `host:guest` bind mount
- `--rw-bind`: read-write `host:guest` bind mount
- `--env`: explicit sandbox environment variable in `KEY=VALUE` form
- `--cwd`: working directory inside the sandbox
- `--stdout-log` and `--stderr-log`: host-visible log export targets
- `--memory` and `--pids`: delegated cgroup v2 limits

`mirage run` always uses the direct one-command model: the requested workload
becomes sandbox PID 1. Network behavior is resolved from either a preset file
or `--network-policy-file`. The current backend supports two
concrete policy shapes:

- allow-all policy -> host namespace passthrough
- isolated deny-only policy -> dedicated network namespace

Richer allow rules or deferred selectors such as domain-based egress fail
explicitly instead of silently degrading.

Mirage does not inherit arbitrary host environment variables into the sandboxed
workload. The managed sandbox environment starts from an explicit `PATH` and
adds any `--env KEY=VALUE` entries you provide.

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
and guest-systemd rootfs validation flows, see [rootfs.md](rootfs.md).

## Tracked Sandbox Lifecycle For Guest `systemd`

For guest-systemd flows, Mirage exposes a small tracked-sandbox model alongside
the direct `run` command:

- `mirage sandbox start` launches a guest-systemd sandbox in the background
- `mirage sandbox status` reports the host-side user-systemd scope state
- `mirage sandbox logs` reads the tracked stdout/stderr launch logs
- `mirage sandbox stop` requests a clean stop through the tracked user-systemd
  scope and escalates only if the scope does not stop in time

This model is intentionally narrow:

- it is for guest-init-style sandboxes only
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
  --preset-file ./examples/presets/openclaw-offline.yaml \
  -- /bin/sh
```

## Presets

`mirage` accepts a single preset document through `--preset-file`. The preset
file can bundle the same configuration you would otherwise pass with several
flags, including rootfs path, bind mounts, working directory, hostname, memory,
PID limits, and network policy.

Example preset file:

```yaml
rootfs:
  path: /srv/mirage/openclaw-rootfs
  template: openclaw-developer
  required_commands:
    - node
networkPolicyFile: ../network-policies/offline.yaml
cwd: /workspace
description: Team preset for local-only agent work
```

Use it with:

```bash
./bin/mirage run --preset-file ./examples/presets/openclaw-offline.yaml -- app
```

Inside a preset file, use exactly one of:

- `networkPolicy`: inline policy details
- `networkPolicyFile`: reference a standalone policy YAML file

When `--preset-file` is used, Mirage rejects overlapping direct flags such as
`--rootfs`, `--network-policy-file`, `--cwd`, bind mounts, hostname, memory,
and PID limits. Move that configuration into the preset file instead.

## Network Usage

The current network philosophy is intentionally narrow and policy-first:

- use `./examples/network-policies/offline.yaml` when the workload should not
  reach non-loopback network
- use `./examples/network-policies/allow-all.yaml` when the workload truly
  needs the host network stack
- use `--network-policy-file` for a reviewable standalone policy document
- expect richer allow rules, ingress allow defaults, and domain-backed egress to
  fail explicitly until a stronger enforcement backend exists

Example:

```bash
./bin/mirage run \
  --rootfs /srv/mirage/rootfs \
  --network-policy-file ./examples/network-policies/allow-all.yaml \
  -- app
```

## Log Export

You can tee workload output into host-visible files while preserving console
output:

```bash
./bin/mirage run \
  --rootfs / \
  --network-policy-file ./examples/network-policies/allow-all.yaml \
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
