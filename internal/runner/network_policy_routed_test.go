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
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		os.Exit(3)
	}
	_, _ = file.WriteString(entry)
	_ = file.Close()
	os.Exit(0)
}

func helperCommand(t *testing.T, logPath string) func(string, ...string) *exec.Cmd {
	t.Helper()
	return func(name string, args ...string) *exec.Cmd {
		commandArgs := append([]string{"-test.run=TestHelperProcess", "--", name}, args...)
		cmd := exec.Command(os.Args[0], commandArgs...)
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"MIRAGE_TEST_COMMAND_LOG="+logPath,
		)
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
