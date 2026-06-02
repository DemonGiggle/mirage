# Network Rule Model

This document defines the current Mirage `networkPolicy` schema and the
matching semantics the runtime is expected to preserve.

## Status

This is the current policy contract. The runtime enforces the IP and CIDR-based
parts today and rejects unsupported shapes such as domain-backed selectors.

## Design Goals

The policy model is intentionally:

- directional
- explicit about defaults
- structurally narrow
- reviewable as YAML
- fail-closed for unsupported selector types

## Top-Level Shape

```yaml
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
```

Requirements:

- `networkPolicy` is required
- `version` is required and must be `1`
- `loopback`, `ingress`, and `egress` are required
- each section must declare `default`
- `ingress.rules` and `egress.rules` are required and may be empty lists

## Rule Shapes

Ingress rules:

```yaml
- name: allow-admin-ssh
  action: allow
  source:
    cidr: 198.51.100.0/24
  protocol: tcp
  ports: [22]
```

Egress rules:

```yaml
- name: allow-dns
  action: allow
  destination:
    ip: 8.8.8.8
  protocol: udp
  ports: [53]
```

Field rules:

- `name` is optional but must not be empty when present
- `action` is required and must be `allow` or `deny`
- ingress rules use `source`
- egress rules use `destination`
- `protocol` is required and must be one of `tcp`, `udp`, `icmp`, or `any`
- `ports` is optional

## Selectors

Selectors use exactly one of:

- `ip`
- `cidr`
- `domain`

Examples:

```yaml
source:
  ip: 203.0.113.10
```

```yaml
destination:
  cidr: 192.0.2.0/24
```

```yaml
destination:
  domain: api.example.com
```

Rules:

- `ip` must be a single IPv4 or IPv6 literal
- `cidr` must be a valid IPv4 or IPv6 CIDR prefix
- `domain` is allowed only in egress selectors
- loopback IPs and loopback CIDRs are rejected in ingress and egress selectors
- `domain` is validated structurally but is not enforced by the current runtime

## Ports

Port rules:

- each port must be an integer between `1` and `65535`
- duplicate ports are rejected
- empty `ports` lists are rejected
- port ranges are not supported
- `ports` cannot be used with `protocol: any`
- `ports` are not valid with `protocol: icmp`

## Matching Semantics

Loopback is independent from ingress and egress. It has its own explicit
default and no per-loopback rule list in v1.

Ingress and egress are evaluated independently:

1. rules are checked in order
2. the first matching rule wins
3. if no rule matches, the direction default applies

This means precedence is positional, not action-based.

## Current Backend Coverage

The current runtime supports these shapes:

- allow-all policy -> host-network passthrough
- IP/CIDR deny-only policies -> isolated namespace with ordered packet-filter
  rules
- IP/CIDR policies with egress allow semantics -> isolated namespace with a
  routed host uplink

The current runtime rejects:

- `destination.domain`
- any selector shape it cannot materialize safely

## Validation Summary

Mirage rejects the following:

- missing required top-level fields
- unknown YAML fields
- invalid actions
- invalid protocols
- invalid IPs or CIDRs
- empty rule names
- selector objects that specify more than one of `ip`, `cidr`, or `domain`
- `domain` in ingress
- `ports` with `protocol: any`
- `ports` with `protocol: icmp`

## Canonical Examples

Fully offline:

```yaml
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
```

Allow internet egress while denying common local ranges:

```yaml
networkPolicy:
  version: 1
  loopback:
    default: allow
  ingress:
    default: deny
    rules: []
  egress:
    default: allow
    rules:
      - name: deny-rfc1918-10
        action: deny
        destination:
          cidr: 10.0.0.0/8
        protocol: any
      - name: deny-rfc1918-172
        action: deny
        destination:
          cidr: 172.16.0.0/12
        protocol: any
      - name: deny-rfc1918-192
        action: deny
        destination:
          cidr: 192.168.0.0/16
        protocol: any
      - name: deny-shared-address-space
        action: deny
        destination:
          cidr: 100.64.0.0/10
        protocol: any
      - name: deny-link-local-v4
        action: deny
        destination:
          cidr: 169.254.0.0/16
        protocol: any
      - name: deny-ula-v6
        action: deny
        destination:
          cidr: fc00::/7
        protocol: any
      - name: deny-link-local-v6
        action: deny
        destination:
          cidr: fe80::/10
        protocol: any
```

## Related Docs

- [usage.md](usage.md)
- [isolation.md](isolation.md)
- [routed-networking.md](routed-networking.md)
