package runner

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DemonGiggle/mirage/internal/spec"
)

func TestResolveCommandBinaryMentionsRootfsWhenPathLookupFails(t *testing.T) {
	sandboxEnv, err := buildSandboxEnv(nil, "/tmp/test-rootfs")
	if err != nil {
		t.Fatalf("buildSandboxEnv returned error: %v", err)
	}
	_, err = resolveCommandBinary("definitely-missing-command", "/tmp/test-rootfs", sandboxEnv)
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

func TestPlanNotesOfflinePolicy(t *testing.T) {
	notes := PlanNotes(spec.Config{
		RootFS: "/",
		NetworkPolicy: &spec.NetworkPolicy{
			Version:  1,
			Loopback: spec.LoopbackPolicy{Default: spec.PolicyAllow},
			Ingress:  spec.IngressPolicy{Default: spec.PolicyDeny, Rules: []spec.IngressRule{}},
			Egress:   spec.EgressPolicy{Default: spec.PolicyDeny, Rules: []spec.EgressRule{}},
		},
	})
	got := strings.Join(notes, "\n")
	if !strings.Contains(got, "network backend: isolated policy namespace (allow loopback)") {
		t.Fatalf("expected offline policy note, got %q", got)
	}
}

func TestPlanNotesNetworkPolicy(t *testing.T) {
	notes := PlanNotes(spec.Config{
		RootFS: "/",
		NetworkPolicy: &spec.NetworkPolicy{
			Version:  1,
			Loopback: spec.LoopbackPolicy{Default: spec.PolicyAllow},
			Ingress:  spec.IngressPolicy{Default: spec.PolicyDeny, Rules: []spec.IngressRule{}},
			Egress: spec.EgressPolicy{
				Default: spec.PolicyDeny,
				Rules: []spec.EgressRule{{
					Name:        "allow-api",
					Action:      spec.PolicyAllow,
					Destination: spec.NetworkSelector{IP: "203.0.113.10"},
					Protocol:    spec.ProtocolTCP,
					Ports:       []int{443},
				}},
			},
		},
	})
	got := strings.Join(notes, "\n")
	if !strings.Contains(got, "network backend: routed policy namespace (allow loopback, host NAT uplink)") {
		t.Fatalf("expected policy network note, got %q", got)
	}
}

func TestPlanNotesAllowAllPolicy(t *testing.T) {
	policy := spec.AllowAllNetworkPolicy()
	notes := PlanNotes(spec.Config{
		RootFS:        "/",
		NetworkPolicy: &policy,
	})
	got := strings.Join(notes, "\n")
	if !strings.Contains(got, "network backend: allow-all policy via host namespace passthrough") {
		t.Fatalf("expected allow-all note, got %q", got)
	}
}

func TestBuildUnshareArgsUsesNetNamespaceForOfflineNetworkPolicy(t *testing.T) {
	args, err := buildUnshareArgs(spec.Config{
		NetworkPolicy: &spec.NetworkPolicy{
			Version:  1,
			Loopback: spec.LoopbackPolicy{Default: spec.PolicyAllow},
			Ingress:  spec.IngressPolicy{Default: spec.PolicyDeny, Rules: []spec.IngressRule{}},
			Egress:   spec.EgressPolicy{Default: spec.PolicyDeny, Rules: []spec.EgressRule{}},
		},
	})
	if err != nil {
		t.Fatalf("buildUnshareArgs returned error: %v", err)
	}
	if !slicesContains(args, "--net") {
		t.Fatalf("expected policy backend to use a dedicated net namespace, got %#v", args)
	}
}

func TestBuildUnshareArgsSkipsNetNamespaceForAllowAllNetworkPolicy(t *testing.T) {
	policy := spec.AllowAllNetworkPolicy()
	args, err := buildUnshareArgs(spec.Config{
		NetworkPolicy: &policy,
	})
	if err != nil {
		t.Fatalf("buildUnshareArgs returned error: %v", err)
	}
	if slicesContains(args, "--net") {
		t.Fatalf("expected allow-all policy to avoid a dedicated net namespace, got %#v", args)
	}
}

func TestBuildUnshareArgsSupportsPolicyAllows(t *testing.T) {
	args, err := buildUnshareArgs(spec.Config{
		NetworkPolicy: &spec.NetworkPolicy{
			Version:  1,
			Loopback: spec.LoopbackPolicy{Default: spec.PolicyAllow},
			Ingress:  spec.IngressPolicy{Default: spec.PolicyDeny, Rules: []spec.IngressRule{}},
			Egress: spec.EgressPolicy{
				Default: spec.PolicyDeny,
				Rules: []spec.EgressRule{
					{
						Name:        "allow-api",
						Action:      spec.PolicyAllow,
						Destination: spec.NetworkSelector{IP: "203.0.113.10"},
						Protocol:    spec.ProtocolTCP,
						Ports:       []int{443},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("expected allow rule policy to succeed, got %v", err)
	}
	if !slices.Contains(args, "--net") {
		t.Fatalf("expected isolated policy to require --net, got %#v", args)
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

func TestCgroupLeafCleanupRemovesLeafBeforeEntry(t *testing.T) {
	leafPath := filepath.Join(t.TempDir(), "mirage-leaf")
	if err := os.Mkdir(leafPath, 0o755); err != nil {
		t.Fatalf("create leaf dir: %v", err)
	}

	selfInLeaf := false
	cleanup := cgroupLeafCleanup(leafPath, &selfInLeaf)
	cleanup()

	if _, err := os.Stat(leafPath); !os.IsNotExist(err) {
		t.Fatalf("expected cleanup to remove leaf before entry, got err=%v", err)
	}
}

func TestCgroupLeafCleanupSkipsRemovalAfterEntry(t *testing.T) {
	leafPath := filepath.Join(t.TempDir(), "mirage-leaf")
	if err := os.Mkdir(leafPath, 0o755); err != nil {
		t.Fatalf("create leaf dir: %v", err)
	}

	selfInLeaf := true
	cleanup := cgroupLeafCleanup(leafPath, &selfInLeaf)
	cleanup()

	if _, err := os.Stat(leafPath); err != nil {
		t.Fatalf("expected cleanup to leave leaf for systemd scope teardown, got %v", err)
	}
}

func slicesContains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func TestBuildSandboxEnvDoesNotInheritHostVariables(t *testing.T) {
	t.Setenv("SECRET_TOKEN", "host-secret")

	env, err := buildSandboxEnv([]string{"FOO=bar"}, "/sandbox-rootfs")
	if err != nil {
		t.Fatalf("buildSandboxEnv returned error: %v", err)
	}
	if envValue(env, "PATH", "") == "" {
		t.Fatal("expected managed sandbox PATH to be present")
	}
	if got := envValue(env, "HOME", ""); got != defaultSandboxHome {
		t.Fatalf("expected default HOME %q, got %q", defaultSandboxHome, got)
	}
	if got := envValue(env, "USER", ""); got != defaultSandboxUser {
		t.Fatalf("expected default USER %q, got %q", defaultSandboxUser, got)
	}
	if envValue(env, "SECRET_TOKEN", "") != "" {
		t.Fatal("did not expect host-only variable to be inherited into sandbox env")
	}
	if envValue(env, "FOO", "") != "bar" {
		t.Fatal("expected explicit sandbox env variable to be present")
	}
}

func TestBuildSandboxEnvSupportsPathOverride(t *testing.T) {
	env, err := buildSandboxEnv([]string{"PATH=/custom/bin", "HOME=/workspace", "USER=workspace-user", "TERM=xterm-256color"}, "/sandbox-rootfs")
	if err != nil {
		t.Fatalf("buildSandboxEnv returned error: %v", err)
	}

	if got := envValue(env, "PATH", ""); got != "/custom/bin" {
		t.Fatalf("expected PATH override to win, got %q", got)
	}
	if got := envValue(env, "HOME", ""); got != "/workspace" {
		t.Fatalf("expected HOME override to win, got %q", got)
	}
	if got := envValue(env, "USER", ""); got != "workspace-user" {
		t.Fatalf("expected USER override to win, got %q", got)
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

	sandboxEnv, err := buildSandboxEnv([]string{"PATH=" + dir}, "/tmp/test-rootfs")
	if err != nil {
		t.Fatalf("buildSandboxEnv returned error: %v", err)
	}
	resolved, err := resolveCommandBinary("demo", "/tmp/test-rootfs", sandboxEnv)
	if err != nil {
		t.Fatalf("resolveCommandBinary returned error: %v", err)
	}
	if resolved != binary {
		t.Fatalf("expected resolved path %q, got %q", binary, resolved)
	}
}

func TestIsLikelyPingBinary(t *testing.T) {
	for _, path := range []string{"/usr/bin/ping", "/bin/ping4", "/bin/ping6"} {
		if !isLikelyPingBinary(path) {
			t.Fatalf("expected %q to be treated as ping", path)
		}
	}
	if isLikelyPingBinary("/usr/bin/curl") {
		t.Fatal("did not expect non-ping binary to be treated as ping")
	}
}

func TestPingSocketProbesUsesDualStackForUnifiedPing(t *testing.T) {
	probes := pingSocketProbes("/usr/bin/ping")
	if len(probes) != 4 {
		t.Fatalf("expected unified ping to probe both IPv4 and IPv6 socket variants, got %#v", probes)
	}
}
