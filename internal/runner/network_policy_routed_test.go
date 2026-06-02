package runner

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DemonGiggle/mirage/internal/netpolicy"
)

func TestConfigureRoutedPolicyNetworkBackendProgramsInterfaceAndFilters(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "commands.log")
	restorePolicyCommand := policyNetworkCommand
	policyNetworkCommand = helperCommand(t, logPath)
	t.Cleanup(func() {
		policyNetworkCommand = restorePolicyCommand
	})

	encoded, err := encodeNetworkPolicyBackend(testAllowBeforeDenyPolicy(t))
	if err != nil {
		t.Fatalf("encodeNetworkPolicyBackend returned error: %v", err)
	}
	if err := configureRoutedPolicyNetworkBackend(encoded, "mrgg0", "198.19.0.2/30", "198.19.0.1"); err != nil {
		t.Fatalf("configureRoutedPolicyNetworkBackend returned error: %v", err)
	}

	got := readHelperCommandLog(t, logPath)
	for _, needle := range []string{
		"ip link set lo up",
		"ip link set mrgg0 up",
		"ip addr add 198.19.0.2/30 dev mrgg0",
		"ip route replace default via 198.19.0.1 dev mrgg0",
		"iptables -w -A OUTPUT -d 192.168.0.1 -j ACCEPT",
		"iptables -w -A OUTPUT -d 192.168.0.0/16 -j DROP",
		"iptables -w -I INPUT 1 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT",
		"ip6tables -w -I INPUT 1 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected routed backend command %q in %s", needle, got)
		}
	}
}

func TestSetupRoutedNetworkHostProgramsVethAndForwarding(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "commands.log")
	restoreHostCommand := routedHostCommand
	restoreReadForwarding := readIPv4ForwardingFile
	restoreStatusReader := readProcessStatusFile
	routedHostCommand = helperCommand(t, logPath)
	readIPv4ForwardingFile = func(string) ([]byte, error) {
		return []byte("1\n"), nil
	}
	readProcessStatusFile = func(string) ([]byte, error) {
		return []byte("Name:\ttest\nCapEff:\t0000000000001000\n"), nil
	}
	t.Cleanup(func() {
		routedHostCommand = restoreHostCommand
		readIPv4ForwardingFile = restoreReadForwarding
		readProcessStatusFile = restoreStatusReader
	})

	cfg := routedNetworkConfig{
		HostIfName:  "mrgh0",
		GuestIfName: "mrgg0",
		HostAddress: "198.19.0.1",
		HostCIDR:    "198.19.0.1/30",
		GuestCIDR:   "198.19.0.2/30",
		SubnetCIDR:  "198.19.0.0/30",
	}

	cleanup, err := setupRoutedNetworkHost(4242, cfg)
	if err != nil {
		t.Fatalf("setupRoutedNetworkHost returned error: %v", err)
	}
	cleanup()

	got := readHelperCommandLog(t, logPath)
	for _, needle := range []string{
		"ip link add mrgh0 type veth peer name mrgg0",
		"ip addr add 198.19.0.1/30 dev mrgh0",
		"ip link set mrgh0 up",
		"ip link set mrgg0 netns 4242",
		"iptables -w -A FORWARD -i mrgh0 -s 198.19.0.0/30 -j ACCEPT",
		"iptables -w -A FORWARD -o mrgh0 -d 198.19.0.0/30 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT",
		"iptables -w -t nat -A POSTROUTING -s 198.19.0.0/30 ! -d 198.19.0.0/30 -j MASQUERADE",
		"iptables -w -t nat -D POSTROUTING -s 198.19.0.0/30 ! -d 198.19.0.0/30 -j MASQUERADE",
		"iptables -w -D FORWARD -o mrgh0 -d 198.19.0.0/30 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT",
		"iptables -w -D FORWARD -i mrgh0 -s 198.19.0.0/30 -j ACCEPT",
		"ip link del mrgh0",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected routed host command %q in %s", needle, got)
		}
	}
}

func TestRequireHostNetworkAdminRejectsMissingCapability(t *testing.T) {
	restoreStatusReader := readProcessStatusFile
	readProcessStatusFile = func(string) ([]byte, error) {
		return []byte("Name:\ttest\nCapEff:\t0000000000000000\n"), nil
	}
	t.Cleanup(func() {
		readProcessStatusFile = restoreStatusReader
	})

	err := requireHostNetworkAdmin()
	if err == nil || !strings.Contains(err.Error(), "requires CAP_NET_ADMIN on the host") {
		t.Fatalf("expected CAP_NET_ADMIN error, got %v", err)
	}
	if !strings.Contains(err.Error(), "setcap cap_net_admin+ep") {
		t.Fatalf("expected capability remediation hint, got %v", err)
	}
}

