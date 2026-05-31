package runner

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const routedNetworkPrefixBits = 30

var routedNetworkBase = netip.MustParseAddr("198.19.0.0")
var routedHostCommand = exec.Command
var readIPv4ForwardingFile = os.ReadFile

type routedNetworkConfig struct {
	HostIfName  string
	GuestIfName string
	HostAddress string
	HostCIDR    string
	GuestCIDR   string
	SubnetCIDR  string
}

func newRoutedNetworkConfig() (routedNetworkConfig, error) {
	if override := strings.TrimSpace(os.Getenv("MIRAGE_ROUTED_NETWORK_TOKEN")); override != "" {
		token, err := strconv.ParseUint(override, 10, 16)
		if err != nil {
			return routedNetworkConfig{}, fmt.Errorf("parse MIRAGE_ROUTED_NETWORK_TOKEN: %w", err)
		}
		return newRoutedNetworkConfigForToken(uint16(token))
	}

	var raw [2]byte
	if _, err := io.ReadFull(rand.Reader, raw[:]); err != nil {
		fallback := uint16(time.Now().UnixNano())
		binary.BigEndian.PutUint16(raw[:], fallback)
	}
	token := binary.BigEndian.Uint16(raw[:]) & 0x3fff
	return newRoutedNetworkConfigForToken(token)
}

func newRoutedNetworkConfigForToken(token uint16) (routedNetworkConfig, error) {
	subnetBase, err := routedSubnetBaseAddress(token)
	if err != nil {
		return routedNetworkConfig{}, err
	}
	hostAddr, err := routedSubnetAddress(subnetBase, 1)
	if err != nil {
		return routedNetworkConfig{}, err
	}
	guestAddr, err := routedSubnetAddress(subnetBase, 2)
	if err != nil {
		return routedNetworkConfig{}, err
	}
	subnetPrefix := netip.PrefixFrom(subnetBase, routedNetworkPrefixBits)
	return routedNetworkConfig{
		HostIfName:  fmt.Sprintf("mrgh%x", token),
		GuestIfName: fmt.Sprintf("mrgg%x", token),
		HostAddress: hostAddr.String(),
		HostCIDR:    netip.PrefixFrom(hostAddr, routedNetworkPrefixBits).String(),
		GuestCIDR:   netip.PrefixFrom(guestAddr, routedNetworkPrefixBits).String(),
		SubnetCIDR:  subnetPrefix.String(),
	}, nil
}

func routedSubnetBaseAddress(index uint16) (netip.Addr, error) {
	return routedSubnetAddress(routedNetworkBase, uint32(index)*4)
}

func routedSubnetAddress(base netip.Addr, offset uint32) (netip.Addr, error) {
	if !base.Is4() {
		return netip.Addr{}, fmt.Errorf("routed network base %q is not IPv4", base)
	}
	raw4 := base.As4()
	raw := binary.BigEndian.Uint32(raw4[:])
	var next [4]byte
	binary.BigEndian.PutUint32(next[:], raw+offset)
	addr, ok := netip.AddrFromSlice(next[:])
	if !ok {
		return netip.Addr{}, errors.New("construct routed network address")
	}
	return addr.Unmap(), nil
}

func waitForRoutedNetworkReady(fd int) error {
	if fd < 0 {
		return fmt.Errorf("routed network backend requires a readiness fd")
	}
	file := os.NewFile(uintptr(fd), "mirage-routed-ready")
	if file == nil {
		return fmt.Errorf("routed network backend could not open readiness fd %d", fd)
	}
	defer closeQuietly(file)

	var signal [1]byte
	if _, err := file.Read(signal[:]); err != nil {
		return fmt.Errorf("wait for routed network readiness: %w", err)
	}
	return nil
}

func configureRoutedPolicyNetworkBackend(encodedPolicy string, ifaceName string, guestCIDR string, gateway string) error {
	if ifaceName == "" || guestCIDR == "" || gateway == "" {
		return fmt.Errorf("routed network backend requires interface, address, and gateway configuration")
	}
	policy, err := decodeNetworkPolicyBackend(encodedPolicy)
	if err != nil {
		return err
	}
	commands, err := buildPolicyNetworkCommands(policy)
	if err != nil {
		return err
	}
	commands = append([]packetFilterCommand{
		{Name: "ip", Args: []string{"link", "set", "lo", "up"}},
		{Name: "ip", Args: []string{"link", "set", ifaceName, "up"}},
		{Name: "ip", Args: []string{"addr", "add", guestCIDR, "dev", ifaceName}},
		{Name: "ip", Args: []string{"route", "replace", "default", "via", gateway, "dev", ifaceName}},
	}, commands...)
	commands = append(commands,
		packetFilterCommand{Name: "iptables", Args: []string{"-w", "-I", "INPUT", "1", "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"}},
		packetFilterCommand{Name: "ip6tables", Args: []string{"-w", "-I", "INPUT", "1", "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"}},
	)
	return runPacketFilterCommands(commands)
}

