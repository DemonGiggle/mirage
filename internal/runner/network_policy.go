package runner

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"

	"github.com/DemonGiggle/mirage/internal/netpolicy"
	"github.com/DemonGiggle/mirage/internal/spec"
)

const (
	backendNetworkPolicyHost     = "host"
	backendNetworkPolicyIsolated = "isolated"
	backendNetworkPolicyRouted   = "routed"
)

var policyNetworkCommand = exec.Command

type networkPolicyBackendPlan struct {
	Policy           netpolicy.Policy
	BackendMode      string
	LoopbackAction   netpolicy.Action
	SerializedPolicy string
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
			Policy:           policy,
			BackendMode:      backendNetworkPolicyHost,
			LoopbackAction:   policy.Loopback,
			SerializedPolicy: "",
		}, nil
	}
	serializedPolicy, err := encodeNetworkPolicyBackend(policy)
	if err != nil {
		return networkPolicyBackendPlan{}, err
	}
	backendMode := backendNetworkPolicyIsolated
	if policyNeedsRoutedUplink(policy) {
		backendMode = backendNetworkPolicyRouted
	}
	return networkPolicyBackendPlan{
		Policy:           policy,
		BackendMode:      backendMode,
		LoopbackAction:   policy.Loopback,
		SerializedPolicy: serializedPolicy,
	}, nil
}

func policyIsHostPassthrough(policy netpolicy.Policy) bool {
	return policy.Loopback == netpolicy.ActionAllow &&
		policy.Ingress.Default == netpolicy.ActionAllow &&
		policy.Egress.Default == netpolicy.ActionAllow &&
		len(policy.Ingress.Rules) == 0 &&
		len(policy.Egress.Rules) == 0
}

func policyNeedsRoutedUplink(policy netpolicy.Policy) bool {
	if policy.Egress.Default == netpolicy.ActionAllow {
		return true
	}
	for _, rule := range policy.Egress.Rules {
		if rule.Action == netpolicy.ActionAllow {
			return true
		}
	}
	return false
}

func encodeNetworkPolicyBackend(policy netpolicy.Policy) (string, error) {
	data, err := json.Marshal(policy)
	if err != nil {
		return "", fmt.Errorf("marshal network policy backend config: %w", err)
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

func decodeNetworkPolicyBackend(encoded string) (netpolicy.Policy, error) {
	if encoded == "" {
		return netpolicy.Policy{}, errors.New("networkPolicy backend config is missing")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return netpolicy.Policy{}, fmt.Errorf("decode network policy backend config: %w", err)
	}
	var policy netpolicy.Policy
	if err := json.Unmarshal(data, &policy); err != nil {
		return netpolicy.Policy{}, fmt.Errorf("unmarshal network policy backend config: %w", err)
	}
	return policy, nil
}
