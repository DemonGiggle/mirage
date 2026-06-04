# OpenClaw

This guide is the shortest working path for running OpenClaw inside Mirage.
It focuses on host prerequisites and a repeatable copy-paste flow.

## Host Prerequisites

Mirage now bootstraps a plain Debian rootfs. For OpenClaw, install Node.js
inside that rootfs after initialization.

On the host, a quick setup is:

```bash
sudo apt update
sudo apt install -y mmdebstrap
```

## Generate The Rootfs

```bash
sudo PATH=$PATH ./bin/mirage rootfs init --output /srv/mirage/openclaw-rootfs
```

Install Node.js and npm inside the guest rootfs:

```bash
sudo ./bin/mirage run \
  --rootfs /srv/mirage/openclaw-rootfs \
  --run-as-root \
  --network-policy-file ./examples/network-policies/allow-all.yaml \
  -- /bin/bash -lc 'apt-get update && apt-get install -y nodejs npm'
```

Validate the generated rootfs and the required commands:

```bash
./bin/mirage doctor --preset-file ./examples/presets/openclaw-offline.yaml
```

## Install OpenClaw

Use the allow-all preset for installation and onboarding steps that need
network access:

```bash
sudo ./bin/mirage run \
  --preset-file ./examples/presets/openclaw-allow-all.yaml \
  -- npm install -g openclaw
```

```bash
sudo ./bin/mirage run \
  --preset-file ./examples/presets/openclaw-allow-all.yaml \
  -- openclaw onboard
```

## Run OpenClaw

For local-only work:

```bash
sudo ./bin/mirage run \
  --preset-file ./examples/presets/openclaw-offline.yaml \
  -- openclaw
```

To run the local gateway:

```bash
sudo ./bin/mirage run \
  --preset-file ./examples/presets/openclaw-allow-all.yaml \
  -- openclaw gateway --port 18789
```

## Notes

- If you need newer Node.js builds than Debian provides, install them inside
  the guest rootfs before running OpenClaw.
- The example presets still help with network policy defaults and rootfs
  command validation, but they no longer build the rootfs for you.
