# OpenClaw

This is the shortest verified path for installing OpenClaw inside Mirage.

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
  --output /tmp/mirage-openclaw-rootfs \
  --allow-overwrite
```

If rootfs generation fails, the keyring might be too old to verify the Debian
rootfs. Download and install it manually.

```bash
wget http://deb.debian.org/debian/pool/main/d/debian-archive-keyring/debian-archive-keyring_2023.3+deb12u2_all.deb
sudo dpkg -i debian-archive-keyring_2023.3+deb12u2_all.deb
```

## Install OpenClaw

Use the official installer inside the Mirage rootfs. The installer script
handles the package installation and the default post-install flow:

```bash
sudo mirage run \
  --rootfs /tmp/mirage-openclaw-rootfs \
  --network-policy-file ./share/mirage/network-policies/block-local-egress.yaml \
  --run-as-root \
  -- /bin/bash -lc 'curl -fsSL https://openclaw.ai/install.sh | bash -s -- --no-onboard'
```

## Onboard

```bash
sudo mirage run \
  --rootfs /tmp/mirage-openclaw-rootfs \
  --network-policy-file ./share/mirage/network-policies/block-local-egress.yaml \
  -- openclaw onboard
```

## Run OpenClaw

To run the local gateway:

```bash
sudo mirage run \
  --rootfs /tmp/mirage-openclaw-rootfs \
  --network-policy-file ./share/mirage/network-policies/block-local-egress.yaml \
  -- openclaw gateway --port 18789
```

## Approval

(I only use telegram, I don't know the behavior from other clients.)

The first time you add the bot into your telegram, you would receive
a message like this:

```
Your Telegram user id: YYYYYY
Pairing code:
  XXXXX
```

Then you can open another terminal to run the approval below. You
don't need to terminate the running OpenClaw first.

```bash
sudo mirage run \
  --rootfs /tmp/mirage-openclaw-rootfs \
  --network-policy-file ./share/mirage/network-policies/block-local-egress.yaml \
  -- openclaw pairing approve telegram XXXXX
```

The running OpenClaw would hotload the approval.


## Note

If you want to give your Claw more control in its sandbox, you can run it as
root. In that case it can install packages on its own, but use that with
caution.

```bash
sudo mirage run \
  --run-as-root \
  --rootfs /tmp/mirage-openclaw-rootfs \
  --network-policy-file ./share/mirage/network-policies/block-local-egress.yaml \
  -- openclaw gateway --port 18789
```