func TestRequireHostNetworkAdminAcceptsCapability(t *testing.T) {
	restoreStatusReader := readProcessStatusFile
	readProcessStatusFile = func(string) ([]byte, error) {
		return []byte("Name:\ttest\nCapEff:\t0000000000001000\n"), nil
	}
	t.Cleanup(func() {
		readProcessStatusFile = restoreStatusReader
	})

	if err := requireHostNetworkAdmin(); err != nil {
		t.Fatalf("requireHostNetworkAdmin returned error: %v", err)
	}
}

func TestRequireIPv4ForwardingRejectsDisabledForwardingWithHint(t *testing.T) {
	restoreReadForwarding := readIPv4ForwardingFile
	readIPv4ForwardingFile = func(string) ([]byte, error) {
		return []byte("0\n"), nil
	}
	t.Cleanup(func() {
		readIPv4ForwardingFile = restoreReadForwarding
	})

	err := requireIPv4Forwarding()
	if err == nil || !strings.Contains(err.Error(), "net.ipv4.ip_forward=1") {
		t.Fatalf("expected ip_forward error, got %v", err)
	}
	if !strings.Contains(err.Error(), "sudo sysctl -w net.ipv4.ip_forward=1") {
		t.Fatalf("expected sysctl remediation hint, got %v", err)
	}
}

func TestSetupRoutedNetworkHostSuggestsInstallingIPCommand(t *testing.T) {
	restoreHostCommand := routedHostCommand
	restoreReadForwarding := readIPv4ForwardingFile
	restoreStatusReader := readProcessStatusFile
	routedHostCommand = func(string, ...string) *exec.Cmd {
		return exec.Command("definitely-missing-binary")
	}
	readIPv4ForwardingFile = func(string) ([]byte, error) {
		return []byte("1\n"), nil
	}
	readProcessStatusFile = func(string) ([]byte, error) {
		return []byte("Name:\ttest\nCapEff:\t0000000000001000\n"), nil
	}
	t.Cleanup(func() {
		routedHostCommand = restoreHostCommand
		readIPv4ForwardingFile = restoreReadForwarding
		readProcessStatusFile = restoreStatusReader
	})

	_, err := setupRoutedNetworkHost(4242, routedNetworkConfig{
		HostIfName:  "mrgh0",
		GuestIfName: "mrgg0",
		HostCIDR:    "198.19.0.1/30",
		SubnetCIDR:  "198.19.0.0/30",
	})
	if err == nil || !strings.Contains(err.Error(), "install iproute2") {
		t.Fatalf("expected iproute2 remediation hint, got %v", err)
	}
}

func TestConfigureRoutedPolicyNetworkBackendSuggestsPrivilegeFix(t *testing.T) {
	restorePolicyCommand := policyNetworkCommand
	logPath := filepath.Join(t.TempDir(), "commands.log")
	policyNetworkCommand = func(name string, args ...string) *exec.Cmd {
		return helperCommandWithEnv(t, map[string]string{
			"MIRAGE_TEST_COMMAND_LOG": logPath,
			"MIRAGE_HELPER_STDERR":    "iptables v1.8.10 (nf_tables): Could not fetch rule set generation id: Permission denied (you must be root)",
			"MIRAGE_HELPER_EXIT":      "1",
		})(name, args...)
	}
	t.Cleanup(func() {
		policyNetworkCommand = restorePolicyCommand
	})

	encoded, err := encodeNetworkPolicyBackend(testAllowBeforeDenyPolicy(t))
	if err != nil {
		t.Fatalf("encodeNetworkPolicyBackend returned error: %v", err)
	}
	err = configureRoutedPolicyNetworkBackend(encoded, "mrgg0", "198.19.0.2/30", "198.19.0.1")
	if err == nil || !strings.Contains(err.Error(), "run mirage with sudo or grant CAP_NET_ADMIN") {
		t.Fatalf("expected privilege remediation hint, got %v", err)
	}
}

