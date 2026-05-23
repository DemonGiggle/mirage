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

func TestResolveCommandBinaryMentionsRootfsWhenPathLookupFails(t *testing.T) {
	t.Setenv("PATH", "/bin:/usr/bin")

	_, err := resolveCommandBinary("definitely-missing-command", "/tmp/test-rootfs")
	if err == nil {
		t.Fatal("expected missing command lookup to fail")
	}

	got := err.Error()
	for _, needle := range []string{
		`resolve command "definitely-missing-command" inside rootfs "/tmp/test-rootfs"`,
		`using the current PATH`,
		`install the executable in the rootfs, set PATH for the sandbox, or invoke it by absolute path inside the rootfs`,
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected error to contain %q, got %q", needle, got)
		}
	}
}

func TestPrepareBindTargetRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "link")
	if err := os.Symlink("/tmp/outside", target); err != nil {
		t.Fatalf("create symlink target: %v", err)
	}

	err := prepareBindTarget(dir, target, false)
	if err == nil || !strings.Contains(err.Error(), "target exists as a symlink") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestPrepareBindTargetRequiresExistingTargetUnderHostRoot(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "missing")

	err := prepareBindTarget("/", target, false)
	if err == nil || !strings.Contains(err.Error(), "target does not exist under host rootfs") {
		t.Fatalf("expected host-root missing target rejection, got %v", err)
	}
}

func TestParseBindMount(t *testing.T) {
	t.Run("read-only", func(t *testing.T) {
		got, err := parseBindMount("/host/data:/workspace/data", true)
		if err != nil {
			t.Fatalf("parseBindMount returned error: %v", err)
		}
		if got.Source != "/host/data" || got.Target != "/workspace/data" || !got.ReadOnly {
			t.Fatalf("unexpected bind mount: %#v", got)
		}
	})

	t.Run("rejects invalid entries", func(t *testing.T) {
		cases := []string{
			"missing-colon",
			"relative-source:/workspace",
			"/host:relative-target",
			"/host:/",
		}
		for _, entry := range cases {
			if _, err := parseBindMount(entry, false); err == nil {
				t.Fatalf("expected parseBindMount(%q) to fail", entry)
			}
		}
	})
}

func TestPrepareBindTargetRejectsTypeMismatch(t *testing.T) {
	dir := t.TempDir()

	fileTarget := filepath.Join(dir, "file-target")
	if err := os.WriteFile(fileTarget, []byte("data"), 0o644); err != nil {
		t.Fatalf("write file target: %v", err)
	}
	if err := prepareBindTarget(dir, fileTarget, true); err == nil || !strings.Contains(err.Error(), "target exists as a file") {
		t.Fatalf("expected file/dir mismatch error, got %v", err)
	}

	dirTarget := filepath.Join(dir, "dir-target")
	if err := os.MkdirAll(dirTarget, 0o755); err != nil {
		t.Fatalf("mkdir dir target: %v", err)
	}
	if err := prepareBindTarget(dir, dirTarget, false); err == nil || !strings.Contains(err.Error(), "target exists as a directory") {
		t.Fatalf("expected dir/file mismatch error, got %v", err)
	}
}

func TestEnsureObservedNetworkToolAvailable(t *testing.T) {
	t.Setenv("PATH", "")

	err := EnsureObservedNetworkToolAvailable()
	if err == nil {
		t.Fatal("expected missing strace check to fail")
	}
	if !strings.Contains(err.Error(), "observed isolated networking requires strace on PATH") {
		t.Fatalf("unexpected error: %v", err)
	}
}
