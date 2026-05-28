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
- reduce the public network surface to the coarse `host` / `none` transition
- defer rule-based policy, diagnostics, and preset redesign into follow-up
  design work

## Phase 4

- add cgroup v2 controls
- add OpenClaw-friendly presets
- improve failure messages
- add integration tests on Linux
- convert skipped PID and memory limit probes into enforced regression tests

## Phase 5

- support reusable local preset files
- keep preset support explicitly transitional until the new rule model lands
- revisit future network policy and diagnostics only after a new core model is defined
