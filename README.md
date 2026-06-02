# mirage

`mirage` is a lightweight Linux sandbox launcher for running local tools inside
an explicit execution envelope without adopting a full container stack.

The project target is pragmatic:

- Linux only
- CLI first
- namespace-based isolation
- explicit rootfs and bind-mount control
- small explicit network surface
- enough guardrails for daily agent and developer workflows

`mirage` is not trying to become another Docker or Kubernetes clone.

## What It Solves

Tools such as OpenClaw often need a middle ground:

- stronger isolation than running directly on the host
- less setup than a VM or full container platform
- network controls that default to deny
- explicit writable paths
- a path from ad hoc runs to explicit, reviewable launch config

## Quick Start

If you are on Debian or Ubuntu, install the host-side tools first:

```bash
sudo apt update
sudo apt install -y util-linux uidmap iproute2 iptables systemd ca-certificates curl tar
```

Install Go from the official release site instead of `apt` so you control the
toolchain version. Mirage currently builds with Go `1.24.4` or newer:

```bash
curl -LO https://go.dev/dl/go1.24.4.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.24.4.linux-amd64.tar.gz
export PATH=/usr/local/go/bin:$PATH
go version
```

Then build Mirage, verify the host, generate a rootfs, and run a first command:

```bash
git clone <your-fork-or-this-repo-url> mirage
cd mirage
mkdir -p ./bin /srv/mirage
go build -o ./bin/mirage ./cmd/mirage
./bin/mirage doctor
./bin/mirage rootfs init --template basic --output /srv/mirage/basic-rootfs
./bin/mirage doctor --rootfs /srv/mirage/basic-rootfs --command /bin/ls
./bin/mirage run --rootfs / --preset-file ./examples/presets/openclaw-offline.yaml -- /bin/echo hello
```

If you already have a suitable Go toolchain on `PATH`, you can skip the Go
install step and start at `go build`.

For the full template catalog, dedicated-rootfs guidance, bind mounts, and
preset-file workflows, use the docs linked below.

## Current Scope

Today the project includes:

- namespace-backed process-tree execution on Linux
- a single direct-workload `mirage run` path
- chroot-based rootfs handoff when using a non-`/` rootfs
- a shared V1 rootfs template schema with curated built-in templates
- read-only and read-write bind mounts
- file-backed preset loading and standalone network policy files
- current backend coverage for allow-all host passthrough and isolated namespace policy enforcement with ordered allow/deny rules
- stdout and stderr export to host-visible log files
- delegated cgroup v2 memory and PID limits

Mirage now exposes a single operator-facing execution shape:

- **direct exec**: `mirage run` launches one foreground workload as sandbox PID 1

Important caveat:

- `--rootfs /` is intentionally convenient for local testing, but it does not
  provide the same filesystem and `/proc` behavior as a dedicated rootfs

For exact behavior and current limitations, see
[docs/isolation.md](docs/isolation.md).

## Release Bundles

`mirage package` creates a standalone bundle for public release. The bundle
contains:

- `bin/mirage`
- `share/mirage/rootfs/templates/*.yaml`
- `share/mirage/network-policies/*.yaml`
- `share/mirage/presets/*.yaml`

That lets downstream users download one package and immediately use the bundled
presets and policies without needing a source checkout.

## Host Requirements

Mirage expects a few host-side tools to already be installed. The exact package
names vary by distribution, but the required host-provided packages/tools are:

- `unshare` for namespace-backed execution
- the `uidmap` package, which provides `newuidmap` and `newgidmap` for Mirage's
  default non-root workload execution
- `ip` for isolated network namespace setup
- `iptables` and `ip6tables` for non-host `networkPolicy` enforcement
- `systemd-run` with a working user manager session if you use delegated `--memory` or `--pids` limits

`./bin/mirage doctor` is the quickest way to check the current environment, but
you should install those host packages/tools yourself before relying on
namespace and network-policy features.

## Documentation Map

- [docs/applications.md](docs/applications.md): application-oriented setup flows
  such as installing and launching OpenClaw inside Mirage
- [docs/rootfs.md](docs/rootfs.md): rootfs choice, template catalog, schema, and
  validation guidance
- [docs/usage.md](docs/usage.md): installation assumptions, command patterns,
  current command surface, preset-file workflows, and common run examples
- [docs/isolation.md](docs/isolation.md): current isolation matrix, guarantees,
  and known caveats
- [docs/cgroups.md](docs/cgroups.md): delegated systemd scopes, cgroup v2
  limits, and the runtime flow for `--memory` and `--pids`
- [docs/network-rule-model.md](docs/network-rule-model.md): canonical draft
  design for the rule-first network policy model
- [docs/routed-networking.md](docs/routed-networking.md): host-to-sandbox veth
  routing, forwarding, NAT, and packet flow for routed network policies
- [docs/network-transition.md](docs/network-transition.md): migration notes,
  replacements for removed `--net` usage, and current backend limits
- [docs/architecture.md](docs/architecture.md): control-plane and backend
  design, namespace setup, and run flow
- [docs/development.md](docs/development.md): build, test, formatting, and repo
  layout for contributors
- [docs/roadmap.md](docs/roadmap.md): staged implementation plan

## Status

The current backend is useful, but still transitional:

- rootfs handoff still ends with `chroot`, not `pivot_root`
- the CLI now exposes only policy-first networking inputs; the isolated backend
  enforces mixed allow/deny ingress and egress rules, while domain-backed
  selectors remain intentionally incomplete
- proc and mount hardening around `--rootfs /` is intentionally documented as a
  current limitation rather than a solved property

The implementation details behind those limitations are documented in
[docs/architecture.md](docs/architecture.md), while the operator-visible impact
is documented in [docs/isolation.md](docs/isolation.md).
