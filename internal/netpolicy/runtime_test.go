package netpolicy

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/DemonGiggle/mirage/internal/spec"
)

func TestCompileOfflinePolicyPreservesDefaults(t *testing.T) {
	runtimePolicy := mustCompilePolicy(t, `networkPolicy:
  version: 1
  loopback:
    default: allow
  ingress:
    default: deny
    rules: []
  egress:
    default: deny
    rules: []
`)

	if runtimePolicy.Loopback != ActionAllow {
		t.Fatalf("unexpected loopback action: %q", runtimePolicy.Loopback)
	}
	if runtimePolicy.Ingress.Default != ActionDeny || runtimePolicy.Egress.Default != ActionDeny {
		t.Fatalf("unexpected defaults: %#v", runtimePolicy)
	}
	if got := runtimePolicy.DecideEgress(tcp("127.0.0.1", 5432)); got != ActionAllow {
		t.Fatalf("expected loopback allow, got %q", got)
	}
	if got := runtimePolicy.DecideEgress(tcp("203.0.113.10", 443)); got != ActionDeny {
		t.Fatalf("expected egress deny default, got %q", got)
	}
}

func TestCompilePreservesRuleOrderAndNormalizedSelectors(t *testing.T) {
	runtimePolicy := mustCompilePolicy(t, `networkPolicy:
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
          cidr: 10.1.2.3/8
        protocol: any
      - name: allow-one-host
        action: allow
        destination:
          ip: 10.0.0.5
        protocol: tcp
        ports: [443]
`)

	if len(runtimePolicy.Egress.Rules) != 2 {
		t.Fatalf("expected two egress rules, got %#v", runtimePolicy.Egress.Rules)
	}
	first := runtimePolicy.Egress.Rules[0]
	if first.Order != 0 || first.Name != "deny-private-v4" || first.Selector.Prefix.String() != "10.0.0.0/8" {
		t.Fatalf("unexpected first rule: %#v", first)
	}
	second := runtimePolicy.Egress.Rules[1]
	if second.Order != 1 || second.Action != ActionAllow || len(second.Ports) != 1 || second.Ports[0] != 443 {
		t.Fatalf("unexpected second rule: %#v", second)
	}
}

func TestCompileUnmapsIPv4MappedSelectorsAndTraffic(t *testing.T) {
	runtimePolicy := mustCompilePolicy(t, `networkPolicy:
  version: 1
  loopback:
    default: allow
  ingress:
    default: deny
    rules: []
  egress:
    default: deny
    rules:
      - name: allow-mapped-host
        action: allow
        destination:
          ip: ::ffff:203.0.113.10
        protocol: tcp
        ports: [443]
      - name: allow-mapped-cidr
        action: allow
        destination:
          cidr: ::ffff:198.51.100.0/120
        protocol: tcp
        ports: [8443]
`)

	if got := runtimePolicy.Egress.Rules[0].Selector.IP.String(); got != "203.0.113.10" {
		t.Fatalf("expected unmapped selector IP, got %q", got)
	}
	if got := runtimePolicy.Egress.Rules[1].Selector.Prefix.String(); got != "198.51.100.0/24" {
		t.Fatalf("expected unmapped selector CIDR, got %q", got)
	}
	if got := runtimePolicy.DecideEgress(tcp("::ffff:203.0.113.10", 443)); got != ActionAllow {
		t.Fatalf("expected mapped traffic to match unmapped IP selector, got %q", got)
	}
	if got := runtimePolicy.DecideEgress(tcp("::ffff:198.51.100.50", 8443)); got != ActionAllow {
		t.Fatalf("expected mapped traffic to match unmapped CIDR selector, got %q", got)
	}
}

func TestFirstMatchDenyBeatsLaterAllow(t *testing.T) {
	runtimePolicy := mustCompilePolicy(t, `networkPolicy:
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
`)

	if got := runtimePolicy.DecideEgress(tcp("10.0.0.5", 443)); got != ActionDeny {
		t.Fatalf("expected earlier deny to win, got %q", got)
	}
}

func TestFirstMatchAllowBeatsLaterDeny(t *testing.T) {
	runtimePolicy := mustCompilePolicy(t, `networkPolicy:
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
`)

	if got := runtimePolicy.DecideEgress(tcp("192.168.1.10", 443)); got != ActionAllow {
		t.Fatalf("expected earlier allow to win, got %q", got)
	}
}

func TestDefaultAppliesWhenNoRuleMatches(t *testing.T) {
	runtimePolicy := mustCompilePolicy(t, `networkPolicy:
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
`)

	if got := runtimePolicy.DecideIngress(tcp("203.0.113.7", 443)); got != ActionAllow {
		t.Fatalf("expected ingress allow default, got %q", got)
	}
	if got := runtimePolicy.DecideIngress(tcp("198.51.100.12", 443)); got != ActionDeny {
		t.Fatalf("expected matching ingress deny, got %q", got)
	}
}

func TestLoopbackDenyIsSeparateFromEgressAllow(t *testing.T) {
	runtimePolicy := mustCompilePolicy(t, `networkPolicy:
  version: 1
  loopback:
    default: deny
  ingress:
    default: deny
    rules: []
  egress:
    default: allow
    rules: []
`)

	if got := runtimePolicy.DecideEgress(tcp("::1", 8080)); got != ActionDeny {
		t.Fatalf("expected loopback deny, got %q", got)
	}
	if got := runtimePolicy.DecideEgress(tcp("203.0.113.10", 8080)); got != ActionAllow {
		t.Fatalf("expected non-loopback egress allow default, got %q", got)
	}
}

func TestCompileRejectsDomainBackedRules(t *testing.T) {
	policy, err := spec.LoadNetworkPolicyYAML([]byte(`networkPolicy:
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
`))
	if err != nil {
		t.Fatalf("LoadNetworkPolicyYAML returned error: %v", err)
	}

	_, err = Compile(policy)
	if err == nil || !strings.Contains(err.Error(), "destination.domain is documented but not enforceable") {
		t.Fatalf("expected domain deferment error, got %v", err)
	}
}

func mustCompilePolicy(t *testing.T, yamlText string) Policy {
	t.Helper()
	policy, err := spec.LoadNetworkPolicyYAML([]byte(yamlText))
	if err != nil {
		t.Fatalf("LoadNetworkPolicyYAML returned error: %v", err)
	}
	runtimePolicy, err := Compile(policy)
	if err != nil {
		t.Fatalf("Compile returned error: %v", err)
	}
	return runtimePolicy
}

func tcp(addr string, port uint16) Traffic {
	return Traffic{
		Remote:   netip.MustParseAddr(addr),
		Protocol: ProtocolTCP,
		Port:     port,
	}
}
