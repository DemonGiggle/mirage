# Network Rule Model

This document defines the draft Mirage network rule model that should become the
lower-level design baseline for future network work.

This document now covers two design slices:

- schema shape and validation boundaries
- matching semantics, precedence, and loopback treatment

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
- `ports`: optional list of integer ports; when omitted, the rule matches any
  port for the selected protocol

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
- `ports`: optional list of integer ports; when omitted, the rule matches any
  port for the selected protocol

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
- loopback IP literals used in ingress or egress selectors
- loopback CIDRs used in ingress or egress selectors

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

## Matching semantics

This section defines how Mirage should interpret the schema once a policy is
valid.

### Directional evaluation

- ingress rules evaluate traffic entering the sandbox
- egress rules evaluate traffic leaving the sandbox
- loopback traffic is classified into the dedicated `loopback` zone before
  ingress or egress rule matching is considered

### Rule evaluation model

Mirage v1 should use **first-match wins** within each directional rule list.

That means:

1. evaluate rules in the order written
2. the first rule whose selector, protocol, and ports all match decides the
   result
3. later rules are ignored once a match is found

### Allow and deny precedence

Mirage v1 should not define a hidden global rule such as "deny always overrides
allow" or "allow always overrides deny."

Instead, **rule order is the precedence mechanism**:

- an earlier `deny` beats a later `allow`
- an earlier `allow` beats a later `deny`

This keeps precedence explicit in the written policy rather than split between
rule order and an extra action hierarchy.

### Default-action semantics

If no rule matches inside a directional section:

- `ingress.default` decides ingress traffic
- `egress.default` decides egress traffic

Defaults are therefore the fallback only after ordered rule matching fails to
find a match.

### Match shape

A rule matches only when all applicable fields match:

- the selector matches the remote endpoint
- the protocol matches
- the port matches when the rule declares `ports`
- when `ports` is omitted, the rule matches any port for the selected protocol

If any one of those checks fails, Mirage continues to the next rule.

## Loopback treatment

Loopback is a separate policy zone in v1.

### Why it is separate

Mirage should treat loopback explicitly because it behaves differently from
general ingress and egress traffic, and because hiding it inside normal selector
matching would blur the design.

### Loopback classification

The `loopback` section governs traffic whose effective peer address is loopback:

- IPv4 loopback: `127.0.0.0/8`
- IPv6 loopback: `::1`

This classification happens before ingress or egress rule evaluation:

- for ingress, Mirage classifies loopback using the source peer address
- for egress, Mirage classifies loopback using the destination peer address

### Relationship to ingress and egress

- ingress and egress rules do **not** match loopback addresses in v1
- loopback traffic is controlled only by `loopback.default`
- v1 has no per-loopback rule list, so there is no loopback rule ordering yet
- loopback IPs and loopback CIDRs are invalid in ingress and egress rules rather
  than silently ignored

This means loopback is explicit, separate, and intentionally narrow:

- `loopback.default: allow` permits loopback traffic regardless of
  `ingress.default` or `egress.default`
- `loopback.default: deny` denies loopback traffic regardless of
  `ingress.default` or `egress.default`

## Canonical semantic examples

### Example: earlier deny beats later allow

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
      - name: deny-private-v4
        action: deny
        to:
          cidr: 10.0.0.0/8
        protocol: any
      - name: allow-one-host
        action: allow
        to:
          ip: 10.0.0.5
        protocol: tcp
        ports: [443]
```

Result:

- egress to `10.0.0.5:443/tcp` is **denied**
- the first rule already matched, so the later allow rule is never reached

### Example: earlier allow beats later deny

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
      - name: deny-one-host
        action: deny
        to:
          ip: 192.168.1.10
        protocol: tcp
        ports: [443]
```

Result:

- egress to `192.168.1.10:443/tcp` is **allowed**
- the broad allow matched first, so the later deny never applies

If Mirage users want the host-specific deny to win, they must place it earlier.

### Example: default applies when no rule matches

```yaml
networkPolicy:
  version: 1
  loopback:
    default: allow
  ingress:
    default: allow
    rules:
      - name: deny-admin-range
        action: deny
        from:
          cidr: 198.51.100.0/24
        protocol: any
  egress:
    default: deny
    rules: []
```

Result:

- ingress from `203.0.113.7` is **allowed**
- no ingress rule matched, so `ingress.default: allow` decides the outcome

### Example: loopback allow is separate from egress deny

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

Result:

- a connection to `127.0.0.1:5432` is **allowed**
- loopback traffic does not fall through to `egress.default`

### Example: loopback deny is separate from egress allow

```yaml
networkPolicy:
  version: 1
  loopback:
    default: deny
  ingress:
    default: deny
    rules: []
  egress:
    default: allow
    rules: []
```

Result:

- a connection to `::1:8080` is **denied**
- `egress.default: allow` does not override `loopback.default: deny`

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

## Open questions that remain outside this pass

This document still does **not** settle:

- domain resolution timing or refresh behavior
- wildcard domains
- private-range or LAN classification semantics
- stateful enforcement details in the eventual backend
- exact mapping from conceptual policy matches to packet-flow enforcement hooks
- final CLI or preset UX built on top of this model
