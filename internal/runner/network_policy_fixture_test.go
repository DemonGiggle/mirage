package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DemonGiggle/mirage/internal/spec"
)

func TestNetworkPolicyBackendFixtureSupportMatrix(t *testing.T) {
	cases := []struct {
		name        string
		wantErrPart string
	}{
		{name: "offline.yaml"},
		{name: "allow-private-egress.yaml", wantErrPart: "allow semantics"},
		{name: "first-match-deny.yaml", wantErrPart: "allow semantics"},
		{name: "loopback-deny-egress-allow.yaml", wantErrPart: "egress.default=allow"},
		{name: "domain-egress.yaml", wantErrPart: "destination.domain is documented but not enforceable"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := loadRunnerPolicyFixture(t, tc.name)
			_, err := planNetworkPolicyBackend(spec.Config{NetworkPolicy: &policy})
			if tc.wantErrPart == "" {
				if err != nil {
					t.Fatalf("planNetworkPolicyBackend returned error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErrPart) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErrPart, err)
			}
		})
	}
}

func loadRunnerPolicyFixture(t *testing.T, name string) spec.NetworkPolicy {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "network-policies", name))
	if err != nil {
		t.Fatalf("read policy fixture %q: %v", name, err)
	}
	policy, err := spec.LoadNetworkPolicyYAML(data)
	if err != nil {
		t.Fatalf("LoadNetworkPolicyYAML returned error: %v", err)
	}
	return policy
}
