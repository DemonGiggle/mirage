# Network Model Review

This document captures the main usability and design gaps in Mirage's current
network model. It is not a new architecture proposal by itself. Its purpose is
to turn the current pain points into a concrete review baseline that future
network work can refine against.

For the current user-visible contract, see [isolation.md](isolation.md). For
backend structure, see [architecture.md](architecture.md). For planned follow-up
work, see [roadmap.md](roadmap.md).

## Why This Review Exists

Mirage already has:

- raw network modes: `host`, `none`, and `isolated`
- built-in presets such as `offline`, `github`, and `openai`
- warn-mode recording through `--warn net`
- allow-list inputs such as `--allow-host`, `--allow-cidr`, and `--allow-port`

That is enough surface area for daily use, but it is still a transitional
model. The main problem is not that any one feature is missing. The problem is
that several partially mature mechanisms overlap in ways that are hard to
predict from the CLI alone.

## Main Review Findings

### 1. The mental model is still too tangled

Users currently have to understand several concepts at once:

- preset selection
- raw network mode
- warn-mode observation
- runtime mode
- rootfs choice
- sandbox lifecycle path

Those concepts are related, but they are not the same thing. Today they are too
easy to mix together. A preset looks like a complete sandbox policy, but the
docs also say presets mainly express network stance. `isolated` looks like a
finished network-isolation mode, but the docs also describe it as an
observation-driven interim path. `init` mode looks like a first-class runtime
shape, but it cannot use the same observed-network flow.

The result is that Mirage does not yet present one clean network story. It
presents several related mechanisms that still need operator interpretation.

### 2. The `isolated` contract is not stable enough

`isolated` is currently the largest source of expectation drift.

Across the repo, it is described as:

- a separate network namespace
- observed connect-attempt enforcement
- an observed allow-list
- a temporary path that still needs refinement toward routable allow-listed
  egress

Those statements are individually defensible, but together they are not yet a
crisp contract. A user reading the CLI or preset names can easily infer "this
mode gives me a working narrow egress boundary", while the project itself still
documents it as an incomplete transition state.

That gap matters because users make operational decisions from names first and
architecture notes second.

### 3. The allow-list abstraction is still too low-level

Mirage's current policy inputs are close to implementation details:

- `allow-host`
- `allow-cidr`
- `allow-port`

That can be useful for power users, but it is not yet a friendly policy model.

#### Hostname intent becomes IP snapshots

`allow-host` reads like a hostname-based rule, but the backend resolves those
entries on the host side before execution. That means the effective policy is
really based on a set of resolved `IP:port` targets captured at one point in
time.

Practical consequences:

- CDN-backed services may drift across runs
- dual-stack behavior can vary by environment
- diagnostics often show IPs rather than the original service name
- a rule that looks service-oriented in the CLI becomes address-oriented during
  enforcement

That makes the interface more intuitive than the underlying behavior, which is
not a good long-term place to stay.

#### `allow-port` is broader than it appears

`allow-port` is convenient, but it is also very wide. In practice, `443` means
"allow any destination on port 443", not "allow this one HTTPS service". That
may be acceptable as a convenience escape hatch, but it should be treated as
such rather than read as a least-privilege rule shape.

### 4. Mirage currently feels closer to observed policy auditing than to a predictable egress model

The current observed-network path wraps the workload, records attempted
connections, and then decides whether policy accepted those attempts.

That is useful, but it changes the operator experience:

- the first visible failure may come from the workload itself
- Mirage's policy explanation can arrive after the connection attempt
- the user may need to infer whether the problem was DNS, routing, app-level
  timeout, or policy

In other words, Mirage can currently explain a blocked attempt, but it does not
yet feel like a complete, upfront, service-level network contract.

### 5. `run` and `sandbox start` do not tell the same network story

This is one of the most concrete usability hazards in the current design.

`mirage run` leaves room for a preset to supply the network stance cleanly.
`mirage sandbox start` currently defaults `--net` to `host`, which changes the
override behavior and makes the lifecycle-oriented path feel different from the
one-shot path.

That makes it harder to move from:

- a short direct run with a preset
- to a longer-lived managed sandbox with what appears to be the same preset

If two primary entrypoints apply the same preset differently, users end up
carrying the wrong assumptions into higher-stakes workflows.

### 6. The `init` path does not yet have a first-class network refinement story

Mirage now has a meaningful guest-init lifecycle, but observed networking is
currently incompatible with `runtime-mode init`.

That leaves the product shape uneven:

- direct runs can participate in the current preset-refinement workflow
- long-lived init sandboxes cannot use the same observed path

This is understandable from an implementation perspective, but it still leaves
an operator-level gap. The workflow that most benefits from stable lifecycle and
repeatable policy is also the workflow that currently loses access to the most
expressive network path.

### 7. Some built-in presets are easier to over-trust than they should be

Preset names can imply a narrower contract than the actual rule shape.

For example, an operator reading `openclaw-openai` may reasonably infer "this
allows OpenAI-related traffic". In practice, that preset currently relies on a
combination that includes broad HTTPS egress plus a loopback gateway port.

That may be the right tradeoff for usability, but it should be documented as a
convenience-oriented preset rather than something that sounds narrowly
service-scoped.

### 8. The warn-mode refinement loop is still too manual

Mirage can record warn-mode observations, but it does not yet provide a
first-class workflow to help users digest them.

What is missing today is not only raw telemetry, but also operator-facing
follow-up tools such as:

- listing recent warn records
- grouping recurring destinations
- suggesting candidate allow rules
- explaining which existing preset a new observation is closest to

The roadmap already points in this direction. The gap is worth stating clearly
because it affects whether the current network model feels teachable or merely
inspectable.

## Recommended Refinement Questions

Future work should answer these questions explicitly instead of letting the
answers emerge indirectly from implementation details:

1. What should `isolated` mean as a user-facing contract?
2. If the observation backend is unavailable, should Mirage fail closed, fail
   open with a warning, or expose a distinct mode name?
3. What is the primary policy abstraction: hostname, CIDR, service set, or
   something higher-level?
4. Should broad rules such as `allow-port 443` remain first-class, or should
   they be framed as convenience-only overrides?
5. Should `run` and `sandbox start` share the same preset and network default
   semantics?
6. What is the intended long-lived network story for `runtime-mode init`?
7. Are built-in presets supposed to optimize for least privilege, fast success,
   or explicitly named tradeoff tiers?
8. How should Mirage turn warn-mode output into actionable preset refinement?

## What This Review Does Not Propose Yet

This review does not lock the project into one specific replacement design. It
does not yet choose:

- a firewall backend
- a DNS-aware policy engine
- a service-class abstraction
- a final preset taxonomy

Those are downstream design choices. The point here is to make sure Mirage
solves the right usability and contract problems instead of only rearranging the
current mechanics.
