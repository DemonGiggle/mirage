# OpenClaw

This guide is the shortest working path for running OpenClaw inside Mirage.
It focuses on host prerequisites and a repeatable copy-paste flow.

## Host Prerequisites

Mirage's OpenClaw-oriented rootfs templates copy host tools into the generated
rootfs. For OpenClaw, the important host commands are:

- `node`
- `npm`
- `npx`

On Debian or Ubuntu, a quick host setup is:

```bash
sudo apt update
sudo apt install -y nodejs npm
node --version
npm --version
npx --version
```

If your distro ships a Node.js release that is too old for your OpenClaw
workflow, install a newer Node.js toolchain first, then regenerate the rootfs.

## Generate The Rootfs

`openclaw-developer` is the default general-purpose template. It includes
Node.js-oriented tooling plus common development utilities.

```bash
./bin/mirage rootfs init \
  --template openclaw-developer \
  --output /srv/mirage/openclaw-rootfs
```

Validate the generated rootfs and the required commands:

```bash
./bin/mirage doctor --preset-file ./examples/presets/openclaw-offline.yaml
```

## Install OpenClaw

Use the allow-all preset for installation and onboarding steps that need
network access:

```bash
./bin/mirage run \
  --preset-file ./examples/presets/openclaw-allow-all.yaml \
  -- npm install -g openclaw
```

```bash
./bin/mirage run \
  --preset-file ./examples/presets/openclaw-allow-all.yaml \
  -- openclaw onboard
```

## Run OpenClaw

For local-only work:

```bash
./bin/mirage run \
  --preset-file ./examples/presets/openclaw-offline.yaml \
  -- openclaw
```

To run the local gateway:

```bash
./bin/mirage run \
  --preset-file ./examples/presets/openclaw-allow-all.yaml \
  -- openclaw gateway --port 18789
```

## Notes

- If you upgrade host Node.js, rerun `mirage rootfs init` so the rootfs picks
  up the new binaries and libraries.
- `openclaw-chat-only`, `openclaw-work`, `openclaw-admin`, `openclaw-root`,
  `openclaw`, and `openclaw-systemd` are available when you need a smaller or
  more specialized rootfs. See [rootfs.md](../rootfs.md).
