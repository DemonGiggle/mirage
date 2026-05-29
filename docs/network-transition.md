# Network Transition Plan

This document tracks the transition from Mirage's current mode-first network
surface to a future rule-first implementation.

It exists to answer a narrower question than
[network-rule-model.md](network-rule-model.md):

- the rule-model document defines the target design
- this document inventories the current codebase and maps the migration path

## Status

- **Document status:** transition plan and inventory
- **Primary issue:** #70
- **Parent issue:** #65

## Goal

Mirage should stop teaching network behavior primarily as named modes while
still acknowledging that the current implementation surface is mode-based.

The transition plan therefore has two jobs:

1. keep the current CLI and runtime honest about what exists today
2. make the follow-up work to reach a rule-first implementation explicit

## What Is Already True

The repository has already moved part of the way:

- core docs now describe `host` / `none` as a transitional surface
- `docs/network-rule-model.md` is the canonical design reference for the future
  rule-first model
- CLI doctor output already describes the current network surface as coarse and
  transitional rather than as the long-term conceptual center

That means `#70` is not about inventing the rule model again. It is about
making the migration boundary explicit.

## Current Transitional Surfaces

These are the parts of Mirage that are still intentionally mode-first today:

- `--net host|none` in the public CLI
- legacy preset YAML that stores `network: host|none`
- `spec.NetworkMode` and `Config.NetworkMode` for the compatibility path
- built-in presets such as `offline` and `openclaw-offline`
- tests that validate current host/none behavior directly

Rule-first config plumbing now also exists:

- preset YAML may define `networkPolicy` instead of `network`
- `spec.Config` can carry a parsed `NetworkPolicy`
- configs and presets reject ambiguous `network` + `networkPolicy`
  combinations
- dry-run summaries and doctor output identify policy-bearing configs

These are not bugs by themselves. They are the surfaces that later rule-first
work must either replace, compile down to, or retire.

## Immediate Rules For Ongoing Work

Until the rule engine exists, contributors should follow these rules:

1. Do not present `host` / `none` as the conceptual center in new docs.
2. Do present them as the current implementation surface when documenting the
   existing CLI.
3. Do not rename the public CLI away from `--net` or preset-based networking
   until there is a concrete replacement path.
4. Do not add new mode-like abstractions or mode-like preset semantics as a
   substitute for the rule model.
5. When adding examples, prefer explicit wording that distinguishes "current
   CLI surface" from "future rule-first design."

## Inventory Of Mode-First Surfaces

### Public docs and examples

These files still describe or demonstrate the current mode-based surface and
must stay aligned with the transition language:

- `README.md`
- `docs/usage.md`
- `docs/isolation.md`
- `docs/architecture.md`
- `docs/applications.md`
- `docs/roadmap.md`

Current state:

- these files now mostly frame `host` / `none` as transitional
- they still contain concrete `--net host|none` examples because that is the
  actual CLI surface today

Follow-up risk:

- future edits could accidentally drift back into teaching modes as the design
  center rather than as the temporary runtime interface

### CLI wording

Relevant files:

- `internal/cli/cli.go`
- `internal/cli/cli_test.go`

Current state:

- `mirage doctor` now reports "current coarse network modes" and "transitional
  preset loading"
- root help and command examples still expose `--net` and preset usage because
  those are real, supported interfaces

Follow-up risk:

- future help text may imply that `host` / `none` are the lasting design model
  rather than the current runtime knobs

### Config structures and validation

Relevant files:

- `internal/spec/spec.go`
- `internal/spec/loader.go`
- `internal/spec/presets/builtins.yaml`
- `internal/spec/*.go` tests

Current state:

- config can carry either `NetworkMode` or a parsed `NetworkPolicy`
- presets still serialize `network: host|none`
- validation requires exactly one of coarse `network` or rule-first
  `networkPolicy`

Follow-up implication:

- runtime compilation and enforcement can now consume a policy object without
  depending on YAML layout
- policy-bearing runs still need a backend implementation before they can
  execute rather than only dry-run

This is one of the main reasons `#70` exists. The design doc alone does not say
how to get from the current config model to the future one.

### Tests that encode the current conceptual center

Relevant files:

- `e2e/e2e_test.go`
- `e2e/probe_tools_test.go`
- `internal/spec/*_test.go`
- any CLI help/doctor tests under `internal/cli`

Current state:

- many tests assert behavior directly in terms of `--net host` and `--net none`
- this is correct for current implementation verification

Follow-up implication:

- later rule-first implementation work must decide which tests remain valid as
  "current-surface compatibility" and which should migrate to policy-based
  fixtures

### Internal naming

Current mode-first names that will eventually deserve re-evaluation include:

- `NetworkMode`
- `ApplyPreset` behavior that fills `NetworkMode`
- preset descriptions that imply networking stance rather than policy shape

Current decision:

- keep these names for now
- do not churn them inside `#70`
- record them so the eventual implementation tickets do not rediscover the same
  debt from scratch

## Separation Of Work

`#70` should stay narrow about what it does now versus later.

### In scope for `#70`

- audit wording
- keep docs honest about the transition
- record the migration inventory
- identify follow-up implementation slices

### Out of scope for `#70`

- parser implementation for `networkPolicy`
- runtime policy compilation
- firewall or packet-filter backend work
- DNS-backed domain enforcement
- replacement of the public CLI surface

## Recommended Follow-Up Issues

Once the transition plan is accepted, rule-first implementation should be split
into concrete follow-up tickets rather than poured back into `#70`.

Recommended slices:

1. Add a first-class policy data structure and parser entrypoint.
2. Define how policy objects coexist with or replace `NetworkMode`.
3. Implement the runtime policy compilation / materialization pipeline.
4. Design and implement the enforcement backend contract.
5. Plan the CLI/config migration from `--net` and preset `network:` fields to
   policy-first surfaces.
6. Rework tests so policy-based behavior is verified without losing coverage for
   the current compatibility surface.

## Exit Criteria For #70

`#70` should be considered complete when:

- the transition plan exists in-repo
- docs and CLI wording have been reviewed for mode-first assumptions
- implementation-facing mode-first surfaces are explicitly inventoried
- the next implementation tickets can start without redefining the migration
  problem from scratch
