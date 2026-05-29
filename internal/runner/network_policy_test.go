package runner

import (
	"os/exec"
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

func TestPlanNetworkPolicyBackendRejectsAllowDefault(t *testing.T) {
	_, err := planNetworkPolicyBackend(spec.Config{
		NetworkPolicy: &spec.NetworkPolicy{
			Version:  1,
			Loopback: spec.LoopbackPolicy{Default: spec.PolicyAllow},
			Ingress:  spec.IngressPolicy{Default: spec.PolicyDeny, Rules: []spec.IngressRule{}},
			Egress:   spec.EgressPolicy{Default: spec.PolicyAllow, Rules: []spec.EgressRule{}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "egress.default=allow") {
		t.Fatalf("expected unsupported egress default error, got %v", err)
	}
}

func TestValidateIsolatedPolicyBackendSupportUnnamedRuleUsesOrder(t *testing.T) {
	err := validateIsolatedPolicyBackendSupport(netpolicy.Policy{
		Ingress: netpolicy.DirectionPolicy{
			Default: netpolicy.ActionDeny,
			Rules: []netpolicy.Rule{{
				Order:  2,
				Action: netpolicy.ActionAllow,
			}},
		},
		Egress: netpolicy.DirectionPolicy{Default: netpolicy.ActionDeny},
	})
	if err == nil || !strings.Contains(err.Error(), "ingress allow rule index 2") {
		t.Fatalf("expected unnamed rule order in error, got %v", err)
	}
}

func TestConfigurePolicyNetworkBackendDenyDoesNotInvokeIP(t *testing.T) {
	previous := policyNetworkCommand
	t.Cleanup(func() {
		policyNetworkCommand = previous
	})
	policyNetworkCommand = func(name string, arg ...string) *exec.Cmd {
		t.Fatalf("did not expect command for loopback deny: %s %v", name, arg)
		return nil
	}

	if err := configurePolicyNetworkBackend(string(netpolicy.ActionDeny)); err != nil {
		t.Fatalf("configurePolicyNetworkBackend returned error: %v", err)
	}
}

func TestConfigurePolicyNetworkBackendRejectsInvalidLoopbackAction(t *testing.T) {
	err := configurePolicyNetworkBackend("maybe")
	if err == nil || !strings.Contains(err.Error(), "loopback action") {
		t.Fatalf("expected invalid loopback action error, got %v", err)
	}
}
