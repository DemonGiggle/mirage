# Roadmap

This document tracks planned work only. For the current implementation shape,
see [architecture.md](architecture.md). For the current user-visible behavior,
see [isolation.md](isolation.md).

## Phase 0

- define scope
- create CLI skeleton
- write architecture notes
- pin V1 non-goals

## Phase 1

- implement config model and validation
- implement dry-run output
- add preset resolution
- add `doctor` checks for required host tools
- add host-side stdout/stderr log export
- add end-to-end CLI coverage for preview and execution plumbing

## Phase 2

- create namespace runner
- mount proc, tmpfs, and bind mounts
- support rootfs handoff
- execute target command inside sandbox
- convert skipped bind-mount probes into enforced regression tests

## Phase 3

- add network namespace modes
- implement `none` and `host`
- add a minimal `isolated` mode
- add warn-mode event logging
- convert skipped network allow-rule and warn-mode probes into enforced regression tests
- refine isolated-mode policy from observed connect enforcement toward routable allow-listed egress

## Phase 4

- add cgroup v2 controls
- add OpenClaw-friendly presets
- improve failure messages
- add integration tests on Linux
- convert skipped PID and memory limit probes into enforced regression tests

## Phase 5

- persist observed network attempts
- derive suggested allow lists
- support reusable local preset files
