package spec

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

type PolicyAction string

const (
	PolicyAllow PolicyAction = "allow"
	PolicyDeny  PolicyAction = "deny"
)

type NetworkProtocol string

const (
	ProtocolTCP  NetworkProtocol = "tcp"
	ProtocolUDP  NetworkProtocol = "udp"
	ProtocolICMP NetworkProtocol = "icmp"
	ProtocolAny  NetworkProtocol = "any"
)

type NetworkPolicy struct {
	Version  int            `json:"version" yaml:"version"`
	Loopback LoopbackPolicy `json:"loopback" yaml:"loopback"`
	Ingress  IngressPolicy  `json:"ingress" yaml:"ingress"`
	Egress   EgressPolicy   `json:"egress" yaml:"egress"`
}

type LoopbackPolicy struct {
	Default PolicyAction `json:"default" yaml:"default"`
}

type IngressPolicy struct {
	Default PolicyAction  `json:"default" yaml:"default"`
	Rules   []IngressRule `json:"rules" yaml:"rules"`
}

type EgressPolicy struct {
	Default PolicyAction `json:"default" yaml:"default"`
	Rules   []EgressRule `json:"rules" yaml:"rules"`
}

type IngressRule struct {
	Name     string          `json:"name,omitempty" yaml:"name,omitempty"`
	Action   PolicyAction    `json:"action" yaml:"action"`
	Source   NetworkSelector `json:"source" yaml:"source"`
	Protocol NetworkProtocol `json:"protocol" yaml:"protocol"`
	Ports    []int           `json:"ports,omitempty" yaml:"ports,omitempty"`
}

type EgressRule struct {
	Name        string          `json:"name,omitempty" yaml:"name,omitempty"`
	Action      PolicyAction    `json:"action" yaml:"action"`
	Destination NetworkSelector `json:"destination" yaml:"destination"`
	Protocol    NetworkProtocol `json:"protocol" yaml:"protocol"`
	Ports       []int           `json:"ports,omitempty" yaml:"ports,omitempty"`
}

type NetworkSelector struct {
	IP     string `json:"ip,omitempty" yaml:"ip,omitempty"`
	CIDR   string `json:"cidr,omitempty" yaml:"cidr,omitempty"`
	Domain string `json:"domain,omitempty" yaml:"domain,omitempty"`
}

type networkPolicyDocument struct {
	NetworkPolicy *NetworkPolicy `json:"networkPolicy" yaml:"networkPolicy"`
}

