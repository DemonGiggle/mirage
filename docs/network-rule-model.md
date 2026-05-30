# Network Rule Model

This document defines the draft Mirage network rule model that should become the
lower-level design baseline for future network work.

This document now covers four design slices:

- schema shape and validation boundaries
- matching semantics, precedence, and loopback treatment
- domain and DNS semantics
- v1 scope, non-goals, and follow-up work

## Status

- **Document status:** draft design baseline
- **Schema and validation scope:** issue #66
- **Matching and loopback semantics:** issue #67
- **Domain enforcement semantics:** issue #68
- **Canonical design-doc wiring:** issue #69
- **Transition planning and migration inventory:** issue #70

## Motivation

Mirage should stop treating named network modes as the main design unit.

The lower-level contract should instead be a reviewable rule model with:

- directional policy
- explicit defaults
- explicit precedence
- narrow selector syntax
- clearly documented deferred behavior

Modes such as `host`, `none`, or future middle-ground behaviors should be
understood as derived UX surfaces layered on top of this model rather than the
foundation of the design itself.

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
  source:
    cidr: 198.51.100.0/24
  protocol: tcp
  ports: [22]
```

Fields:

- `name`: optional human-readable label
- `action`: required, `allow` or `deny`
- `source`: required ingress selector
- `protocol`: required, `tcp`, `udp`, `icmp`, or `any`
- `ports`: optional list of integer ports; when omitted, the rule matches any
  port for the selected protocol

### Egress rule

```yaml
- name: allow-openai
  action: allow
  destination:
    domain: api.openai.com
  protocol: tcp
  ports: [443]
```

Fields:

- `name`: optional human-readable label
- `action`: required, `allow` or `deny`
- `destination`: required egress selector
- `protocol`: required, `tcp`, `udp`, `icmp`, or `any`
- `ports`: optional list of integer ports; when omitted, the rule matches any
  port for the selected protocol

Using separate rule types lets Mirage reject directional misuse structurally:

- ingress rules use `source`
- egress rules use `destination`
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
- Mirage must validate and normalize domain syntax before any resolver or
  backend consumption rather than treating the field as an opaque string

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
- ingress rule missing `source`
- egress rule missing `destination`
- ingress rule containing `destination`
- egress rule containing `source`
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

## Domain and DNS semantics

`domain` is intentionally treated as a separate design problem rather than just
another selector spelling.

### Why it is separate

Even a narrow-looking field such as:

```yaml
destination:
  domain: api.openai.com
```

quietly carries questions about:

- when DNS resolution happens
- whether answers are refreshed
- how A and AAAA records are handled
- how CNAME chains are interpreted
- what failure means
- whether the real enforcement unit is the domain name or an IP snapshot

Mirage should not pretend those semantics are trivial.

### V1 stance

The v1 design keeps `domain` in the schema and in the design document, but does
**not** make domain-based enforcement part of the v1 implementation contract.

That means:

- `domain` remains valid syntax for egress destination selection
- ingress may not use `domain`
- the document defines the intended future contract shape
- the first enforcement implementation is still allowed to reject or defer
  domain-backed rules explicitly instead of pretending support exists

This is deliberate. Mirage should first make `ip` / `cidr`, rule ordering,
defaults, and loopback behavior solid before it promises DNS-backed policy.

### Minimum future contract if domain enforcement is added later

If Mirage later enables domain-based enforcement, the minimum acceptable
contract should be conservative and explicit:

- resolution happens at sandbox start time
- the result is a fixed IP snapshot rather than a continuously refreshed view
- both A and AAAA answers are included
- CNAME chains are followed to their terminal A/AAAA answers
- no answer means no match, not implicit allow
- domain input must be syntactically validated and normalized before resolver or
  backend use; Mirage must reject malformed names and must not pass raw user
  strings through to shell commands, resolver CLIs, or firewall tooling
- explicit IP/CIDR deny rules must remain able to override domain-derived
  allows by rule order

This section defines the expected direction, but it is still **deferred
design**, not a v1 enforcement promise.

### Interaction with local-network blocking

Mirage should not create a hidden DNS escape hatch for policies that block local
network ranges.

If an operator expresses "block the local network" with explicit deny rules such
as `192.168.0.0/16` or `fe80::/10`, then domain-based policy must preserve that
intent:

- DNS lookup traffic must not get an implicit allow that bypasses normal egress
  matching
- if Mirage depends on a resolver whose address falls inside a denied local
  range, domain-backed startup resolution must fail explicitly rather than
  quietly bypassing the deny
- operators that need domain rules together with local-network blocking must
  either use a resolver reachable through allowed addresses or add a narrow
  explicit exception for the resolver itself

This keeps "block local network" honest. Mirage should not claim to block LAN
access while silently permitting DNS to a local resolver behind the scenes.

## V1 scope

This document is intended to be specific enough for parser, validation, and
future backend design work to proceed without guessing. That does **not** mean
every documented field becomes a v1 runtime guarantee.

### In scope for the v1 design baseline

- the top-level `networkPolicy` structure
- explicit `loopback`, `ingress`, and `egress` sections
- separate ingress and egress rule shapes
- selector validation rules
- protocol and port validation rules
- first-match rule evaluation
- explicit default-action semantics
- loopback as a separate policy zone

### Out of scope for the first implementation contract

- domain-backed enforcement
- wildcard domains
- stateful connection-tracking semantics
- private-range or LAN classification shortcuts
- service names, identities, or process-aware policy
- final CLI UX layered on top of the rule model
- final preset UX layered on top of the rule model

## Non-goals

This document is not trying to:

- finalize firewall backend implementation
- define packet-hook placement in detail
- preserve old `allow-host` semantics
- freeze the final user-facing policy authoring UX
- define every future derived mode or preset

## Follow-up work

This document should become the canonical design reference, but it does not end
the broader network work. The main follow-up areas are:

- implementation/backend design for actual enforcement
- exact DNS/domain enforcement behavior beyond schema reservation
- transition planning from mode-first to rule-first framing (`#70`), documented
  in [network-transition.md](network-transition.md)
- future CLI and preset layering on top of this model
- diagnostics and explainability for blocked traffic

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
        destination:
          cidr: 10.0.0.0/8
        protocol: any
      - name: allow-one-host
        action: allow
        destination:
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
        destination:
          cidr: 192.168.0.0/16
        protocol: any
      - name: deny-one-host
        action: deny
        destination:
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
        source:
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
        destination:
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
        destination:
          cidr: 192.168.0.0/16
        protocol: any
```

### Block local egress, allow everything else

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

This is the intended rule-model shape for "allow public egress, block local
network ranges." If the resolver itself lives inside one of those denied
prefixes, Mirage should not bypass that deny implicitly for DNS.

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
        destination:
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
        source:
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
        source:
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
        destination:
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
        destination:
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
