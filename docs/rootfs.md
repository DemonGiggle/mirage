# Rootfs and Templates

This document explains what Mirage expects from `--rootfs`, how `mirage rootfs
init` builds generated root filesystems, and which built-in templates exist
today.

## Rootfs Modes

Mirage supports two practical rootfs choices:

- `--rootfs /`
- a dedicated non-`/` rootfs

`--rootfs /` is useful for quick local checks, but it is intentionally weak.
The sandbox sees the host root as `/`, and Mirage does not replace the host
`/proc` mount in that mode.

A dedicated rootfs is the preferred mode when you care about:

- filesystem separation
- a fresh procfs mount
- predictable runtime paths such as `/tmp`, `/run`, and `/dev`

## Create and Validate

List built-in templates:

```bash
./bin/mirage rootfs list-template
```

Generate a rootfs:

```bash
sudo PATH=$PATH ./bin/mirage rootfs init --template basic --output /srv/mirage/basic-rootfs
```

Reuse an existing non-empty output directory only when you intentionally want
Mirage to replace generated files:

```bash
sudo PATH=$PATH ./bin/mirage rootfs init \
  --template basic \
  --output /srv/mirage/basic-rootfs \
  --allow-overwrite
```

Validate a rootfs and a command inside it:

```bash
./bin/mirage doctor --rootfs /srv/mirage/basic-rootfs --command /bin/ls
```

## What `rootfs init` Does

A built-in template can declare:

- directories to create
- binaries to copy from an absolute host path or from host `PATH`
- whether shared-library dependencies should be copied
- runtime trees to copy recursively
- runtime files to copy
- generated files to write directly

Common behavior across generated rootfs trees:

- Mirage creates baseline runtime directories such as `/proc`, `/tmp`, and
  `/run`.
- Mirage copies declared binaries and their dependency closures when requested.
- Script entrypoints keep their shebang interpreters.
- Missing required host assets are reported as warnings while the rest of the
  rootfs is still written.
- Missing optional host assets are skipped.

At runtime, dedicated rootfs runs also receive a managed device layout under
`/dev`, including `/dev/shm` and `/dev/pts`.

`rootfs init` currently runs through `sudo`, and `PATH` should be preserved as
`sudo PATH=$PATH ...` so host `PATH` lookups used by the template still work.

## Template Schema

The built-in schema is versioned as `v1`. A template can include these fields:

```json
{
  "version": "v1",
  "name": "basic",
  "description": "Small runnable base rootfs with shell and core inspection tools.",
  "directories": [],
  "binaries": [],
  "runtime_trees": [],
  "runtime_files": [],
  "generated_files": []
}
```

Binary entries choose exactly one host source:

- `host_path`
- `lookup_name`

Runtime and generated paths must be absolute inside the guest tree.

## Built-In Templates

| Template | Purpose |
| --- | --- |
| `basic` | Small runnable base rootfs with shell and inspection tools |
| `debian` | Minimal Debian-style template with `apt`, `dpkg`, package state, and repository metadata copied from the host |
| `node` | Node.js-oriented template with `node`, `npm`, `npx`, CA material, and `/workspace` |
| `python` | Python-oriented template with `python3`, `pip3`, CA material, and `/workspace` |
| `openclaw` | Compatibility-oriented OpenClaw template with Node.js, Git, Bash, and `/workspace` |
| `openclaw-chat-only` | Smallest OpenClaw-focused level with Node.js, TLS material, locales, tzdata, and OpenSSL |
| `openclaw-work` | `openclaw-chat-only` plus common shell, archive, patching, JSON, and search tooling |
| `openclaw-developer` | `openclaw-work` plus VCS, editors, interpreters, databases, and common build toolchains |
| `openclaw-admin` | `openclaw-developer` plus networking, process, capability, and sync utilities |
| `openclaw-root` | `openclaw-admin` plus package-management, tracing, debugging, namespace, and filesystem tooling |
| `openclaw-systemd` | OpenClaw-oriented template with guest systemd tooling and systemd-ready directories |

Notes:

- `basic` is the smallest general template and the best first rootfs for CLI
  validation.
- `debian` is host-backed. It is meant for apt and dpkg workflows on Debian-like
  hosts, not for bootstrapping a Debian filesystem from nothing.
- `node`, `python`, and the OpenClaw templates depend on host binaries already
  existing on `PATH`.
- `openclaw-developer` is the default preset-oriented template in
  `examples/presets/`.

## Preset Interaction

Preset files can declare:

- `rootfs.path`
- `rootfs.template`
- `rootfs.required_commands`

If the preset rootfs path is absent or empty and `rootfs.template` is present,
Mirage can generate that rootfs automatically before `mirage run`.

`mirage doctor --preset-file ...` also validates any
`rootfs.required_commands` entries declared in the preset.

## Application Flows

For a short OpenClaw-specific setup sequence, see
[apps/openclaw.md](apps/openclaw.md).
