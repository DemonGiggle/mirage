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
const linuxCapabilityNETADMIN = 12

var routedNetworkBase = netip.MustParseAddr("198.19.0.0")
var routedHostCommand = exec.Command
var readIPv4ForwardingFile = os.ReadFile
var readProcessStatusFile = os.ReadFile

var errRoutedNetworkSetupAborted = errors.New("routed network setup aborted before readiness")

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
		if errors.Is(err, io.EOF) {
			return errRoutedNetworkSetupAborted
		}
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
		cmd := policyNetworkCommand(command.Name, command.Args...)
		cmd.Env = append(cmd.Environ(), "XTABLES_LOCKFILE=/tmp/xtables.lock")
		output, err := cmd.CombinedOutput()
		if err != nil {
			if command.Name == "ip6tables" && strings.Contains(string(output), "ip6tables table `filter': Table does not exist") {
				continue
			}
			return routedCommandError("apply networkPolicy backend command", command.Name, command.Args, err, output, "networkPolicy enforcement")
		}
	}
	return nil
}

func routedCommandError(prefix string, name string, args []string, err error, output []byte, privilegeContext string) error {
	suffix := ""
	if trimmed := strings.TrimSpace(string(output)); trimmed != "" {
		suffix += ": " + trimmed
	}
	if hint := routedCommandFixHint(name, err, output, privilegeContext); hint != "" {
		suffix += "; " + hint
	}
	return fmt.Errorf("%s %s %v: %w%s", prefix, name, args, err, suffix)
}

func routedCommandFixHint(name string, err error, output []byte, privilegeContext string) string {
	var execErr *exec.Error
	if errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound) {
		switch name {
		case "ip":
			return "install iproute2 (or the distro package that provides `ip`) and ensure `ip` is on PATH before retrying"
		case "iptables", "ip6tables":
			return fmt.Sprintf("install the host package that provides `%s` and ensure it is on PATH before using networkPolicy enforcement", name)
		}
	}

	combined := err.Error()
	if trimmed := strings.TrimSpace(string(output)); trimmed != "" {
		combined += "\n" + trimmed
	}
	lower := strings.ToLower(combined)
	if strings.Contains(lower, "permission denied") || strings.Contains(lower, "operation not permitted") || strings.Contains(lower, "you must be root") {
		return fmt.Sprintf("run mirage with sudo or grant CAP_NET_ADMIN before using %s", privilegeContext)
	}
	return ""
}

func setupRoutedNetworkHost(pid int, cfg routedNetworkConfig) (func(), error) {
	if err := requireRoutedNetworkHostPrerequisites(); err != nil {
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
			return routedCommandError("run", name, args, err, output, "routed networking")
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

func requireRoutedNetworkHostPrerequisites() error {
	if err := requireHostNetworkAdmin(); err != nil {
		return err
	}
	if err := requireIPv4Forwarding(); err != nil {
		return err
	}
	return nil
}

func requireHostNetworkAdmin() error {
	data, err := readProcessStatusFile("/proc/self/status")
	if err != nil {
		return fmt.Errorf("read /proc/self/status: %w", err)
	}

	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "CapEff:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "CapEff:"))
		if raw == "" {
			break
		}
		mask, err := strconv.ParseUint(raw, 16, 64)
		if err != nil {
			return fmt.Errorf("parse CapEff from /proc/self/status: %w", err)
		}
		if mask&(uint64(1)<<linuxCapabilityNETADMIN) == 0 {
			return errors.New("routed network backend requires CAP_NET_ADMIN on the host; run mirage with sudo or grant the capability with `sudo setcap cap_net_admin+ep /path/to/mirage` before using routed egress policies")
		}
		return nil
	}

	return errors.New("read /proc/self/status: missing CapEff entry; ensure /proc is mounted and readable on the host before using routed egress policies")
}

func requireIPv4Forwarding() error {
	data, err := readIPv4ForwardingFile("/proc/sys/net/ipv4/ip_forward")
	if err != nil {
		return fmt.Errorf("read /proc/sys/net/ipv4/ip_forward: %w; ensure /proc/sys is mounted and readable on the host before using routed networking", err)
	}
	if strings.TrimSpace(string(data)) != "1" {
		return errors.New(
			"routed network backend requires net.ipv4.ip_forward=1 on the host.\n" +
				"Fix it with:\n" +
				"- Enable it immediately: `sudo sysctl -w net.ipv4.ip_forward=1`\n" +
				"- Persist it across reboots: create `/etc/sysctl.d/99-mirage.conf` with:\n" +
				"  `net.ipv4.ip_forward = 1`\n" +
				"- Reload sysctl settings: `sudo sysctl --system`",
		)
	}
	return nil
}
