# Network Policy Migration

This document records the completed migration away from Mirage's removed
`--net host|none` surface and into policy-first networking.

## What Changed

Mirage no longer accepts:

- `mirage run --net ...`
- preset YAML with `network: host|none`
- internal config shapes built around coarse network modes

Mirage now accepts:

- built-in presets such as `allow-all`, `offline`, and `openclaw-offline`
- file-backed presets that define `networkPolicy`
- standalone policy documents via `--network-policy-file`

## Replacement Map

| Removed surface | Replacement |
| --- | --- |
| `--net none` | `--preset offline` or `--network-policy-file ./offline.yaml` |
| `--net host` | `--preset allow-all` or `--network-policy-file ./allow-all.yaml` |
| preset `network: none` | preset `networkPolicy` with deny-only ingress/egress |
| preset `network: host` | preset `networkPolicy` with allow-all ingress/egress |

## Current Backend Coverage

The public surface is now policy-first, but the runtime backend still enforces
only a narrow subset of the full rule model:

- **allow-all policy**: host namespace passthrough
- **isolated deny-only policy**: dedicated network namespace, with loopback
  controlled by policy
- **richer allow rules or deferred selectors**: explicit unsupported error

That means Mirage now fails closed for unsupported rule shapes instead of
accepting a coarse mode that bypasses the policy model.

## Canonical Examples

Local-only command:

```bash
./bin/mirage run --rootfs / --preset offline -- /bin/echo hello
```

Host-network command:

```bash
./bin/mirage run --rootfs /srv/rootfs --preset allow-all -- app
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
presets:
  - name: team-offline
    networkPolicy:
      version: 1
      loopback:
        default: allow
      ingress:
        default: deny
        rules: []
      egress:
        default: deny
        rules: []
    description: Team preset for local-only work
```

## Remaining Follow-Up Work

The rule model in [network-rule-model.md](network-rule-model.md) is broader than
today's backend coverage. Future work still needs to implement:

- richer ingress and egress allow-rule enforcement
- domain-backed policy materialization
- stronger packet-filter and diagnostics support

Until then, unsupported policy shapes should continue to fail explicitly.