func configurePolicyNetworkBackend(encodedPolicy string) error {
	policy, err := decodeNetworkPolicyBackend(encodedPolicy)
	if err != nil {
		return err
	}
	commands, err := buildPolicyNetworkCommands(policy)
	if err != nil {
		return err
	}
	return runPacketFilterCommands(commands)
}

func runPacketFilterCommands(commands []packetFilterCommand) error {
	for _, command := range commands {
		output, err := policyNetworkCommand(command.Name, command.Args...).CombinedOutput()
		if err != nil {
			if trimmed := strings.TrimSpace(string(output)); trimmed != "" {
				return fmt.Errorf("apply networkPolicy backend command %s %v: %w: %s", command.Name, command.Args, err, trimmed)
			}
			return fmt.Errorf("apply networkPolicy backend command %s %v: %w", command.Name, command.Args, err)
		}
	}
	return nil
}

func setupRoutedNetworkHost(pid int, cfg routedNetworkConfig) (func(), error) {
	if err := requireIPv4Forwarding(); err != nil {
		return nil, err
	}

	var cleanup []func()
	rollback := func() {
		for index := len(cleanup) - 1; index >= 0; index-- {
			cleanup[index]()
		}
	}

	run := func(name string, args ...string) error {
		output, err := routedHostCommand(name, args...).CombinedOutput()
		if err != nil {
			if trimmed := strings.TrimSpace(string(output)); trimmed != "" {
				return fmt.Errorf("run %s %v: %w: %s", name, args, err, trimmed)
			}
			return fmt.Errorf("run %s %v: %w", name, args, err)
		}
		return nil
	}

	if err := run("ip", "link", "add", cfg.HostIfName, "type", "veth", "peer", "name", cfg.GuestIfName); err != nil {
		return nil, err
	}
	cleanup = append(cleanup, func() {
		_ = routedHostCommand("ip", "link", "del", cfg.HostIfName).Run()
	})

	if err := run("ip", "addr", "add", cfg.HostCIDR, "dev", cfg.HostIfName); err != nil {
		rollback()
		return nil, err
	}
	if err := run("ip", "link", "set", cfg.HostIfName, "up"); err != nil {
		rollback()
		return nil, err
	}
	if err := run("ip", "link", "set", cfg.GuestIfName, "netns", strconv.Itoa(pid)); err != nil {
		rollback()
		return nil, err
	}

	forwardOut := []string{"-w", "-A", "FORWARD", "-i", cfg.HostIfName, "-s", cfg.SubnetCIDR, "-j", "ACCEPT"}
	if err := run("iptables", forwardOut...); err != nil {
		rollback()
		return nil, err
	}
	cleanup = append(cleanup, func() {
		_ = routedHostCommand("iptables", append([]string{"-w", "-D"}, forwardOut[2:]...)...).Run()
	})

	forwardBack := []string{"-w", "-A", "FORWARD", "-o", cfg.HostIfName, "-d", cfg.SubnetCIDR, "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"}
	if err := run("iptables", forwardBack...); err != nil {
		rollback()
		return nil, err
	}
	cleanup = append(cleanup, func() {
		_ = routedHostCommand("iptables", append([]string{"-w", "-D"}, forwardBack[2:]...)...).Run()
	})

	masquerade := []string{"-w", "-t", "nat", "-A", "POSTROUTING", "-s", cfg.SubnetCIDR, "!", "-d", cfg.SubnetCIDR, "-j", "MASQUERADE"}
	if err := run("iptables", masquerade...); err != nil {
		rollback()
		return nil, err
	}
	cleanup = append(cleanup, func() {
		_ = routedHostCommand("iptables", append([]string{"-w", "-t", "nat", "-D"}, masquerade[4:]...)...).Run()
	})

	return rollback, nil
}

func requireIPv4Forwarding() error {
	data, err := readIPv4ForwardingFile("/proc/sys/net/ipv4/ip_forward")
	if err != nil {
		return fmt.Errorf("read /proc/sys/net/ipv4/ip_forward: %w", err)
	}
	if strings.TrimSpace(string(data)) != "1" {
		return fmt.Errorf("routed network backend requires net.ipv4.ip_forward=1 on the host")
	}
	return nil
}
