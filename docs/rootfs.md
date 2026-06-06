# Rootfs

This document explains what Mirage expects from `--rootfs`, how `mirage rootfs
init` bootstraps generated root filesystems, and what Mirage expects from the
resulting Debian tree.

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

Generate a rootfs:

```bash
sudo ./bin/mirage rootfs init --output /tmp/mirage/basic-rootfs
```

Reuse an existing non-empty output directory only when you intentionally want
Mirage to clear and rebuild the rootfs:

```bash
sudo ./bin/mirage rootfs init \
  --output /tmp/mirage/basic-rootfs \
  --allow-overwrite
```

Validate a rootfs and a command inside it:

```bash
./bin/mirage doctor --rootfs /tmp/mirage/basic-rootfs --command /bin/ls
```

## What `rootfs init` Does

`rootfs init` bootstraps a Debian `bookworm` `minbase` rootfs with
`mmdebstrap`.

During `rootfs init`, Mirage prints the underlying bootstrap command, streams
its output, and then prints the apt config write step it performs inside the
rootfs.

The bootstrap step currently uses this package set:

- `apt`
- `ca-certificates`
- `bash`
- `coreutils`
- `util-linux`
- `procps`
- `psmisc`
- `iproute2`
- `curl`
- `tar`
- `gzip`
- `xz-utils`
- `git`

After the bootstrap, Mirage writes `/etc/apt/apt.conf.d/99sandbox-minimal`
inside the guest with:

```conf
APT::Install-Recommends "false";
APT::Install-Suggests "false";
APT::Sandbox::User "root";
```

Common behavior across generated rootfs trees:

- Mirage creates a Debian base userspace first.
- Mirage writes a minimal guest apt policy file that disables recommends and
  suggests.
- Mirage preserves a standard Debian userspace instead of copying host tools
  into the rootfs.

At runtime, dedicated rootfs runs also receive a managed device layout under
`/dev`, including `/dev/shm` and `/dev/pts`.

`rootfs init` currently runs through `sudo`.

When you pass `--allow-overwrite`, Mirage clears the existing output directory
before running `mmdebstrap` again.

## Preset Interaction

Preset files can declare:

- `rootfs.path`
- `rootfs.required_commands`

`mirage doctor --preset-file ...` also validates any
`rootfs.required_commands` entries declared in the preset.

## Application Flows

For a short Application-specific setup sequence, see
[apps/openclaw.md](apps/openclaw.md).
[apps/hermes.md](apps/hermes.md).
