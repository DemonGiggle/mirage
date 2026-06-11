# Cross-Architecture Rootfs Init

This document explains how to run `mirage rootfs init --arch <arch>` on a host
with a different CPU architecture.

For example, on an `x86_64` host, `mirage rootfs init --arch arm64 ...` can
fail unless the host is configured to run foreign-architecture binaries through
`binfmt_misc` and QEMU user emulation.

Mirage currently bootstraps Debian `trixie` rootfs trees. That release choice
is required for `riscv64` because the needed Debian packages are available
there.

## When You Need This

Use this setup when the host architecture and the requested rootfs
architecture differ, for example:

```bash
sudo ./bin/mirage rootfs init --output /tmp/mirage/arm64-rootfs --arch arm64
```

If the host architecture already matches the requested target architecture, you
do not need the extra QEMU and `binfmt_misc` setup in this guide.

## Host Setup

Install and enable the required host support before running cross-architecture
`rootfs init`.

### 1. Install required packages

```bash
sudo apt update
sudo apt install -y qemu-user-static binfmt-support
```

### 2. Load the `binfmt_misc` kernel module

```bash
sudo modprobe binfmt_misc
```

### 3. Mount `binfmt_misc` if it is not already mounted

```bash
if ! mountpoint -q /proc/sys/fs/binfmt_misc; then
    sudo mount -t binfmt_misc binfmt_misc /proc/sys/fs/binfmt_misc
fi
```

### 4. Check current `binfmt_misc` entries

```bash
ls /proc/sys/fs/binfmt_misc/
```

### 5. Import the correct QEMU rule for the requested `--arch`

Mirage currently supports these rootfs architecture values:

- `x86_64` -> `qemu-x86_64`
- `arm64` -> `qemu-aarch64`
- `arm32` -> `qemu-arm`
- `riscv64` -> `qemu-riscv64`

Use the matching `update-binfmts --import ...` entry for the rootfs
architecture you requested.

Example for `arm64`:

```bash
sudo update-binfmts --import qemu-aarch64 || true
```

Example for `riscv64`:

```bash
sudo update-binfmts --import qemu-riscv64 || true
```

### 6. Enable the matching QEMU rule

Enable the same entry you imported in the previous step.

Example for `arm64`:

```bash
sudo update-binfmts --enable qemu-aarch64
```

Example for `riscv64`:

```bash
sudo update-binfmts --enable qemu-riscv64
```

### 7. Verify that the matching QEMU rule is registered

Check the same `binfmt_misc` entry you enabled.

Example for `arm64`:

```bash
cat /proc/sys/fs/binfmt_misc/qemu-aarch64
```

Example for `riscv64`:

```bash
cat /proc/sys/fs/binfmt_misc/qemu-riscv64
```

### 8. Verify that the matching static QEMU binary exists

Check the matching emulator binary for the target architecture.

Example for `arm64`:

```bash
which qemu-aarch64-static
```

Example for `riscv64`:

```bash
which qemu-riscv64-static
```

## Run `rootfs init`

After the host is configured, retry the rootfs bootstrap:

```bash
sudo ./bin/mirage rootfs init --output /tmp/mirage/arm64-rootfs --arch arm64
```

## Notes

- The example above is for `arm64` on an `x86_64` host because that is the
  most common mismatch case.
- Use the QEMU entry that matches the Mirage `--arch` value:
  `x86_64 -> qemu-x86_64`, `arm64 -> qemu-aarch64`,
  `arm32 -> qemu-arm`, `riscv64 -> qemu-riscv64`.
- `rootfs init` still requires `sudo`, even after `binfmt_misc` is configured.
