# Hermes Agent on Mirage

This note explains the built-in `hermes-agent` rootfs template and why it is
shaped the way it is.

## What the template includes

The `hermes-agent` template is meant for running Hermes Agent itself inside a
dedicated Mirage rootfs, not just for hosting random Python code.

It therefore includes:

- Python runtime entrypoints: `python3`, `pip3`, and Python stdlib/runtime
  trees copied from common host locations
- Node runtime entrypoints: `node`, `npm`, `npx`, plus common Node module
  trees so the shipped TUI and browser tooling do not stop at wrapper scripts
- common shell and repo tools: `bash`, `git`, `curl`, `find`, `sed`, `awk`,
  `jq`, `patch`, archive tools, and related Unix basics
- Hermes-friendly utilities: `rg`, `ffmpeg`, `ps`, `pgrep`, and `pkill`
- TLS, locale, timezone, and terminfo runtime data for HTTPS-capable CLI and
  TUI flows

## Why these pieces are in scope

The upstream Hermes Agent project currently expects both Python and Node in a
normal installation path:

- `pyproject.toml` requires Python `>=3.11`
- the root `package.json` declares Node `>=20`
- the upstream install flow provisions Node, ripgrep, and ffmpeg alongside the
  Python environment
- the upstream Docker image also carries Python, Node, git, curl, ripgrep,
  ffmpeg, and procps-style process tools

That combination is why this template sits between Mirage's generic `python`
template and the heavier `openclaw-developer` profile. Hermes needs more than a
plain Python runtime, but it does not inherently need a full compiler and
systems-admin toolchain just to run.

## What is intentionally not bundled

The template does not try to mirror every tool in Hermes's upstream Docker
image.

In particular, it does not force in:

- compiler stacks such as `gcc`, `g++`, `make`, `rustc`, or `cargo`
- container-host tools such as `docker`
- privilege-oriented recovery tools from `openclaw-root`

If your Hermes workflow needs those, start from `openclaw-developer` or
`openclaw-root` instead.

The template also does not require a host `uv` binary. Hermes can bootstrap its
own managed `uv`, and making host `uv` mandatory would create unnecessary
missing-asset noise on otherwise healthy hosts.

## Suggested install flow

Generate the rootfs:

```bash
./bin/mirage rootfs init \
  --template hermes-agent \
  --output /srv/mirage/hermes-agent-rootfs
```

Do a simple validation pass:

```bash
./bin/mirage doctor \
  --rootfs /srv/mirage/hermes-agent-rootfs \
  --command /usr/bin/python3
```

Then install Hermes Agent inside a Mirage run using a network-enabled policy or
preset that matches your environment. A conservative starting point is to use
the rootfs for guest separation while keeping the actual package installation
steps explicit inside the workload command.

## When to choose another template

- Use `python` when you only need Python workloads and do not care about Hermes
  itself.
- Use `openclaw-developer` when you expect native builds, editors, SQLite, and
  broader development-tool coverage.
- Use `openclaw-root` only when you intentionally need privileged diagnostics
  and recovery-style tooling.
