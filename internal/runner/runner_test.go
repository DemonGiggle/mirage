package runner

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractConnectAddressIPv4(t *testing.T) {
	line := `123 connect(3, {sa_family=AF_INET, sin_port=htons(443), sin_addr=inet_addr("203.0.113.10")}, 16) = -1 ENETUNREACH (Network is unreachable)`
	got, ok := extractConnectAddress(line)
	if !ok {
		t.Fatal("expected IPv4 connect line to parse")
	}
	if got != "203.0.113.10:443" {
		t.Fatalf("unexpected address: %q", got)
	}
}

func TestExtractConnectAddressIPv6(t *testing.T) {
	line := `123 connect(3, {sa_family=AF_INET6, sin6_port=htons(443), sin6_flowinfo=htonl(0), inet_pton(AF_INET6, "2001:db8::1", &sin6_addr), sin6_scope_id=0}, 28) = -1 ENETUNREACH (Network is unreachable)`
	got, ok := extractConnectAddress(line)
	if !ok {
		t.Fatal("expected IPv6 connect line to parse")
	}
	if got != "[2001:db8::1]:443" {
		t.Fatalf("unexpected address: %q", got)
	}
}

func TestIsAllowedAddress(t *testing.T) {
	policy := networkPolicy{
		ResolvedAllowHosts: []hostPort{{Host: "203.0.113.10", Port: 443}},
		AllowPorts:         []int{53},
	}
	if !isAllowedAddress("203.0.113.10:443", policy) {
		t.Fatal("expected resolved allow-host match")
	}
	if !isAllowedAddress("198.51.100.7:53", policy) {
		t.Fatal("expected allow-port match")
	}
	if isAllowedAddress("198.51.100.7:80", policy) {
		t.Fatal("did not expect undeclared address to be allowed")
	}
}

func TestPersistWarnRecord(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("MIRAGE_STATE_DIR", stateDir)

	policy := networkPolicy{Mode: "isolated", WarnNet: true}
	attempts := []connectAttempt{{Address: "203.0.113.10:443", Allowed: false, Raw: "connect(...)"}}
	if err := persistWarnRecord(policy, []string{"probe"}, attempts); err != nil {
		t.Fatalf("persistWarnRecord returned error: %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(stateDir, "warn"))
	if err != nil {
		t.Fatalf("read warn directory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 warn record, got %d", len(entries))
	}
}

func TestResolveAllowHosts(t *testing.T) {
	resolved, err := resolveAllowHosts([]string{"127.0.0.1:443", net.JoinHostPort("::1", "443")})
	if err != nil {
		t.Fatalf("resolveAllowHosts returned error: %v", err)
	}
	got := strings.Join(resolved, ",")
	for _, needle := range []string{"127.0.0.1:443", "[::1]:443"} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected resolved list to contain %q, got %q", needle, got)
		}
	}
}