func TestConfigureRoutedPolicyNetworkBackendIgnoresMissingIP6TablesConntrackMatch(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "commands.log")
	restorePolicyCommand := policyNetworkCommand
	policyNetworkCommand = func(name string, args ...string) *exec.Cmd {
		if name == "ip6tables" && strings.Contains(strings.Join(args, " "), "conntrack") {
			return helperCommandWithEnv(t, map[string]string{
				"MIRAGE_TEST_COMMAND_LOG": logPath,
				"MIRAGE_HELPER_STDERR":    "ip6tables v1.8.4 (legacy): Couldn't load match `conntrack':No such file or directory",
				"MIRAGE_HELPER_EXIT":      "1",
			})(name, args...)
		}
		return helperCommand(t, logPath)(name, args...)
	}
	t.Cleanup(func() {
		policyNetworkCommand = restorePolicyCommand
	})

	encoded, err := encodeNetworkPolicyBackend(testAllowBeforeDenyPolicy(t))
	if err != nil {
		t.Fatalf("encodeNetworkPolicyBackend returned error: %v", err)
	}
	if err := configureRoutedPolicyNetworkBackend(encoded, "mrgg0", "198.19.0.2/30", "198.19.0.1"); err != nil {
		t.Fatalf("expected missing ip6tables conntrack match to be ignored, got %v", err)
	}
}

func TestWaitForRoutedNetworkReadyTreatsEOFAsAbortedSetup(t *testing.T) {
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = readEnd.Close()
		_ = writeEnd.Close()
	})
	_ = writeEnd.Close()

	err = waitForRoutedNetworkReady(int(readEnd.Fd()))
	if !errors.Is(err, errRoutedNetworkSetupAborted) {
		t.Fatalf("expected aborted setup sentinel, got %v", err)
	}
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	logPath := os.Getenv("MIRAGE_TEST_COMMAND_LOG")
	args := os.Args
	sentinel := -1
	for index, value := range args {
		if value == "--" {
			sentinel = index
			break
		}
	}
	if sentinel < 0 || sentinel+1 >= len(args) {
		os.Exit(2)
	}
	entry := strings.Join(args[sentinel+1:], " ") + "\n"
	if logPath != "" {
		file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			os.Exit(3)
		}
		_, _ = file.WriteString(entry)
		_ = file.Close()
	}
	if stderr := os.Getenv("MIRAGE_HELPER_STDERR"); stderr != "" {
		_, _ = os.Stderr.WriteString(stderr)
	}
	if exitCode := os.Getenv("MIRAGE_HELPER_EXIT"); exitCode != "" && exitCode != "0" {
		os.Exit(1)
	}
	os.Exit(0)
}

func helperCommand(t *testing.T, logPath string) func(string, ...string) *exec.Cmd {
	t.Helper()
	return func(name string, args ...string) *exec.Cmd {
		return helperCommandWithEnv(t, map[string]string{
			"MIRAGE_TEST_COMMAND_LOG": logPath,
		})(name, args...)
	}
}

func helperCommandWithEnv(t *testing.T, extraEnv map[string]string) func(string, ...string) *exec.Cmd {
	t.Helper()
	return func(name string, args ...string) *exec.Cmd {
		commandArgs := append([]string{"-test.run=TestHelperProcess", "--", name}, args...)
		cmd := exec.Command(os.Args[0], commandArgs...)
		env := append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
		for key, value := range extraEnv {
			env = append(env, key+"="+value)
		}
		cmd.Env = env
		return cmd
	}
}

func readHelperCommandLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read helper command log: %v", err)
	}
	return string(data)
}

func testAllowBeforeDenyPolicy(t *testing.T) netpolicy.Policy {
	t.Helper()
	return netpolicy.Policy{
		Loopback: netpolicy.ActionAllow,
		Ingress: netpolicy.DirectionPolicy{
			Zone:    netpolicy.ZoneIngress,
			Default: netpolicy.ActionDeny,
		},
		Egress: netpolicy.DirectionPolicy{
			Zone:    netpolicy.ZoneEgress,
			Default: netpolicy.ActionDeny,
			Rules: []netpolicy.Rule{
				{
					Order:    0,
					Name:     "allow-gateway",
					Action:   netpolicy.ActionAllow,
					Selector: netpolicy.Selector{Kind: netpolicy.SelectorIP, IP: mustAddr(t, "192.168.0.1")},
					Protocol: netpolicy.ProtocolAny,
				},
				{
					Order:    1,
					Name:     "deny-lan",
					Action:   netpolicy.ActionDeny,
					Selector: netpolicy.Selector{Kind: netpolicy.SelectorCIDR, Prefix: mustPrefix(t, "192.168.0.0/16")},
					Protocol: netpolicy.ProtocolAny,
				},
			},
		},
	}
}
