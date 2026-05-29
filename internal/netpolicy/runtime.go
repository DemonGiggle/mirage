package netpolicy

import (
	"errors"
	"fmt"
	"net/netip"

	"github.com/DemonGiggle/mirage/internal/spec"
)

type Zone string

const (
	ZoneLoopback Zone = "loopback"
	ZoneIngress  Zone = "ingress"
	ZoneEgress   Zone = "egress"
)

type Action string

const (
	ActionAllow Action = "allow"
	ActionDeny  Action = "deny"
)

type Protocol string

const (
	ProtocolTCP  Protocol = "tcp"
	ProtocolUDP  Protocol = "udp"
	ProtocolICMP Protocol = "icmp"
	ProtocolAny  Protocol = "any"
)

type SelectorKind string

const (
	SelectorIP   SelectorKind = "ip"
	SelectorCIDR SelectorKind = "cidr"
)

type Policy struct {
	Loopback Action
	Ingress  DirectionPolicy
	Egress   DirectionPolicy
}

type DirectionPolicy struct {
	Zone    Zone
	Default Action
	Rules   []Rule
}

type Rule struct {
	Order    int
	Name     string
	Action   Action
	Selector Selector
	Protocol Protocol
	Ports    []uint16
}

type Selector struct {
	Kind   SelectorKind
	IP     netip.Addr
	Prefix netip.Prefix
}

type Traffic struct {
	Remote   netip.Addr
	Protocol Protocol
	Port     uint16
}

func Compile(policy spec.NetworkPolicy) (Policy, error) {
	if err := spec.ValidateNetworkPolicy(&policy); err != nil {
		return Policy{}, err
	}

	ingress, err := compileIngress(policy.Ingress)
	if err != nil {
		return Policy{}, err
	}
	egress, err := compileEgress(policy.Egress)
	if err != nil {
		return Policy{}, err
	}

	return Policy{
		Loopback: compileAction(policy.Loopback.Default),
		Ingress:  ingress,
		Egress:   egress,
	}, nil
}

func (p Policy) DecideIngress(traffic Traffic) Action {
	traffic.Remote = traffic.Remote.Unmap()
	if traffic.Remote.IsLoopback() {
		return p.Loopback
	}
	return p.Ingress.Decide(traffic)
}

func (p Policy) DecideEgress(traffic Traffic) Action {
	traffic.Remote = traffic.Remote.Unmap()
	if traffic.Remote.IsLoopback() {
		return p.Loopback
	}
	return p.Egress.Decide(traffic)
}

func (p DirectionPolicy) Decide(traffic Traffic) Action {
	traffic.Remote = traffic.Remote.Unmap()
	for _, rule := range p.Rules {
		if rule.Matches(traffic) {
			return rule.Action
		}
	}
	return p.Default
}

func (r Rule) Matches(traffic Traffic) bool {
	if !r.Selector.Matches(traffic.Remote) {
		return false
	}
	if r.Protocol != ProtocolAny && r.Protocol != traffic.Protocol {
		return false
	}
	if len(r.Ports) == 0 {
		return true
	}
	for _, port := range r.Ports {
		if port == traffic.Port {
			return true
		}
	}
	return false
}

func (s Selector) Matches(addr netip.Addr) bool {
	switch s.Kind {
	case SelectorIP:
		return s.IP == addr
	case SelectorCIDR:
		return s.Prefix.Contains(addr)
	default:
		return false
	}
}

func compileIngress(policy spec.IngressPolicy) (DirectionPolicy, error) {
	rules := make([]Rule, 0, len(policy.Rules))
	for i, rule := range policy.Rules {
		compiled, err := compileRule(i, rule.Name, rule.Action, rule.Source, rule.Protocol, rule.Ports)
		if err != nil {
			return DirectionPolicy{}, fmt.Errorf("compile ingress rule %d: %w", i, err)
		}
		rules = append(rules, compiled)
	}
	return DirectionPolicy{Zone: ZoneIngress, Default: compileAction(policy.Default), Rules: rules}, nil
}

func compileEgress(policy spec.EgressPolicy) (DirectionPolicy, error) {
	rules := make([]Rule, 0, len(policy.Rules))
	for i, rule := range policy.Rules {
		compiled, err := compileRule(i, rule.Name, rule.Action, rule.Destination, rule.Protocol, rule.Ports)
		if err != nil {
			return DirectionPolicy{}, fmt.Errorf("compile egress rule %d: %w", i, err)
		}
		rules = append(rules, compiled)
	}
	return DirectionPolicy{Zone: ZoneEgress, Default: compileAction(policy.Default), Rules: rules}, nil
}

func compileRule(order int, name string, action spec.PolicyAction, selector spec.NetworkSelector, protocol spec.NetworkProtocol, ports []int) (Rule, error) {
	compiledSelector, err := compileSelector(selector)
	if err != nil {
		return Rule{}, err
	}
	compiledPorts := make([]uint16, 0, len(ports))
	for _, port := range ports {
		compiledPorts = append(compiledPorts, uint16(port))
	}
	return Rule{
		Order:    order,
		Name:     name,
		Action:   compileAction(action),
		Selector: compiledSelector,
		Protocol: compileProtocol(protocol),
		Ports:    compiledPorts,
	}, nil
}

func compileSelector(selector spec.NetworkSelector) (Selector, error) {
	switch {
	case selector.IP != "":
		addr, err := netip.ParseAddr(selector.IP)
		if err != nil {
			return Selector{}, err
		}
		return Selector{Kind: SelectorIP, IP: addr.Unmap()}, nil
	case selector.CIDR != "":
		prefix, err := netip.ParsePrefix(selector.CIDR)
		if err != nil {
			return Selector{}, err
		}
		prefix = unmapPrefix(prefix)
		return Selector{Kind: SelectorCIDR, Prefix: prefix.Masked()}, nil
	case selector.Domain != "":
		return Selector{}, errors.New("destination.domain is documented but not enforceable by the runtime policy compiler yet")
	default:
		return Selector{}, errors.New("selector is empty")
	}
}

func unmapPrefix(prefix netip.Prefix) netip.Prefix {
	addr := prefix.Addr()
	if !addr.Is4In6() {
		return prefix
	}
	bits := prefix.Bits() - 96
	if bits < 0 {
		bits = 0
	}
	return netip.PrefixFrom(addr.Unmap(), bits)
}

func compileAction(action spec.PolicyAction) Action {
	switch action {
	case spec.PolicyAllow:
		return ActionAllow
	default:
		return ActionDeny
	}
}

func compileProtocol(protocol spec.NetworkProtocol) Protocol {
	switch protocol {
	case spec.ProtocolTCP:
		return ProtocolTCP
	case spec.ProtocolUDP:
		return ProtocolUDP
	case spec.ProtocolICMP:
		return ProtocolICMP
	default:
		return ProtocolAny
	}
}
