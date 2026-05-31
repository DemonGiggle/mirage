package runner

import (
	"errors"
	"fmt"

	"github.com/DemonGiggle/mirage/internal/netpolicy"
)

type packetFilterCommand struct {
	Name string
	Args []string
}

type packetFilterFamily struct {
	command      string
	icmpProtocol string
}

var packetFilterFamilies = []packetFilterFamily{
	{command: "iptables", icmpProtocol: "icmp"},
	{command: "ip6tables", icmpProtocol: "ipv6-icmp"},
}

func buildPolicyNetworkCommands(policy netpolicy.Policy) ([]packetFilterCommand, error) {
	switch policy.Loopback {
	case netpolicy.ActionAllow, netpolicy.ActionDeny:
	default:
		return nil, errors.New("networkPolicy backend loopback action must be allow or deny")
	}

	var commands []packetFilterCommand
	for _, family := range packetFilterFamilies {
		commands = append(commands, buildPolicyChainCommands(policy, family)...)
	}
	return commands, nil
}

func buildPolicyChainCommands(policy netpolicy.Policy, family packetFilterFamily) []packetFilterCommand {
	commands := []packetFilterCommand{
		{Name: family.command, Args: []string{"-w", "-F", "INPUT"}},
		{Name: family.command, Args: []string{"-w", "-F", "OUTPUT"}},
		{Name: family.command, Args: []string{"-w", "-P", "INPUT", packetFilterTarget(policy.Ingress.Default)}},
		{Name: family.command, Args: []string{"-w", "-P", "OUTPUT", packetFilterTarget(policy.Egress.Default)}},
		{Name: family.command, Args: []string{"-w", "-A", "INPUT", "-i", "lo", "-j", packetFilterTarget(policy.Loopback)}},
		{Name: family.command, Args: []string{"-w", "-A", "OUTPUT", "-o", "lo", "-j", packetFilterTarget(policy.Loopback)}},
	}
	commands = append(commands, buildPolicyRuleCommands("INPUT", policy.Ingress.Rules, family)...)
	commands = append(commands, buildPolicyRuleCommands("OUTPUT", policy.Egress.Rules, family)...)
	return commands
}

func buildPolicyRuleCommands(chain string, rules []netpolicy.Rule, family packetFilterFamily) []packetFilterCommand {
	var commands []packetFilterCommand
	for _, rule := range rules {
		selector, ok := packetFilterSelector(rule.Selector)
		if !ok || selectorFamilyCommand(rule.Selector) != family.command {
			continue
		}

		baseArgs := []string{"-w", "-A", chain}
		if chain == "INPUT" {
			baseArgs = append(baseArgs, "-s", selector)
		} else {
			baseArgs = append(baseArgs, "-d", selector)
		}
		protocolArgs := packetFilterProtocolArgs(rule.Protocol, family)
		if len(rule.Ports) == 0 {
			args := append(append([]string{}, baseArgs...), protocolArgs...)
			args = append(args, "-j", packetFilterTarget(rule.Action))
			commands = append(commands, packetFilterCommand{Name: family.command, Args: args})
			continue
		}
		for _, port := range rule.Ports {
			args := append(append([]string{}, baseArgs...), protocolArgs...)
			args = append(args, "--dport", fmt.Sprintf("%d", port), "-j", packetFilterTarget(rule.Action))
			commands = append(commands, packetFilterCommand{Name: family.command, Args: args})
		}
	}
	return commands
}

func packetFilterSelector(selector netpolicy.Selector) (string, bool) {
	switch selector.Kind {
	case netpolicy.SelectorIP:
		return selector.IP.String(), true
	case netpolicy.SelectorCIDR:
		return selector.Prefix.String(), true
	default:
		return "", false
	}
}

func selectorFamilyCommand(selector netpolicy.Selector) string {
	switch selector.Kind {
	case netpolicy.SelectorIP:
		if selector.IP.Is6() {
			return "ip6tables"
		}
	case netpolicy.SelectorCIDR:
		if selector.Prefix.Addr().Is6() {
			return "ip6tables"
		}
	}
	return "iptables"
}

func packetFilterProtocolArgs(protocol netpolicy.Protocol, family packetFilterFamily) []string {
	switch protocol {
	case netpolicy.ProtocolTCP:
		return []string{"-p", "tcp"}
	case netpolicy.ProtocolUDP:
		return []string{"-p", "udp"}
	case netpolicy.ProtocolICMP:
		return []string{"-p", family.icmpProtocol}
	default:
		return nil
	}
}

func packetFilterTarget(action netpolicy.Action) string {
	if action == netpolicy.ActionAllow {
		return "ACCEPT"
	}
	return "DROP"
}
