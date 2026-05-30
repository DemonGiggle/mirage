# Network Policy Migration

This document records the completed migration away from Mirage's removed
`--net host|none` surface and into policy-first networking.

## What Changed

Mirage no longer accepts:

- `mirage run --net ...`
- preset YAML with `network: host|none`
- internal config shapes built around coarse network modes

Mirage now accepts:

- single preset files passed through `--preset-file`
- standalone policy documents via `--network-policy-file`

## Replacement Map

| Removed surface | Replacement |
| --- | --- |
| `--net none` | `--network-policy-file ./examples/network-policies/offline.yaml` or `--preset-file ./examples/presets/openclaw-offline.yaml` |
| `--net host` | `--network-policy-file ./examples/network-policies/allow-all.yaml` or `--preset-file ./examples/presets/openclaw-allow-all.yaml` |
| preset `network: none` | preset `networkPolicy` with deny-only ingress/egress |
| preset `network: host` | preset `networkPolicy` with allow-all ingress/egress |

## Current Backend Coverage

The public surface is now policy-first, but the runtime backend still enforces
only a conservative subset of the full rule model:

- **allow-all policy**: host namespace passthrough
- **IP/CIDR-based isolated policy**: dedicated network namespace with ordered
  loopback, ingress, and egress allow/deny enforcement
- **deferred selectors** such as `destination.domain`: explicit unsupported
  error

That means Mirage now fails closed for unsupported rule shapes instead of
accepting a coarse mode that bypasses the policy model.

## Canonical Examples

Local-only command:

```bash
./bin/mirage run --rootfs / --network-policy-file ./examples/network-policies/offline.yaml -- /bin/echo hello
```

Host-network command:

```bash
./bin/mirage run --rootfs /srv/rootfs --network-policy-file ./examples/network-policies/allow-all.yaml -- app
```

Standalone policy document:

```bash
./bin/mirage run \
  --rootfs /srv/rootfs \
  --network-policy-file ./network-policy.yaml \
  -- app
```

Preset file:

```yaml
rootfs:
  path: /srv/rootfs
networkPolicyFile: ../network-policies/offline.yaml
description: Team preset for local-only work
```

## Remaining Follow-Up Work

The rule model in [network-rule-model.md](network-rule-model.md) is broader than
today's backend coverage. Future work still needs to implement:

- domain-backed policy materialization
- stronger packet-filter and diagnostics support

Until then, unsupported policy shapes should continue to fail explicitly.