func (p *NetworkPolicy) UnmarshalYAML(value *yaml.Node) error {
	if err := rejectUnknownYAMLFields(value, "networkPolicy", "version", "loopback", "ingress", "egress"); err != nil {
		return err
	}
	var raw struct {
		Version  *int            `yaml:"version"`
		Loopback *LoopbackPolicy `yaml:"loopback"`
		Ingress  *IngressPolicy  `yaml:"ingress"`
		Egress   *EgressPolicy   `yaml:"egress"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	if raw.Version == nil {
		return errors.New("networkPolicy.version is required")
	}
	if raw.Loopback == nil {
		return errors.New("networkPolicy.loopback is required")
	}
	if raw.Ingress == nil {
		return errors.New("networkPolicy.ingress is required")
	}
	if raw.Egress == nil {
		return errors.New("networkPolicy.egress is required")
	}
	p.Version = *raw.Version
	p.Loopback = *raw.Loopback
	p.Ingress = *raw.Ingress
	p.Egress = *raw.Egress
	return nil
}

func (p *LoopbackPolicy) UnmarshalYAML(value *yaml.Node) error {
	if err := rejectUnknownYAMLFields(value, "loopback", "default"); err != nil {
		return err
	}
	var raw struct {
		Default *PolicyAction `yaml:"default"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	if raw.Default == nil {
		return errors.New("loopback.default is required")
	}
	p.Default = *raw.Default
	return nil
}

func (p *IngressPolicy) UnmarshalYAML(value *yaml.Node) error {
	if err := rejectUnknownYAMLFields(value, "ingress", "default", "rules"); err != nil {
		return err
	}
	var raw struct {
		Default *PolicyAction  `yaml:"default"`
		Rules   *[]IngressRule `yaml:"rules"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	if raw.Default == nil {
		return errors.New("ingress.default is required")
	}
	if raw.Rules == nil {
		return errors.New("ingress.rules is required")
	}
	p.Default = *raw.Default
	p.Rules = *raw.Rules
	return nil
}

func (p *EgressPolicy) UnmarshalYAML(value *yaml.Node) error {
	if err := rejectUnknownYAMLFields(value, "egress", "default", "rules"); err != nil {
		return err
	}
	var raw struct {
		Default *PolicyAction `yaml:"default"`
		Rules   *[]EgressRule `yaml:"rules"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	if raw.Default == nil {
		return errors.New("egress.default is required")
	}
	if raw.Rules == nil {
		return errors.New("egress.rules is required")
	}
	p.Default = *raw.Default
	p.Rules = *raw.Rules
	return nil
}

func (r *IngressRule) UnmarshalYAML(value *yaml.Node) error {
	if err := rejectUnknownYAMLFields(value, "ingress rule", "name", "action", "source", "protocol", "ports"); err != nil {
		return err
	}
	var raw struct {
		Name     *string          `yaml:"name"`
		Action   *PolicyAction    `yaml:"action"`
		Source   *NetworkSelector `yaml:"source"`
		Protocol *NetworkProtocol `yaml:"protocol"`
		Ports    *[]int           `yaml:"ports"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	if raw.Name != nil {
		if strings.TrimSpace(*raw.Name) == "" {
			return errors.New("ingress rule name must not be empty")
		}
		r.Name = *raw.Name
	}
	if raw.Action != nil {
		r.Action = *raw.Action
	}
	if raw.Source != nil {
		r.Source = *raw.Source
	}
	if raw.Protocol != nil {
		r.Protocol = *raw.Protocol
	}
	if raw.Ports != nil {
		r.Ports = *raw.Ports
	}
	return nil
}

func (r *EgressRule) UnmarshalYAML(value *yaml.Node) error {
	if err := rejectUnknownYAMLFields(value, "egress rule", "name", "action", "destination", "protocol", "ports"); err != nil {
		return err
	}
	var raw struct {
		Name        *string          `yaml:"name"`
		Action      *PolicyAction    `yaml:"action"`
		Destination *NetworkSelector `yaml:"destination"`
		Protocol    *NetworkProtocol `yaml:"protocol"`
		Ports       *[]int           `yaml:"ports"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	if raw.Name != nil {
		if strings.TrimSpace(*raw.Name) == "" {
			return errors.New("egress rule name must not be empty")
		}
		r.Name = *raw.Name
	}
	if raw.Action != nil {
		r.Action = *raw.Action
	}
	if raw.Destination != nil {
		r.Destination = *raw.Destination
	}
	if raw.Protocol != nil {
		r.Protocol = *raw.Protocol
	}
	if raw.Ports != nil {
		r.Ports = *raw.Ports
	}
	return nil
}

func (s *NetworkSelector) UnmarshalYAML(value *yaml.Node) error {
	if err := rejectUnknownYAMLFields(value, "network selector", "ip", "cidr", "domain"); err != nil {
		return err
	}
	var raw struct {
		IP     string `yaml:"ip"`
		CIDR   string `yaml:"cidr"`
		Domain string `yaml:"domain"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	s.IP = raw.IP
	s.CIDR = raw.CIDR
	s.Domain = raw.Domain
	return nil
}

func rejectUnknownYAMLFields(value *yaml.Node, context string, allowedFields ...string) error {
	if value.Kind != yaml.MappingNode {
		return nil
	}
	allowed := make(map[string]struct{}, len(allowedFields))
	for _, field := range allowedFields {
		allowed[field] = struct{}{}
	}
	for i := 0; i < len(value.Content); i += 2 {
		key := value.Content[i].Value
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("%s has unknown field %q", context, key)
		}
	}
	return nil
}

func LoadNetworkPolicyYAML(data []byte) (NetworkPolicy, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	var doc networkPolicyDocument
	if err := decoder.Decode(&doc); err != nil {
		return NetworkPolicy{}, fmt.Errorf("parse network policy: %w", err)
	}

	var extra any
	err := decoder.Decode(&extra)
	switch {
	case err == io.EOF:
	case err != nil:
		return NetworkPolicy{}, fmt.Errorf("parse network policy: %w", err)
	default:
		return NetworkPolicy{}, errors.New("parse network policy: multiple YAML documents are not supported")
	}

	if doc.NetworkPolicy == nil {
		return NetworkPolicy{}, errors.New("networkPolicy is required")
	}
	return NormalizeNetworkPolicy(*doc.NetworkPolicy)
}

func ValidateNetworkPolicy(policy *NetworkPolicy) error {
	if policy == nil {
		return errors.New("networkPolicy is required")
	}

	var problems []error
	if policy.Version != 1 {
		problems = append(problems, fmt.Errorf("networkPolicy.version must be 1, got %d", policy.Version))
	}
	validateDefaultAction("networkPolicy.loopback.default", policy.Loopback.Default, &problems)
	validateDefaultAction("networkPolicy.ingress.default", policy.Ingress.Default, &problems)
	validateDefaultAction("networkPolicy.egress.default", policy.Egress.Default, &problems)
	validateIngressRules(policy.Ingress.Rules, &problems)
	validateEgressRules(policy.Egress.Rules, &problems)

	if len(problems) > 0 {
		return errors.Join(problems...)
	}
	return nil
}

func NormalizeNetworkPolicy(policy NetworkPolicy) (NetworkPolicy, error) {
	if err := ValidateNetworkPolicy(&policy); err != nil {
		return NetworkPolicy{}, err
	}

	out := policy
	out.Ingress.Rules = append([]IngressRule(nil), policy.Ingress.Rules...)
	for i := range out.Ingress.Rules {
		selector, err := normalizeSelector(out.Ingress.Rules[i].Source, false)
		if err != nil {
			return NetworkPolicy{}, fmt.Errorf("networkPolicy.ingress.rules[%d].source: %w", i, err)
		}
		out.Ingress.Rules[i].Source = selector
		out.Ingress.Rules[i].Ports = append([]int(nil), policy.Ingress.Rules[i].Ports...)
	}
	out.Egress.Rules = append([]EgressRule(nil), policy.Egress.Rules...)
	for i := range out.Egress.Rules {
		selector, err := normalizeSelector(out.Egress.Rules[i].Destination, true)
		if err != nil {
			return NetworkPolicy{}, fmt.Errorf("networkPolicy.egress.rules[%d].destination: %w", i, err)
		}
		out.Egress.Rules[i].Destination = selector
		out.Egress.Rules[i].Ports = append([]int(nil), policy.Egress.Rules[i].Ports...)
	}
	return out, nil
}

func validateDefaultAction(field string, action PolicyAction, problems *[]error) {
	if action != PolicyAllow && action != PolicyDeny {
		*problems = append(*problems, fmt.Errorf("%s must be allow or deny", field))
	}
}

func validateIngressRules(rules []IngressRule, problems *[]error) {
	seenNames := map[string]struct{}{}
	for i := range rules {
		rule := rules[i]
		prefix := "networkPolicy.ingress.rules[" + strconv.Itoa(i) + "]"
		validateRuleName(prefix, rule.Name, seenNames, problems)
		validateRuleAction(prefix, rule.Action, problems)
		validateSelector(prefix+".source", rule.Source, false, problems)
		validateProtocolAndPorts(prefix, rule.Protocol, rule.Ports, problems)
	}
}

func validateEgressRules(rules []EgressRule, problems *[]error) {
	seenNames := map[string]struct{}{}
	for i := range rules {
		rule := rules[i]
		prefix := "networkPolicy.egress.rules[" + strconv.Itoa(i) + "]"
		validateRuleName(prefix, rule.Name, seenNames, problems)
		validateRuleAction(prefix, rule.Action, problems)
		validateSelector(prefix+".destination", rule.Destination, true, problems)
		validateProtocolAndPorts(prefix, rule.Protocol, rule.Ports, problems)
	}
}

func validateRuleName(prefix, name string, seen map[string]struct{}, problems *[]error) {
	if name == "" {
		return
	}
	if strings.TrimSpace(name) == "" {
		*problems = append(*problems, fmt.Errorf("%s.name must not be empty", prefix))
		return
	}
	if _, ok := seen[name]; ok {
		*problems = append(*problems, fmt.Errorf("%s.name %q is duplicated in this direction", prefix, name))
		return
	}
	seen[name] = struct{}{}
}

func validateRuleAction(prefix string, action PolicyAction, problems *[]error) {
	if action != PolicyAllow && action != PolicyDeny {
		*problems = append(*problems, fmt.Errorf("%s.action must be allow or deny", prefix))
	}
}

func validateProtocolAndPorts(prefix string, protocol NetworkProtocol, ports []int, problems *[]error) {
	switch protocol {
	case ProtocolTCP, ProtocolUDP:
	case ProtocolICMP, ProtocolAny:
		if len(ports) > 0 {
			*problems = append(*problems, fmt.Errorf("%s.ports are only valid with tcp or udp", prefix))
		}
	default:
		*problems = append(*problems, fmt.Errorf("%s.protocol must be tcp, udp, icmp, or any", prefix))
	}
	if ports == nil {
		return
	}
	if len(ports) == 0 {
		*problems = append(*problems, fmt.Errorf("%s.ports must not be empty when present", prefix))
		return
	}
	seen := map[int]struct{}{}
	for _, port := range ports {
		if port < 1 || port > 65535 {
			*problems = append(*problems, fmt.Errorf("%s.ports contains invalid port %d", prefix, port))
			continue
		}
		if _, ok := seen[port]; ok {
			*problems = append(*problems, fmt.Errorf("%s.ports contains duplicate port %d", prefix, port))
			continue
		}
		seen[port] = struct{}{}
	}
}

func validateSelector(prefix string, selector NetworkSelector, allowDomain bool, problems *[]error) {
	_, err := normalizeSelector(selector, allowDomain)
	if err != nil {
		*problems = append(*problems, fmt.Errorf("%s %v", prefix, err))
	}
}

func normalizeSelector(selector NetworkSelector, allowDomain bool) (NetworkSelector, error) {
	var fields []string
	if selector.IP != "" {
		fields = append(fields, "ip")
	}
	if selector.CIDR != "" {
		fields = append(fields, "cidr")
	}
	if selector.Domain != "" {
		fields = append(fields, "domain")
	}
	if len(fields) != 1 {
		return NetworkSelector{}, errors.New("must define exactly one of ip, cidr, or domain")
	}

	switch fields[0] {
	case "ip":
		addr, err := netip.ParseAddr(strings.TrimSpace(selector.IP))
		if err != nil {
			return NetworkSelector{}, fmt.Errorf("ip is invalid: %w", err)
		}
		if addr.IsLoopback() {
			return NetworkSelector{}, errors.New("ip must not be loopback")
		}
		return NetworkSelector{IP: addr.String()}, nil
	case "cidr":
		prefixValue, err := netip.ParsePrefix(strings.TrimSpace(selector.CIDR))
		if err != nil {
			return NetworkSelector{}, fmt.Errorf("cidr is invalid: %w", err)
		}
		prefixValue = prefixValue.Masked()
		if prefixValue.Addr().IsLoopback() {
			return NetworkSelector{}, errors.New("cidr must not be loopback")
		}
		return NetworkSelector{CIDR: prefixValue.String()}, nil
	case "domain":
		if !allowDomain {
			return NetworkSelector{}, errors.New("domain is only valid for egress destinations")
		}
		normalized, err := normalizeDomain(selector.Domain)
		if err != nil {
			return NetworkSelector{}, fmt.Errorf("domain is invalid: %w", err)
		}
		return NetworkSelector{Domain: normalized}, nil
	}
	return NetworkSelector{}, errors.New("selector is invalid")
}

func normalizeDomain(value string) (string, error) {
	domain := strings.ToLower(strings.TrimSpace(value))
	domain = strings.TrimSuffix(domain, ".")
	if domain == "" {
		return "", errors.New("empty domain")
	}
	if len(domain) > 253 {
		return "", errors.New("domain is longer than 253 characters")
	}
	if strings.ContainsAny(domain, "/:@") || strings.Contains(domain, "*") {
		return "", errors.New("domain must not include scheme, path, port, userinfo, or wildcards")
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" {
			return "", errors.New("domain contains an empty label")
		}
		if len(label) > 63 {
			return "", fmt.Errorf("domain label %q is longer than 63 characters", label)
		}
		for i, r := range label {
			if r == '-' {
				if i == 0 || i == len(label)-1 {
					return "", fmt.Errorf("domain label %q must not start or end with hyphen", label)
				}
				continue
			}
			if r > unicode.MaxASCII || (!unicode.IsLetter(r) && !unicode.IsDigit(r)) {
				return "", fmt.Errorf("domain label %q contains invalid character %q", label, r)
			}
		}
	}
	return domain, nil
}
