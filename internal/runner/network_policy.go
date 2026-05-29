package runner

import (
	"errors"
	"fmt"
	"os/exec"

	"github.com/DemonGiggle/mirage/internal/netpolicy"
	"github.com/DemonGiggle/mirage/internal/spec"
)

const backendNetworkPolicyIsolated = "policy-isolated"

var policyNetworkCommand = exec.Command

type networkPolicyBackendPlan struct {
	Policy         netpolicy.Policy
	BackendMode    string
	LoopbackAction netpolicy.Action
}

func planNetworkPolicyBackend(cfg spec.Config) (networkPolicyBackendPlan, bool, error) {
	if cfg.NetworkPolicy == nil {
		return networkPolicyBackendPlan{}, false, nil
	}
	policy, err := netpolicy.Compile(*cfg.NetworkPolicy)
	if err != nil {
		return networkPolicyBackendPlan{}, true, err
	}
	if err := validateIsolatedPolicyBackendSupport(policy); err != nil {
		return networkPolicyBackendPlan{}, true, err
	}
	return networkPolicyBackendPlan{
		Policy:         policy,
		BackendMode:    backendNetworkPolicyIsolated,
		LoopbackAction: policy.Loopback,
	}, true, nil
}

func validateIsolatedPolicyBackendSupport(policy netpolicy.Policy) error {
	var unsupported []string
	if policy.Ingress.Default == netpolicy.ActionAllow {
		unsupported = append(unsupported, "ingress.default=allow")
	}
	if policy.Egress.Default == netpolicy.ActionAllow {
		unsupported = append(unsupported, "egress.default=allow")
	}
	for _, rule := range policy.Ingress.Rules {
		if rule.Action == netpolicy.ActionAllow {
			unsupported = append(unsupported, fmt.Sprintf("ingress allow rule %q", rule.Name))
		}
	}
	for _, rule := range policy.Egress.Rules {
		if rule.Action == netpolicy.ActionAllow {
			unsupported = append(unsupported, fmt.Sprintf("egress allow rule %q", rule.Name))
		}
	}
	if len(unsupported) > 0 {
		return fmt.Errorf("networkPolicy requires allow semantics this backend cannot enforce yet: %v", unsupported)
	}
	return nil
}

func configurePolicyNetworkBackend(loopbackAction string) error {
	switch netpolicy.Action(loopbackAction) {
	case netpolicy.ActionAllow:
		if err := policyNetworkCommand("ip", "link", "set", "lo", "up").Run(); err != nil {
			return fmt.Errorf("enable loopback for networkPolicy backend: %w", err)
		}
	case netpolicy.ActionDeny:
		return nil
	default:
		return errors.New("networkPolicy backend loopback action must be allow or deny")
	}
	return nil
}
