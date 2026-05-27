package runner

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DemonGiggle/mirage/internal/spec"
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
	_, err := resolveCommandBinary("definitely-missing-command", "/tmp/test-rootfs", buildSandboxEnv(nil, spec.RuntimeModeDirect))
	if err == nil {
		t.Fatal("expected missing command lookup to fail")
	}

	got := err.Error()
	for _, needle := range []string{
		`resolve command "definitely-missing-command" inside rootfs "/tmp/test-rootfs"`,
		`using sandbox PATH`,
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

func TestShouldStartTransientOpenClawGateway(t *testing.T) {
	cases := []struct {
		name    string
		command []string
		want    bool
	}{
		{
			name:    "plain onboard",
			command: []string{"openclaw", "onboard", "--non-interactive", "--accept-risk"},
			want:    true,
		},
		{
			name:    "absolute path onboard",
			command: []string{"/workspace/bin/openclaw", "onboard", "--non-interactive", "--accept-risk"},
			want:    true,
		},
		{
			name:    "skip health already requested",
			command: []string{"openclaw", "onboard", "--skip-health"},
			want:    false,
		},
		{
			name:    "remote mode",
			command: []string{"openclaw", "onboard", "--mode", "remote"},
			want:    false,
		},
		{
			name:    "remote url",
			command: []string{"openclaw", "onboard", "--remote-url", "ws://example"},
			want:    false,
		},
		{
			name:    "different command",
			command: []string{"openclaw", "gateway", "run"},
			want:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldStartTransientOpenClawGateway(tc.command); got != tc.want {
				t.Fatalf("shouldStartTransientOpenClawGateway(%q) = %v, want %v", tc.command, got, tc.want)
			}
		})
	}
}

func TestPlanNotesIsolatedNetworkWithoutObservationTool(t *testing.T) {
	t.Setenv("PATH", "")

	notes := PlanNotes(spec.Config{
		RootFS:      "/",
		NetworkMode: spec.NetworkIsolated,
	})
	got := strings.Join(notes, "\n")
	if !strings.Contains(got, "network backend: host namespace without observed policy enforcement (strace unavailable)") {
		t.Fatalf("expected degraded isolated-network note, got %q", got)
	}
}

func TestDelegatedScopeArgs(t *testing.T) {
	args := delegatedScopeArgs("mirage-sandbox-demo.scope", "mirage", "__cgroup-exec", "--memory", "128M", "--pids", "7", "--", "unshare", "--fork", "cmd")
	got := strings.Join(args, " ")
	for _, needle := range []string{
		"--user --scope --quiet --collect -p Delegate=yes --unit=mirage-sandbox-demo.scope",
		"-- mirage __cgroup-exec --memory 128M --pids 7 -- unshare --fork cmd",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected delegated scope args to contain %q, got %q", needle, got)
		}
	}
}

func TestWriteOptionalCgroupFileIgnoresMissingFile(t *testing.T) {
	err := writeOptionalCgroupFile(filepath.Join(t.TempDir(), "missing", "memory.swap.max"), "0\n")
	if err != nil {
		t.Fatalf("expected missing optional cgroup file to be ignored, got %v", err)
	}
}

func TestPlanNotesInitMode(t *testing.T) {
	notes := PlanNotes(spec.Config{
		RootFS:      "/",
		NetworkMode: spec.NetworkHost,
		RuntimeMode: spec.RuntimeModeInit,
		ScopeName:   "mirage-sandbox-demo.scope",
	})
	got := strings.Join(notes, "\n")
	for _, needle := range []string{
		"execution mode: guest init command becomes sandbox PID 1",
		"one sandbox = one isolated process tree rooted at guest init",
		"init runtime mounts: managed /dev tmpfs, read-only /sys, and delegated cgroup2",
		"cgroup v2: enforced via delegated systemd user-scope leaf cgroup (guest-unified-cgroup-v2)",
		"systemd user scope: mirage-sandbox-demo.scope",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected plan notes to contain %q, got %q", needle, got)
		}
	}
}

