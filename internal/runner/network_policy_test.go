package runner

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/DemonGiggle/mirage/internal/netpolicy"
	"github.com/DemonGiggle/mirage/internal/spec"
)

func TestPlanNetworkPolicyBackendOfflinePolicy(t *testing.T) {
	plan, err := planNetworkPolicyBackend(spec.Config{
		NetworkPolicy: &spec.NetworkPolicy{
			Version:  1,
			Loopback: spec.LoopbackPolicy{Default: spec.PolicyAllow},
			Ingress:  spec.IngressPolicy{Default: spec.PolicyDeny, Rules: []spec.IngressRule{}},
			Egress:   spec.EgressPolicy{Default: spec.PolicyDeny, Rules: []spec.EgressRule{}},
		},
	})
	if err != nil {
		t.Fatalf("planNetworkPolicyBackend returned error: %v", err)
	}
	if plan.BackendMode != backendNetworkPolicyIsolated || plan.LoopbackAction != netpolicy.ActionAllow {
		t.Fatalf("unexpected backend plan: %#v", plan)
	}
}

func TestPlanNetworkPolicyBackendAllowAllPolicyUsesHostPassthrough(t *testing.T) {
	policy := spec.AllowAllNetworkPolicy()
	plan, err := planNetworkPolicyBackend(spec.Config{
		NetworkPolicy: &policy,
	})
	if err != nil {
		t.Fatalf("planNetworkPolicyBackend returned error: %v", err)
	}
	if plan.BackendMode != backendNetworkPolicyHost || plan.LoopbackAction != netpolicy.ActionAllow {
		t.Fatalf("unexpected backend plan: %#v", plan)
	}
}

func TestPlanNetworkPolicyBackendSupportsAllowDefault(t *testing.T) {
	plan, err := planNetworkPolicyBackend(spec.Config{
		NetworkPolicy: &spec.NetworkPolicy{
			Version:  1,
			Loopback: spec.LoopbackPolicy{Default: spec.PolicyAllow},
			Ingress:  spec.IngressPolicy{Default: spec.PolicyDeny, Rules: []spec.IngressRule{}},
			Egress:   spec.EgressPolicy{Default: spec.PolicyAllow, Rules: []spec.EgressRule{}},
		},
	})
	if err != nil {
		t.Fatalf("planNetworkPolicyBackend returned error: %v", err)
	}
	if plan.BackendMode != backendNetworkPolicyRouted {
		t.Fatalf("expected routed backend, got %#v", plan)
	}
	if plan.SerializedPolicy == "" {
		t.Fatalf("expected serialized policy config, got %#v", plan)
	}
}

