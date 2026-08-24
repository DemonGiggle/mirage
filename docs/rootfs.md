# Rootfs

This document explains what Mirage expects from `--rootfs`, how `mirage rootfs
init` bootstraps generated root filesystems, and what Mirage expects from the
resulting Debian or Tiny Core tree.

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
mirage rootfs init --output /tmp/mirage/basic-rootfs
```

Generate the pinned Tiny Core rootfs:

```bash
mirage rootfs init \
  --output /tmp/mirage/tinycore-rootfs \
  --distro tinycore
```

Generate a rootfs for a specific target architecture:

```bash
mirage rootfs init --output /tmp/mirage/arm64-rootfs --arch arm64
```

Generate a rootfs for a different Debian release:

```bash
./bin/mirage rootfs init --output /tmp/mirage/bookworm-rootfs --debian-release bookworm
```

Add extra Debian packages during bootstrap:

```bash
./bin/mirage rootfs init \
  --output /tmp/mirage/dev-rootfs \
  --extra-pkg jq,vim,htop
```

Install the guest sudo package for later use with `mirage run --sudo`:

```bash
mirage rootfs init --output /tmp/mirage/sudo-rootfs --sudo
```

If the host CPU architecture differs from the requested rootfs architecture,
configure QEMU user emulation and `binfmt_misc` first. See
[rootfs-cross-arch.md](rootfs-cross-arch.md).

Reuse an existing non-empty output directory only when you intentionally want
Mirage to clear and rebuild the rootfs:

```bash
mirage rootfs init \
  --output /tmp/mirage/basic-rootfs \
  --allow-overwrite
```

Validate a rootfs and a command inside it:

```bash
mirage doctor --rootfs /tmp/mirage/basic-rootfs --command /bin/ls
```

## What `rootfs init` Does

`--distro` selects the rootfs provider. It defaults to `debian` for backward
compatibility and also accepts `tinycore`.

### Debian

`rootfs init` bootstraps a Debian `trixie` `minbase` rootfs with
`mmdebstrap`.

By default Mirage uses `trixie` because `riscv64` rootfs support depends on
Debian package availability in that release.

`--debian-release` lets you override that codename when you want a different
Debian base tree. Mirage passes the value directly to `mmdebstrap` after
trimming surrounding spaces and rejecting whitespace inside the codename.

During `rootfs init`, Mirage prints the underlying bootstrap command, streams
its output, and then prints the apt config write step it performs inside the
rootfs.

`--arch` accepts these values:

- `x86_64`
- `arm64`
- `arm32`
- `riscv64`

Mirage translates those user-facing names into the Debian architecture name
used by `mmdebstrap`. If you omit `--arch`, Mirage detects the host
architecture and uses that by default.

`--extra-pkg` accepts a comma-separated list of Debian package names. Mirage
trims surrounding spaces, rejects invalid or empty names, and appends the
extra packages after the default bootstrap package set.

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

`rootfs init --sudo` appends `sudo` to this package set. The flag is opt-in so
the original minimal bootstrap contents remain unchanged. Passing
`--extra-pkg sudo` is equivalent at the package-installation level.

After the bootstrap, Mirage writes `/etc/apt/apt.conf.d/99sandbox-minimal`
inside the guest with:

```conf
APT::Install-Recommends "false";
APT::Install-Suggests "false";
APT::Sandbox::User "root";
```

### Tiny Core

`rootfs init --distro tinycore` downloads Tiny Core 16.1's archived
`rootfs64.gz` initramfs and extracts it as the dedicated rootfs. It also
downloads the `squashfs-tools` extension and its required libraries so
`tce-load` can extract extensions in userspace. Mirage pins every archive URL
and SHA-256 digest, so a changed or corrupted download is rejected before
extraction. Tiny Core's download server currently serves these archives over
HTTP, making the pinned SHA-256 checks part of the trust boundary. The host
must provide `unsquashfs` from the `squashfs-tools` package during generation.

The extractor handles the `newc` CPIO format directly. It rejects absolute and
traversing paths, refuses to descend through archive-created symlinks, rejects
device nodes outside `/dev`, and does not materialize `/dev` device nodes
because Mirage replaces `/dev` with its managed runtime layout.

The Tiny Core backend currently supports:

- Tiny Core `16.1`
- `x86_64`
- runtime `.tcz` installation through `tce-load` copy-to-filesystem mode

Tiny Core's normal copy mode still mounts each SquashFS extension before
copying it. That mount is unavailable in Mirage's rootless user namespace, so
Mirage patches `tce-load` to call the bootstrapped `unsquashfs` instead. It
also creates Tiny Core's extension state directories, enables
`copy2fs.flg`, and seeds `/etc/resolv.conf` from the host for repository
lookups.

Run extension installation with `mirage run --sudo` so `tce-load` can copy
files into root-owned system paths:

```bash
mirage run \
  --sudo \
  --rootfs /tmp/mirage/tinycore-rootfs \
  --network-policy-file ./examples/network-policies/allow-all.yaml \
  -- tce-load -wi vim
