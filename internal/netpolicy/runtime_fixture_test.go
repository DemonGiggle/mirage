package netpolicy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DemonGiggle/mirage/internal/spec"
)

func TestRuntimePolicyFixtureDecisions(t *testing.T) {
	cases := []struct {
		name    string
		desc    string
		traffic Traffic
		want    Action
	}{
		{name: "offline.yaml", desc: "deny_external", traffic: tcp("203.0.113.10", 443), want: ActionDeny},
		{name: "offline.yaml", desc: "allow_loopback", traffic: tcp("127.0.0.1", 5432), want: ActionAllow},
		{name: "block-local-egress.yaml", desc: "deny_lan", traffic: tcp("192.168.1.10", 443), want: ActionDeny},
		{name: "block-local-egress.yaml", desc: "allow_public", traffic: tcp("203.0.113.10", 443), want: ActionAllow},
		{name: "first-match-deny.yaml", desc: "deny_matched", traffic: tcp("10.0.0.5", 443), want: ActionDeny},
		{name: "first-match-allow.yaml", desc: "allow_matched", traffic: tcp("192.168.1.10", 443), want: ActionAllow},
		{name: "loopback-deny-egress-allow.yaml", desc: "deny_loopback", traffic: tcp("::1", 8080), want: ActionDeny},
		{name: "loopback-deny-egress-allow.yaml", desc: "allow_egress", traffic: tcp("203.0.113.10", 8080), want: ActionAllow},
	}

	for _, tc := range cases {
		t.Run(tc.name+"/"+tc.desc, func(t *testing.T) {
			runtimePolicy := mustCompilePolicyFixture(t, tc.name)
			if got := runtimePolicy.DecideEgress(tc.traffic); got != tc.want {
				t.Fatalf("DecideEgress returned %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRuntimePolicyFixtureIngressDefault(t *testing.T) {
	runtimePolicy := mustCompilePolicyFixture(t, "default-ingress-allow.yaml")
	if got := runtimePolicy.DecideIngress(tcp("203.0.113.7", 443)); got != ActionAllow {
		t.Fatalf("expected ingress default allow, got %q", got)
	}
	if got := runtimePolicy.DecideIngress(tcp("198.51.100.12", 443)); got != ActionDeny {
		t.Fatalf("expected ingress deny rule, got %q", got)
	}
}

func TestRuntimePolicyDomainFixtureIsCompileTimeDeferred(t *testing.T) {
	policy, err := spec.LoadNetworkPolicyYAML(readRuntimePolicyFixture(t, "domain-egress.yaml"))
	if err != nil {
		t.Fatalf("LoadNetworkPolicyYAML returned error: %v", err)
	}
	_, err = Compile(policy)
	if err == nil || !strings.Contains(err.Error(), "destination.domain is documented but not enforceable") {
		t.Fatalf("expected domain deferment error, got %v", err)
	}
}

func mustCompilePolicyFixture(t *testing.T, name string) Policy {
	t.Helper()
	policy, err := spec.LoadNetworkPolicyYAML(readRuntimePolicyFixture(t, name))
	if err != nil {
		t.Fatalf("LoadNetworkPolicyYAML returned error: %v", err)
	}
	runtimePolicy, err := Compile(policy)
	if err != nil {
		t.Fatalf("Compile returned error: %v", err)
	}
	return runtimePolicy
}

func readRuntimePolicyFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "network-policies", name))
	if err != nil {
		t.Fatalf("read policy fixture %q: %v", name, err)
	}
	return data
}
