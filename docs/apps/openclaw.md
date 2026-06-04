# OpenClaw

This guide is the shortest verified path for installing OpenClaw inside Mirage.
It records the exact command sequence used to get from a fresh Debian rootfs to
the start of `openclaw onboard`.

## Host Prerequisites

Mirage bootstraps a plain Debian rootfs. For current OpenClaw releases, the
Debian `nodejs` package is not new enough, so you need a newer Node.js build
inside the rootfs before running `npm install -g openclaw`.

On the host, a quick setup is:

```bash
sudo apt update
sudo apt install -y mmdebstrap
```

## Generate The Rootfs

Build Mirage first if you have not already:

```bash
go build -o ./bin/mirage ./cmd/mirage
sudo install -m 755 ./bin/mirage /usr/local/bin/mirage
```

Then bootstrap a fresh rootfs:

```bash
sudo /usr/local/bin/mirage rootfs init \
  --output /tmp/mirage-openclaw-rootfs \
  --allow-overwrite
```

## Install Node.js In The Rootfs

Install the Debian packages Mirage needs for a basic Node/npm toolchain:

```bash
sudo /usr/local/bin/mirage run \
  --rootfs /tmp/mirage-openclaw-rootfs \
  --network-policy-file ./examples/network-policies/allow-all.yaml \
  --run-as-root \
  -- /bin/bash -lc 'apt-get update && apt-get install -y nodejs npm'
```

Replace the Debian Node build with a newer release that satisfies OpenClaw:

```bash
sudo /usr/local/bin/mirage run \
  --rootfs /tmp/mirage-openclaw-rootfs \
  --network-policy-file ./examples/network-policies/allow-all.yaml \
  --run-as-root \
  -- /bin/bash -lc 'set -euo pipefail; mkdir -p /opt/node-v22.19.0; cd /tmp; curl -fsSLO https://nodejs.org/dist/v22.19.0/node-v22.19.0-linux-x64.tar.xz; tar -xJf node-v22.19.0-linux-x64.tar.xz -C /opt/node-v22.19.0 --strip-components=1; /opt/node-v22.19.0/bin/node --version; /opt/node-v22.19.0/bin/npm --version'
```

## Install OpenClaw

Install the package with the newer Node build on `PATH`:

```bash
sudo /usr/local/bin/mirage run \
  --rootfs /tmp/mirage-openclaw-rootfs \
  --network-policy-file ./examples/network-policies/allow-all.yaml \
  --run-as-root \
  -- /bin/bash -lc 'export PATH=/opt/node-v22.19.0/bin:$PATH; npm install -g openclaw'
```

Start onboarding in non-interactive mode so it can stop after the initial
setup without walking through every prompt:

```bash
sudo /usr/local/bin/mirage run \
  --rootfs /tmp/mirage-openclaw-rootfs \
  --network-policy-file ./examples/network-policies/allow-all.yaml \
  --run-as-root \
  -- /bin/bash -lc 'export PATH=/opt/node-v22.19.0/bin:$PATH; openclaw onboard --non-interactive --accept-risk --auth-choice skip --skip-ui --skip-skills --skip-channels --skip-daemon --skip-health --skip-hooks --skip-search --skip-bootstrap'
```

## Run OpenClaw

For local-only work:

```bash
sudo /usr/local/bin/mirage run \
  --rootfs /tmp/mirage-openclaw-rootfs \
  --network-policy-file ./examples/network-policies/offline.yaml \
  --run-as-root \
  -- openclaw
```

To run the local gateway:

```bash
sudo /usr/local/bin/mirage run \
  --rootfs /tmp/mirage-openclaw-rootfs \
  --network-policy-file ./examples/network-policies/allow-all.yaml \
  --run-as-root \
  -- openclaw gateway --port 18789
```

## Notes

- The verified onboarding command writes the initial config to
  `~/.openclaw/openclaw.json` and initializes the workspace and session
  directories.
- If you want the fully guided TUI, drop `--non-interactive` and the `--skip-*`
  flags.
