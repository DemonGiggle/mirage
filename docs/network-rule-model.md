# Network Rule Model

This document defines the draft Mirage network rule model that should become the
lower-level design baseline for future network work.

This first pass focuses on schema shape and validation boundaries. Matching
semantics, precedence, and loopback behavior beyond the schema surface are
follow-up design work.

## Status

- **Document status:** draft design baseline
- **Schema and validation scope:** issue #66
- **Matching and loopback semantics:** issue #67
- **Domain enforcement semantics:** issue #68

## Design goals

The model should make Mirage network policy reviewable before implementation by:

- separating `ingress` and `egress`
- making defaults explicit
- keeping selector syntax narrow and structurally validated
- keeping protocol and port syntax narrow enough for future parser and backend
  work to begin without guessing

## Top-level shape

The canonical YAML shape is:

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

### `networkPolicy.version`

- required
- integer
- v1 must be exactly `1`
- future versions must use a new number rather than quietly changing v1 meaning

### `loopback`

The v1 schema keeps loopback intentionally narrow:

```yaml
loopback:
  default: allow | deny
```

- required
- has an explicit `default`
- does **not** define per-loopback rules in v1
- exists as its own section so loopback remains explicit rather than accidental

### `ingress`

```yaml
ingress:
  default: allow | deny
  rules:
    - ...
```

- required
- `default` is required
- `rules` is required and may be an empty list
- each rule must use the ingress rule shape

### `egress`

```yaml
egress:
  default: allow | deny
  rules:
    - ...
```

- required
- `default` is required
- `rules` is required and may be an empty list
- each rule must use the egress rule shape

## Rule structures

Mirage should use separate rule types rather than one over-permissive shared
type.

### Ingress rule

```yaml
- name: allow-admin-ssh
  action: allow
  from:
    cidr: 198.51.100.0/24
  protocol: tcp
  ports: [22]
```

Fields:

- `name`: optional human-readable label
- `action`: required, `allow` or `deny`
- `from`: required ingress selector
- `protocol`: required, `tcp`, `udp`, `icmp`, or `any`
- `ports`: optional list of integer ports

### Egress rule

```yaml
- name: allow-openai
  action: allow
  to:
    domain: api.openai.com
  protocol: tcp
  ports: [443]
```

Fields:

- `name`: optional human-readable label
- `action`: required, `allow` or `deny`
- `to`: required egress selector
- `protocol`: required, `tcp`, `udp`, `icmp`, or `any`
- `ports`: optional list of integer ports

Using separate rule types lets Mirage reject directional misuse structurally:

- ingress rules use `from`
- egress rules use `to`
- ingress rules cannot carry `domain`

## Selector structure

Selectors use one narrow object shape:

```yaml
ip: 203.0.113.10
```

Valid selector keys:

- `ip`
- `cidr`
- `domain`

Exactly one of those keys must be present.

### `ip`

- single IPv4 or IPv6 literal
- no port information
- convenience syntax for a single host
- validation should normalize it to the canonical IP string form

### `cidr`

- IPv4 or IPv6 CIDR prefix
- must parse as a valid network prefix
- host bits must be normalized to the network address form

### `domain`

- domain name only
- allowed only in egress selectors
- no wildcard syntax in v1
- no scheme, path, or port syntax

## Protocol and port structure

### `protocol`

Valid values:

- `tcp`
- `udp`
- `icmp`
- `any`

### `ports`

- optional
- list of integer ports
- each port must be between `1` and `65535`
- duplicate ports must be rejected
- port ranges are out of scope for v1

## Validation rules

The parser and validator should reject the following explicitly:

### Top-level validation

- missing `networkPolicy`
- missing `version`
- any `version` other than `1`
- missing `loopback`, `ingress`, or `egress`
- missing `default` in any policy section
- any `default` other than `allow` or `deny`
- non-list `rules`

### Rule-shape validation

- missing `action`
- any `action` other than `allow` or `deny`
- ingress rule missing `from`
- egress rule missing `to`
- ingress rule containing `to`
- egress rule containing `from`
- empty `name`
- duplicate rule names within the same direction when `name` is provided

### Selector validation

- empty selector
- selector with more than one of `ip`, `cidr`, or `domain`
- invalid IP literal
- invalid CIDR
- `domain` used in ingress
- `domain` combined with `ip` or `cidr`

### Protocol and port validation

- missing `protocol`
- any `protocol` outside `tcp`, `udp`, `icmp`, `any`
- `ports` used with `icmp`
- `ports` used with `protocol: any`
- empty `ports` list
- duplicate port values
- port values outside `1-65535`
- non-integer ports
- port ranges such as `"80-90"`

## Canonical valid examples

### Fully offline policy

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

### Allow selected egress only

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
    rules:
      - name: allow-openai
        action: allow
        to:
          domain: api.openai.com
        protocol: tcp
        ports: [443]
```

### Allow LAN egress, deny everything else

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
    rules:
      - name: allow-private-v4
        action: allow
        to:
          cidr: 192.168.0.0/16
        protocol: any
```

### Domain-based egress with explicit ports

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
    rules:
      - name: allow-github-https
        action: allow
        to:
          domain: github.com
        protocol: tcp
        ports: [443]
```

## Canonical invalid examples

### Invalid: selector mixes `ip` and `cidr`

```yaml
networkPolicy:
  version: 1
  loopback:
    default: allow
  ingress:
    default: deny
    rules:
      - action: allow
        from:
          ip: 203.0.113.10
          cidr: 203.0.113.0/24
        protocol: tcp
        ports: [22]
  egress:
    default: deny
    rules: []
```

Why invalid:

- selector may define exactly one of `ip`, `cidr`, or `domain`

### Invalid: domain used in ingress

```yaml
networkPolicy:
  version: 1
  loopback:
    default: allow
  ingress:
    default: deny
    rules:
      - action: allow
        from:
          domain: admin.example.com
        protocol: tcp
        ports: [22]
  egress:
    default: deny
    rules: []
```

Why invalid:

- `domain` selectors are egress-only in v1

### Invalid: ports used with `protocol: any`

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
    rules:
      - action: allow
        to:
          cidr: 203.0.113.0/24
        protocol: any
        ports: [443]
```

Why invalid:

- `ports` are valid only with `tcp` or `udp`

### Invalid: port range syntax in v1

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
    rules:
      - action: allow
        to:
          ip: 203.0.113.10
        protocol: tcp
        ports: ["8000-8010"]
```

Why invalid:

- v1 keeps port syntax intentionally narrow and integer-only

## Explicit non-goals for this schema pass

This schema pass does **not** settle:

- rule evaluation order
- allow/deny precedence
- default-action semantics when multiple rules could appear relevant
- exact loopback matching semantics
- domain resolution timing or refresh behavior
- wildcard domains
- private-range or LAN classification semantics
- final CLI or preset UX built on top of this model