func TestPlanNetworkPolicyBackendAllowRuleNeedsRoutedUplink(t *testing.T) {
	plan, err := planNetworkPolicyBackend(spec.Config{
		NetworkPolicy: &spec.NetworkPolicy{
			Version:  1,
			Loopback: spec.LoopbackPolicy{Default: spec.PolicyAllow},
			Ingress:  spec.IngressPolicy{Default: spec.PolicyDeny, Rules: []spec.IngressRule{}},
			Egress: spec.EgressPolicy{
				Default: spec.PolicyDeny,
				Rules: []spec.EgressRule{{
					Name:        "allow-gateway",
					Action:      spec.PolicyAllow,
					Destination: spec.NetworkSelector{IP: "192.168.0.1"},
					Protocol:    spec.ProtocolAny,
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("planNetworkPolicyBackend returned error: %v", err)
	}
	if plan.BackendMode != backendNetworkPolicyRouted {
		t.Fatalf("expected routed backend, got %#v", plan)
	}
}

func TestNetworkPolicyBackendEncodingRoundTrip(t *testing.T) {
	original := netpolicy.Policy{
		Loopback: netpolicy.ActionDeny,
		Ingress: netpolicy.DirectionPolicy{
			Zone:    netpolicy.ZoneIngress,
			Default: netpolicy.ActionAllow,
			Rules: []netpolicy.Rule{{
				Order:    0,
				Name:     "allow-lan",
				Action:   netpolicy.ActionAllow,
				Selector: netpolicy.Selector{Kind: netpolicy.SelectorCIDR, Prefix: mustPrefix(t, "192.168.0.0/16")},
				Protocol: netpolicy.ProtocolTCP,
				Ports:    []uint16{443},
			}},
		},
		Egress: netpolicy.DirectionPolicy{
			Zone:    netpolicy.ZoneEgress,
			Default: netpolicy.ActionDeny,
		},
	}
	encoded, err := encodeNetworkPolicyBackend(original)
	if err != nil {
		t.Fatalf("encodeNetworkPolicyBackend returned error: %v", err)
	}
	decoded, err := decodeNetworkPolicyBackend(encoded)
	if err != nil {
		t.Fatalf("decodeNetworkPolicyBackend returned error: %v", err)
	}
	if !reflect.DeepEqual(decoded, original) {
		t.Fatalf("decoded policy = %#v, want %#v", decoded, original)
	}
}

func TestBuildPolicyNetworkCommandsAllowRuleBeforeBroaderDeny(t *testing.T) {
	policy := netpolicy.Policy{
		Loopback: netpolicy.ActionAllow,
		Ingress: netpolicy.DirectionPolicy{
			Zone:    netpolicy.ZoneIngress,
			Default: netpolicy.ActionDeny,
		},
		Egress: netpolicy.DirectionPolicy{
			Zone:    netpolicy.ZoneEgress,
			Default: netpolicy.ActionDeny,
			Rules: []netpolicy.Rule{
				{
					Order:    0,
					Name:     "allow-gateway",
					Action:   netpolicy.ActionAllow,
					Selector: netpolicy.Selector{Kind: netpolicy.SelectorIP, IP: mustAddr(t, "192.168.0.1")},
					Protocol: netpolicy.ProtocolAny,
				},
				{
					Order:    1,
					Name:     "deny-lan",
					Action:   netpolicy.ActionDeny,
					Selector: netpolicy.Selector{Kind: netpolicy.SelectorCIDR, Prefix: mustPrefix(t, "192.168.0.0/16")},
					Protocol: netpolicy.ProtocolAny,
				},
			},
		},
	}
	commands, err := buildPolicyNetworkCommands(policy)
	if err != nil {
		t.Fatalf("buildPolicyNetworkCommands returned error: %v", err)
	}
	got := flattenPolicyCommands(commands)
	want := []string{
		"iptables -w -P OUTPUT DROP",
		"iptables -w -A OUTPUT -o lo -j ACCEPT",
		"iptables -w -A OUTPUT -d 192.168.0.1 -j ACCEPT",
		"iptables -w -A OUTPUT -d 192.168.0.0/16 -j DROP",
	}
	for _, item := range want {
		if !strings.Contains(got, item) {
			t.Fatalf("expected command %q in %s", item, got)
		}
	}
	if strings.Index(got, "iptables -w -A OUTPUT -d 192.168.0.1 -j ACCEPT") >= strings.Index(got, "iptables -w -A OUTPUT -d 192.168.0.0/16 -j DROP") {
		t.Fatalf("expected gateway allow to precede broader deny: %s", got)
	}
}

func TestBuildPolicyNetworkCommandsMixedFamilyAndPorts(t *testing.T) {
	policy := netpolicy.Policy{
		Loopback: netpolicy.ActionDeny,
		Ingress: netpolicy.DirectionPolicy{
			Zone:    netpolicy.ZoneIngress,
			Default: netpolicy.ActionAllow,
		},
		Egress: netpolicy.DirectionPolicy{
			Zone:    netpolicy.ZoneEgress,
			Default: netpolicy.ActionDeny,
			Rules: []netpolicy.Rule{
				{
					Order:    0,
					Name:     "allow-v4-https",
					Action:   netpolicy.ActionAllow,
					Selector: netpolicy.Selector{Kind: netpolicy.SelectorCIDR, Prefix: mustPrefix(t, "203.0.113.0/24")},
					Protocol: netpolicy.ProtocolTCP,
					Ports:    []uint16{443},
				},
				{
					Order:    1,
					Name:     "allow-v6-dns",
					Action:   netpolicy.ActionAllow,
					Selector: netpolicy.Selector{Kind: netpolicy.SelectorIP, IP: mustAddr(t, "2001:db8::53")},
					Protocol: netpolicy.ProtocolUDP,
					Ports:    []uint16{53},
				},
			},
		},
	}
	commands, err := buildPolicyNetworkCommands(policy)
	if err != nil {
		t.Fatalf("buildPolicyNetworkCommands returned error: %v", err)
	}
	got := flattenPolicyCommands(commands)
	for _, item := range []string{
		"iptables -w -P INPUT ACCEPT",
		"iptables -w -A INPUT -i lo -j DROP",
		"iptables -w -A OUTPUT -d 203.0.113.0/24 -p tcp --dport 443 -j ACCEPT",
		"ip6tables -w -A OUTPUT -d 2001:db8::53 -p udp --dport 53 -j ACCEPT",
	} {
		if !strings.Contains(got, item) {
			t.Fatalf("expected command %q in %s", item, got)
		}
	}
}

func TestDecodeNetworkPolicyBackendRejectsMissingConfig(t *testing.T) {
	_, err := decodeNetworkPolicyBackend("")
	if err == nil || !strings.Contains(err.Error(), "config is missing") {
		t.Fatalf("expected missing config error, got %v", err)
	}
}

func mustAddr(t *testing.T, value string) netip.Addr {
	t.Helper()
	addr, err := netip.ParseAddr(value)
	if err != nil {
		t.Fatalf("ParseAddr(%q) returned error: %v", value, err)
	}
	return addr
}

func mustPrefix(t *testing.T, value string) netip.Prefix {
	t.Helper()
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		t.Fatalf("ParsePrefix(%q) returned error: %v", value, err)
	}
	return prefix
}

func flattenPolicyCommands(commands []packetFilterCommand) string {
	var lines []string
	for _, command := range commands {
		lines = append(lines, command.Name+" "+strings.Join(command.Args, " "))
	}
	return strings.Join(lines, "\n")
}
