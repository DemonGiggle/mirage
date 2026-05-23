# Roadmap

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

## Phase 2

- create namespace runner
- mount proc, tmpfs, and bind mounts
- support rootfs handoff
- execute target command inside sandbox

## Phase 3

- add network namespace modes
- implement `none` and `host`
- add a minimal `isolated` mode
- add warn-mode event logging

## Phase 4

- add cgroup v2 controls
- add OpenClaw-friendly presets
- improve failure messages
- add integration tests on Linux

## Phase 5

- persist observed network attempts
- derive suggested allow lists
- support reusable local preset files

