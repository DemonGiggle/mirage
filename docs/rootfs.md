# Rootfs and Templates

This document describes how Mirage uses `--rootfs`, what `mirage rootfs init`
prepares, and how the built-in rootfs templates map to current workflows. For
general CLI usage, see [usage.md](usage.md). For operator-visible isolation
tradeoffs, see [isolation.md](isolation.md).

## Choosing a Rootfs

Mirage supports two broad rootfs shapes:

- `--rootfs /`: convenient for quick local checks and host-root-backed runs
- a dedicated non-`/` rootfs: the preferred mode when filesystem separation,
  proc visibility, or guest-init workflows matter

`--rootfs /` is intentionally weaker. It does not provide the same filesystem
or `/proc` behavior as a dedicated generated or custom rootfs.

## Creating and Validating a Rootfs

Generate a runnable rootfs from a built-in template:

```bash
./bin/mirage rootfs init --template basic --output /srv/mirage/basic-rootfs
```

If you intentionally want to reuse an existing non-empty output directory,
opt in explicitly:

```bash
./bin/mirage rootfs init \
  --template basic \
  --output /srv/mirage/basic-rootfs \
  --allow-overwrite
```

Validate a command inside that rootfs before launching a full workload:

```bash
./bin/mirage doctor --rootfs /srv/mirage/basic-rootfs --command /bin/ls
```

For guest-init-oriented rootfs validation, point `doctor` at a dedicated rootfs
that already contains the init binary and the service unit you expect to launch:

```bash
./bin/mirage doctor \
  --rootfs /srv/mirage/systemd-rootfs \
  --runtime-mode init \
  --command /usr/bin/systemd \
  --service-unit openclaw.service
```

That checks the init entrypoint, required runtime paths, `/etc/machine-id`, and
whether the requested unit is present at
`/etc/systemd/system/<name>` or `/usr/lib/systemd/system/<name>`.

## Rootfs Template Model

A V1 rootfs template describes:

- template `version`, `name`, and `description`
- directories that should exist in the generated rootfs
- binaries copied either from an explicit host absolute path or from host `PATH`
- whether each binary should bring along its shared-library dependency closure
- whether a binary is optional and may be skipped if the host copy is missing or
  unusable
- runtime trees copied recursively from the host into the rootfs
- runtime files copied from the host into the rootfs
- generated files written directly by Mirage into the rootfs

Every built-in template currently prepares the same baseline runtime layout:

- runtime directories: `/proc`, `/tmp`, and `/run`
- common runtime files: `/etc/hosts`, `/etc/resolv.conf`, and
  `/etc/nsswitch.conf`
- declared binaries copied with their ELF dependency closures when requested
- shebang interpreters copied when a declared command is a script wrapper

Mirage reports missing **non-optional** host assets as warnings and still writes
the rest of the rootfs. Optional host assets are skipped silently.

By default, `rootfs init` still rejects a non-empty output directory. Use
`--allow-overwrite` only when you explicitly want Mirage to reuse an existing
rootfs path and replace generated files in place.

### Schema shape

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
      "target_path": "/usr/bin/host",
      "lookup_name": "host",
      "copy_dependencies": true,
      "optional": true
    }
  ],
  "runtime_trees": [
    {
      "host_path": "/usr/share/zoneinfo",
      "target_path": "/usr/share/zoneinfo",
      "optional": true
    }
  ],
  "runtime_files": [
    {"host_path": "/etc/hosts", "target_path": "/etc/hosts"},
    {"host_path": "/etc/resolv.conf", "target_path": "/etc/resolv.conf"}
  ],
  "generated_files": [
    {"target_path": "/etc/machine-id", "content": "", "mode": 420}
  ]
}
```

## Built-In Templates

| Template | What it prepares | Good starting point for |
| --- | --- | --- |
| `basic` | Shell and inspection basics: `/bin/sh`, `/bin/ls`, `/bin/cat`, `/bin/mkdir`, `/bin/pwd`, `/bin/rm`, `/bin/true`, `/bin/false`, and `/usr/bin/env` | Sanity checks, simple shell commands, and minimal rootfs runs |
| `node` | Everything from `basic`, plus `/workspace`, `/etc/ssl/certs`, `node`, `npm`, `npx`, and common CA bundle files when present on the host | Node.js-oriented tooling and HTTPS-capable Node workloads |
| `python` | Everything from `basic`, plus `/workspace`, `/etc/ssl/certs`, `python3`, `pip3`, and common CA bundle files when present on the host | Python-oriented tooling and HTTPS-capable Python workloads |
| `openclaw-chat-only` | Everything from `node`, plus locale/tzdata runtime data and `openssl` | Minimal OpenClaw chat-oriented runs that need Node.js, TLS, and locale/timezone data |
| `openclaw-work` | Everything from `openclaw-chat-only`, plus shell, archive, patching, JSON, and search tooling | OpenClaw work sessions with common Unix utilities |
| `openclaw-developer` | Everything from `openclaw-work`, plus VCS, editors, Python, SQLite, and common build-toolchain entrypoints | OpenClaw development-oriented sessions |
| `openclaw-admin` | Everything from `openclaw-developer`, plus networking, process, and capability utilities | OpenClaw troubleshooting and host/network administration tasks |
| `openclaw-root` | Everything from `openclaw-admin`, plus package-management, tracing, debugging, namespace, and filesystem tools | Privileged or recovery-oriented OpenClaw sessions |

Notes:

- `basic` is the smallest built-in template and the best first choice when you
  just want a runnable rootfs for `/bin/ls` or `/bin/sh`.
- `node`, `python`, and all `openclaw*` templates intentionally add a writable
  `/workspace` layout because those flows commonly mount or use project trees there.
- the leveled `openclaw-*` templates compose strictly from the previous level
  plus the current level's additions.
- the built-in OpenClaw preset hints currently recommend `openclaw-developer`
  and expect `node` to be present.

## OpenClaw-Oriented Rootfs Flows

For end-to-end OpenClaw installation and launch examples, see
[applications.md#openclaw](applications.md#openclaw).