func TestRequiresCgroupScopeForInitMode(t *testing.T) {
	if !requiresCgroupScope(spec.Config{RuntimeMode: spec.RuntimeModeInit}) {
		t.Fatal("expected init mode to require a delegated cgroup scope")
	}
}

func TestPrepareGuestRunLayoutCreatesSystemdStateDirs(t *testing.T) {
	rootfs := t.TempDir()

	if err := prepareGuestRunLayout(rootfs); err != nil {
		t.Fatalf("prepareGuestRunLayout returned error: %v", err)
	}

	for _, want := range []string{
		filepath.Join(rootfs, "run", "lock"),
		filepath.Join(rootfs, "run", "systemd"),
		filepath.Join(rootfs, "run", "systemd", "system"),
	} {
		info, err := os.Stat(want)
		if err != nil {
			t.Fatalf("expected runtime directory %q to exist: %v", want, err)
		}
		if !info.IsDir() {
			t.Fatalf("expected runtime path %q to be a directory", want)
		}
	}
}

func TestHasEnvKey(t *testing.T) {
	items := []string{"PATH=/usr/bin", "container=mirage"}
	if !hasEnvKey(items, "container") {
		t.Fatal("expected container env key to be found")
	}
	if hasEnvKey(items, "HOME") {
		t.Fatal("did not expect missing env key to be reported present")
	}
}

func TestBuildSandboxEnvDoesNotInheritHostVariables(t *testing.T) {
	t.Setenv("SECRET_TOKEN", "host-secret")

	env := buildSandboxEnv([]string{"FOO=bar"}, spec.RuntimeModeDirect)
	if !hasEnvKey(env, "PATH") {
		t.Fatal("expected managed sandbox PATH to be present")
	}
	if hasEnvKey(env, "SECRET_TOKEN") {
		t.Fatal("did not expect host-only variable to be inherited into sandbox env")
	}
	if !hasEnvKey(env, "FOO") {
		t.Fatal("expected explicit sandbox env variable to be present")
	}
}

func TestBuildSandboxEnvSupportsPathOverrideAndInitContainer(t *testing.T) {
	env := buildSandboxEnv([]string{"PATH=/custom/bin", "TERM=xterm-256color"}, spec.RuntimeModeInit)

	if got := envValue(env, "PATH", ""); got != "/custom/bin" {
		t.Fatalf("expected PATH override to win, got %q", got)
	}
	pathCount := 0
	for _, item := range env {
		if strings.HasPrefix(item, "PATH=") {
			pathCount++
		}
	}
	if pathCount != 1 {
		t.Fatalf("expected a single PATH entry, got %d entries: %v", pathCount, env)
	}
	if !hasEnvKey(env, "container") {
		t.Fatal("expected init mode to inject container=mirage")
	}
	if got := envValue(env, "TERM", ""); got != "xterm-256color" {
		t.Fatalf("expected TERM to be preserved from explicit env, got %q", got)
	}
}

func TestResolveCommandBinaryUsesSandboxPath(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "demo")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write demo binary: %v", err)
	}

	resolved, err := resolveCommandBinary("demo", "/tmp/test-rootfs", buildSandboxEnv([]string{"PATH=" + dir}, spec.RuntimeModeDirect))
	if err != nil {
		t.Fatalf("resolveCommandBinary returned error: %v", err)
	}
	if resolved != binary {
		t.Fatalf("expected resolved path %q, got %q", binary, resolved)
	}
}

func TestBuildUnshareArgsAddsCgroupNamespaceForInitMode(t *testing.T) {
	args, err := buildUnshareArgs(spec.Config{
		NetworkMode: spec.NetworkHost,
		RuntimeMode: spec.RuntimeModeInit,
	})
	if err != nil {
		t.Fatalf("buildUnshareArgs returned error: %v", err)
	}
	if !strings.Contains(strings.Join(args, " "), "--cgroup") {
		t.Fatalf("expected init-mode unshare args to include --cgroup, got %#v", args)
	}
}