```

Only the installation needs that capability; installed commands can run as
the default non-root `mirage` user. `rootfs init --sudo` is accepted for
workflow compatibility, but Tiny Core already ships its sudo binary.
`--extra-pkg` remains Debian-only; use `tce-load` for Tiny Core packages.
Additional Tiny Core architectures require separate archive, dependency, and
checksum definitions.

Common behavior across generated rootfs trees:

- Mirage validates the selected distribution and its options before clearing
  an existing output directory.
- Mirage creates or extracts a complete userspace instead of copying a host
  shell into production rootfs trees.
- Debian generation writes the minimal guest apt policy described above;
  Tiny Core generation does not create Debian or apt state.

At runtime, dedicated rootfs runs also receive a managed device layout under
`/dev`, including `/dev/shm` and `/dev/pts`.

## Host Privilege Behavior

Mirage selects the bootstrap strategy from the host effective user ID:

- On a root host, Mirage preserves the original behavior and asks
  `mmdebstrap` to populate Debian output directories directly. Tiny Core is
  downloaded and extracted directly.
- On a rootless host, Mirage uses `mmdebstrap --mode=unshare --format=tar`,
  validates the archive while extracting it, and omits device nodes that Mirage
  manages at runtime. The Tiny Core backend performs equivalent safe handling
  for its CPIO archive. Mirage then enters a temporary mapped user namespace
  and shifts the normalized tree to the caller's subordinate root ID.

The resulting ownership marker enables a keep-ID runtime map: the invoking host
user appears as guest `mirage` (`1000:1000`), while guest root and other guest
IDs use subordinate IDs. This keeps root-owned system files protected and lets
the default workload use caller-owned read-write bind mounts with ordinary Unix
owner permissions. Rootless extraction still does not preserve package-specific
ownership or extended attributes such as file capabilities; it is intended for
the minimal CLI pipeline. Rootless generation requires unprivileged user
namespaces, subordinate UID/GID ranges of at least 65,535 IDs, and working
`newuidmap` and `newgidmap` helpers. Mirage applies the multi-range mapping
through those helpers, so it does not require modern `unshare --map-users` or
`--map-groups` options.

Rootfs trees generated by older Mirage versions do not contain the ownership
marker and continue using the legacy runtime mapping. Rebuild them with
`mirage rootfs init --allow-overwrite` to opt into keep-ID behavior. During an
overwrite Mirage temporarily reclaims a marked tree inside the same mapped user
namespace before clearing it.

Rootless trees generated with `rootfs init --sudo` use the same keep-ID map.
During ownership finalization Mirage records inode modes, shifts ownership, and
restores those modes, including the setuid bit on `/usr/bin/sudo`. Consequently,
the default `mirage` account can write caller-owned read-write binds while guest
sudo continues to escalate to namespace root.

Running `sudo mirage rootfs init ...` remains supported when an operator wants
the original privileged directory bootstrap and full Debian ownership data.

The `/usr/bin/sudo` generated by `rootfs init --sudo` works with both bootstrap
strategies. A privileged directory bootstrap preserves Debian's root ownership
directly. A rootless archive bootstrap shifts the binary to the subordinate ID
mapped as guest root and restores its setuid mode.

At runtime, `mirage run --sudo` validates that `/usr/bin/sudo` is a regular,
non-symlink, setuid file owned by namespace root. Mirage also rejects symlinked
path components before installing its read-only `/etc/sudoers` and `/etc/hosts`
overrides. Rootfs trees not created by Mirage must satisfy the same checks.

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
