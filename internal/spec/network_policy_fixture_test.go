package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNetworkPolicyFixturesValidate(t *testing.T) {
	for _, name := range []string{
		"offline.yaml",
		"allow-private-egress.yaml",
		"first-match-deny.yaml",
		"first-match-allow.yaml",
		"default-ingress-allow.yaml",
		"loopback-deny-egress-allow.yaml",
		"domain-egress.yaml",
	} {
		t.Run(name, func(t *testing.T) {
			policy, err := LoadNetworkPolicyYAML(readPolicyFixture(t, name))
			if err != nil {
				t.Fatalf("LoadNetworkPolicyYAML returned error: %v", err)
			}
			if policy.Version != 1 {
				t.Fatalf("unexpected version: %d", policy.Version)
			}
		})
	}
}

func TestNetworkPolicyInvalidFixturesReject(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{name: "invalid-mixed-selector.yaml", want: "must define exactly one"},
		{name: "invalid-ports-with-any.yaml", want: "ports are only valid with tcp or udp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadNetworkPolicyYAML(readPolicyFixture(t, tc.name))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func readPolicyFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "network-policies", name))
	if err != nil {
		t.Fatalf("read policy fixture %q: %v", name, err)
	}
	return data
}
