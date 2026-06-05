# mirage

`mirage` is a lightweight Linux sandbox launcher for local tools and agent
workloads. It gives you a direct CLI for rootfs selection, bind mounts,
policy-first networking, and delegated cgroup limits without introducing a full
container platform.

## Why Mirage

- Linux-first sandboxing with normal kernel primitives
- explicit rootfs and bind-mount exposure
- reviewable network policy files and presets
- one foreground workload per sandbox
- simple release bundles with bundled presets and example policies

## Quick Start

On Debian or Ubuntu, install the required host tools:

```bash
sudo apt update
sudo apt install -y \
    util-linux \
    uidmap \
    iproute2 \
    iptables \
    systemd \
    ca-certificates \
    curl \
    tar \
    debian-archive-keyring \
    mmdebstrap
```

Mirage currently builds with Go `1.24.4` or newer. If you do not already have
that on `PATH`, install it from the official release site:

```bash
curl -LO https://go.dev/dl/go1.24.4.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.24.4.linux-amd64.tar.gz
export PATH=/usr/local/go/bin:$PATH
go version
```

Build Mirage, verify the host, generate a basic rootfs, and run a first
command:

```bash
git clone https://github.com/DemonGiggle/mirage.git
cd mirage
mkdir -p ./bin /srv/mirage
go build -o ./bin/mirage ./cmd/mirage
./bin/mirage doctor
sudo PATH=$PATH ./bin/mirage rootfs init --output /srv/mirage/basic-rootfs
./bin/mirage doctor --rootfs /srv/mirage/basic-rootfs --command /bin/ls
sudo ./bin/mirage run --rootfs /srv/mirage/basic-rootfs --network-policy-file ./examples/network-policies/offline.yaml -- /bin/ls /
```

If you failed to generate a rootfs, the keyring might be too old to verify the debian rootfs.
You need to download and install it manually.

```
wget http://deb.debian.org/debian/pool/main/d/debian-archive-keyring/debian-archive-keyring_2023.3+deb12u2_all.deb
sudo dpkg -i debian-archive-keyring_2023.3+deb12u2_all.deb
```

`rootfs init` currently needs `sudo` plus the caller `PATH` preserved so Mirage
can resolve host binaries for the generated rootfs. `run` currently executes
through `sudo` as well.

## Limits

- `mirage run` launches one direct foreground workload; it is not an init or
  orchestration system.
- Dedicated rootfs runs prepare runtime mounts and then hand off with `chroot`,
  not `pivot_root`.
- `--rootfs /` is a convenience mode and does not provide a fresh filesystem or
  `/proc` view.
- Domain-backed network selectors are still intentionally unsupported.

## Docs

- [docs/usage.md](docs/usage.md): command reference and operator workflows
- [docs/rootfs.md](docs/rootfs.md): rootfs behavior and generation rules
- [docs/isolation.md](docs/isolation.md): current guarantees and caveats
- [docs/apps/openclaw.md](docs/apps/openclaw.md): short OpenClaw setup flow
- [docs/apps/hermes.md](docs/apps/hermes.md): short Hermes Agent setup flow
- [docs/cgroups.md](docs/cgroups.md): delegated memory and PID limits
- [docs/architecture.md](docs/architecture.md): runtime structure and run flow
- [docs/network-rule-model.md](docs/network-rule-model.md): network policy schema and semantics
- [docs/routed-networking.md](docs/routed-networking.md): routed uplink backend details
- [docs/development.md](docs/development.md): contributor workflow
