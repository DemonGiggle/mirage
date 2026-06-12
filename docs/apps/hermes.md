# Hermes

This is the shortest verified path for installing Hermes inside Mirage.

## Host Prerequisites

On the host, make sure Mirage itself can be built and that you have
`mmdebstrap` available:

```bash
sudo apt update
sudo apt install -y \
    debian-archive-keyring \
    mmdebstrap
```

## Generate Mirage Package

Create a Mirage package:

```bash
mkdir -p /tmp/mirage
mirage package --output /tmp/mirage
```

The following examples assume you change the working directory to `/tmp/mirage`.

## Generate the Rootfs

Then bootstrap a fresh rootfs:

```bash
sudo mirage rootfs init \
  --output /tmp/mirage-hermes-rootfs \
  --allow-overwrite
```

If rootfs generation fails, the keyring might be too old to verify the Debian
rootfs. Download and install it manually.

```bash
wget http://deb.debian.org/debian/pool/main/d/debian-archive-keyring/debian-archive-keyring_2023.3+deb12u2_all.deb
sudo dpkg -i debian-archive-keyring_2023.3+deb12u2_all.deb
```

## Install Hermes

Use the official installer inside the Mirage rootfs:

```bash
sudo mirage run \
  --rootfs /tmp/mirage-hermes-rootfs \
  --network-policy-file ./share/mirage/network-policies/block-local-egress.yaml \
  -- /bin/bash -lc 'curl -fsSL https://raw.githubusercontent.com/NousResearch/hermes-agent/main/scripts/install.sh | bash'
```

This follows the official Hermes installation command:
<https://hermes-agent.nousresearch.com/docs/getting-started/installation>

## Run Hermes

Start Hermes:

```bash
sudo mirage run \
  --rootfs /tmp/mirage-hermes-rootfs \
  --network-policy-file ./share/mirage/network-policies/block-local-egress.yaml \
  -- /bin/bash -lc '$HOME/.local/bin/hermes gateway'
```
