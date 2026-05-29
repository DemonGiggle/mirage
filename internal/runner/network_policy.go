package runner

import (
	"errors"
	"fmt"
	"os/exec"

	"github.com/DemonGiggle/mirage/internal/netpolicy"
	"github.com/DemonGiggle/mirage/internal/spec"
)

const (
	backendNetworkPolicyHost     = "host"
	backendNetworkPolicyIsolated = "isolated"
)

var policyNetworkCommand = exec.Command

type networkPolicyBackendPlan struct {
	Policy         netpolicy.Policy
	BackendMode    string
	LoopbackAction netpolicy.Action
}

func planNetworkPolicyBackend(cfg spec.Config) (networkPolicyBackendPlan, error) {
	if cfg.NetworkPolicy == nil {
		return networkPolicyBackendPlan{}, errors.New("network policy configuration is missing")
	}
	policy, err := netpolicy.Compile(*cfg.NetworkPolicy)
	if err != nil {
		return networkPolicyBackendPlan{}, err
	}
	if policyIsHostPassthrough(policy) {
		return networkPolicyBackendPlan{
			Policy:         policy,
			BackendMode:    backendNetworkPolicyHost,
			LoopbackAction: policy.Loopback,
		}, nil
	}
	if err := validateIsolatedPolicyBackendSupport(policy); err != nil {
		return networkPolicyBackendPlan{}, err
	}
	return networkPolicyBackendPlan{
		Policy:         policy,
		BackendMode:    backendNetworkPolicyIsolated,
		LoopbackAction: policy.Loopback,
	}, nil
}

func policyIsHostPassthrough(policy netpolicy.Policy) bool {
	return policy.Loopback == netpolicy.ActionAllow &&
		policy.Ingress.Default == netpolicy.ActionAllow &&
		policy.Egress.Default == netpolicy.ActionAllow &&
		len(policy.Ingress.Rules) == 0 &&
		len(policy.Egress.Rules) == 0
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
			unsupported = append(unsupported, fmt.Sprintf("ingress allow rule %s", ruleIdentifier(rule)))
		}
	}
	for _, rule := range policy.Egress.Rules {
		if rule.Action == netpolicy.ActionAllow {
			unsupported = append(unsupported, fmt.Sprintf("egress allow rule %s", ruleIdentifier(rule)))
		}
	}
	if len(unsupported) > 0 {
		return fmt.Errorf("networkPolicy requires allow semantics this backend cannot enforce yet: %v", unsupported)
	}
	return nil
}

func ruleIdentifier(rule netpolicy.Rule) string {
	if rule.Name == "" {
		return fmt.Sprintf("index %d", rule.Order)
	}
	return fmt.Sprintf("%q", rule.Name)
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
