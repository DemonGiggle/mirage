package e2e

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Verifies that a workload can spawn child processes without leaving the same
// sandbox-owned process tree.
func TestProbeSpawnChildStaysInSandboxTree(t *testing.T) {
	requireNamespaceBackend(t)

	repoRoot := projectRoot(t)
	probePath := buildProbe(t, repoRoot, "./cmd/probe-spawn-child")

	output, err := runMirage(t, repoRoot,
		"run",
		"--rootfs", "/",
		"--net", "host",
		"--",
		probePath,
	)
	if err != nil {
		t.Fatalf("probe-spawn-child failed: %v\noutput:\n%s", err, output)
	}

	if !strings.Contains(output, "parent pid=1") {
		t.Fatalf("expected sandbox parent pid 1, got:\n%s", output)
	}
	if !strings.Contains(output, "child pid=") || !strings.Contains(output, "ppid=1") {
		t.Fatalf("expected child process to stay under sandbox init, got:\n%s", output)
	}
}

// Verifies that file reads succeed for content inside the sandbox rootfs and
// fail for host paths outside the exposed filesystem view.
func TestProbeFileReadRespectsRootfsBoundary(t *testing.T) {
	requireNamespaceBackend(t)

	repoRoot := projectRoot(t)
	rootfs := t.TempDir()
	buildProbeIntoRootfs(t, repoRoot, "./cmd/probe-file-read", rootfs, "probe-file-read")

	insidePath := filepath.Join(rootfs, "inside.txt")
	if err := os.WriteFile(insidePath, []byte("inside"), 0o644); err != nil {
		t.Fatalf("write inside fixture: %v", err)
	}

	hostSecretDir := t.TempDir()
	hostSecret := filepath.Join(hostSecretDir, "secret.txt")
	if err := os.WriteFile(hostSecret, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write host secret fixture: %v", err)
	}

	output, err := runMirage(t, repoRoot,
		"run",
		"--rootfs", rootfs,
		"--net", "host",
		"--",
		"/probe-file-read",
		"/inside.txt",
	)
	if err != nil {
		t.Fatalf("expected in-rootfs read to succeed: %v\noutput:\n%s", err, output)
	}
	if !strings.Contains(output, "read-ok path=/inside.txt") {
		t.Fatalf("unexpected success output:\n%s", output)
	}

	output, err = runMirage(t, repoRoot,
		"run",
		"--rootfs", rootfs,
		"--net", "host",
		"--",
		"/probe-file-read",
		hostSecret,
	)
	if err == nil {
		t.Fatalf("expected host path read to fail, got output:\n%s", output)
	}
	if !strings.Contains(output, "read-failed") {
		t.Fatalf("expected read-failed output, got:\n%s", output)
	}
}

// Verifies that file writes stay confined to the sandbox rootfs and do not
// create or modify files on the host outside that boundary.
func TestProbeFileWriteRespectsRootfsBoundary(t *testing.T) {
	requireNamespaceBackend(t)

	repoRoot := projectRoot(t)
	rootfs := t.TempDir()
	buildProbeIntoRootfs(t, repoRoot, "./cmd/probe-file-write", rootfs, "probe-file-write")

	output, err := runMirage(t, repoRoot,
		"run",
		"--rootfs", rootfs,
		"--net", "host",
		"--",
		"/probe-file-write",
		"/inside-out.txt",
		"inside-data",
	)
	if err != nil {
		t.Fatalf("expected in-rootfs write to succeed: %v\noutput:\n%s", err, output)
	}
	if !strings.Contains(output, "write-ok path=/inside-out.txt") {
		t.Fatalf("unexpected success output:\n%s", output)
	}

	written, err := os.ReadFile(filepath.Join(rootfs, "inside-out.txt"))
	if err != nil {
		t.Fatalf("read rootfs output: %v", err)
	}
	if string(written) != "inside-data" {
		t.Fatalf("unexpected written content: %q", string(written))
	}

	hostOutside := filepath.Join(t.TempDir(), "outside.txt")
	output, err = runMirage(t, repoRoot,
		"run",
		"--rootfs", rootfs,
		"--net", "host",
		"--",
		"/probe-file-write",
		hostOutside,
		"outside-data",
	)
	if err == nil {
		t.Fatalf("expected host path write to fail, got output:\n%s", output)
	}
	if _, statErr := os.Stat(hostOutside); !os.IsNotExist(statErr) {
		t.Fatalf("expected no host file write, stat err=%v", statErr)
	}
}

// Verifies that basic outbound TCP behavior matches the selected network mode:
// host networking should connect, while net=none should fail closed.
func TestProbeTCPConnectHonorsNetworkMode(t *testing.T) {
	requireNamespaceBackend(t)

	repoRoot := projectRoot(t)
	probePath := buildProbe(t, repoRoot, "./cmd/probe-tcp-connect")

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	accepted := make(chan struct{}, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			_ = conn.Close()
			accepted <- struct{}{}
		}
	}()

	output, err := runMirage(t, repoRoot,
		"run",
		"--rootfs", "/",
		"--net", "host",
		"--",
		probePath,
		listener.Addr().String(),
	)
	if err != nil {
		t.Fatalf("expected host-network connect to succeed: %v\noutput:\n%s", err, output)
	}
	if !strings.Contains(output, "connect-ok addr=") {
		t.Fatalf("unexpected connect success output:\n%s", output)
	}
	select {
	case <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("expected listener to accept a connection")
	}

	output, err = runMirage(t, repoRoot,
		"run",
		"--rootfs", "/",
		"--net", "none",
		"--",
		probePath,
		listener.Addr().String(),
	)
	if err == nil {
		t.Fatalf("expected no-network connect to fail, got output:\n%s", output)
	}
	if !strings.Contains(output, "connect-failed") {
		t.Fatalf("expected connect-failed output, got:\n%s", output)
	}
}

// Will verify that read-only and read-write bind mounts enforce the intended
// visibility and mutability boundaries once bind mounts are implemented.
func TestProbeBindMountReadOnlyBoundary(t *testing.T) {
	requireNamespaceBackend(t)

	repoRoot := projectRoot(t)
	rootfs := t.TempDir()
	buildProbeIntoRootfs(t, repoRoot, "./cmd/probe-file-read", rootfs, "probe-file-read")
	buildProbeIntoRootfs(t, repoRoot, "./cmd/probe-file-write", rootfs, "probe-file-write")

	hostReadOnly := t.TempDir()
	hostWritable := t.TempDir()

	if err := os.WriteFile(filepath.Join(hostReadOnly, "fixture.txt"), []byte("ro-data"), 0o644); err != nil {
		t.Fatalf("write read-only fixture: %v", err)
	}

	output, err := runMirage(t, repoRoot,
		"run",
		"--rootfs", rootfs,
		"--net", "host",
		"--ro-bind", hostReadOnly+":/ro",
		"--rw-bind", hostWritable+":/rw",
		"--",
		"/probe-file-read",
		"/ro/fixture.txt",
	)
	if err != nil {
		t.Fatalf("expected read-only bind read to succeed: %v\noutput:\n%s", err, output)
	}
	if !strings.Contains(output, "read-ok path=/ro/fixture.txt") {
		t.Fatalf("unexpected read-only bind read output:\n%s", output)
	}

	output, err = runMirage(t, repoRoot,
		"run",
		"--rootfs", rootfs,
		"--net", "host",
		"--ro-bind", hostReadOnly+":/ro",
		"--rw-bind", hostWritable+":/rw",
		"--",
		"/probe-file-write",
		"/ro/blocked.txt",
		"blocked",
	)
	if err == nil {
		t.Fatalf("expected read-only bind write to fail, got output:\n%s", output)
	}
	if !strings.Contains(output, "write-failed path=/ro/blocked.txt") {
		t.Fatalf("unexpected read-only bind write output:\n%s", output)
	}
	if _, statErr := os.Stat(filepath.Join(hostReadOnly, "blocked.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("expected no host write through read-only bind, stat err=%v", statErr)
	}

	output, err = runMirage(t, repoRoot,
		"run",
		"--rootfs", rootfs,
		"--net", "host",
		"--ro-bind", hostReadOnly+":/ro",
		"--rw-bind", hostWritable+":/rw",
		"--",
		"/probe-file-write",
		"/rw/created.txt",
		"rw-data",
	)
	if err != nil {
		t.Fatalf("expected writable bind write to succeed: %v\noutput:\n%s", err, output)
	}
	if !strings.Contains(output, "write-ok path=/rw/created.txt") {
		t.Fatalf("unexpected writable bind output:\n%s", output)
	}

	written, err := os.ReadFile(filepath.Join(hostWritable, "created.txt"))
	if err != nil {
		t.Fatalf("read writable bind output: %v", err)
	}
	if string(written) != "rw-data" {
		t.Fatalf("unexpected writable bind content: %q", string(written))
	}
}

// Will verify that isolated networking only permits explicitly allowed
// destinations once allow-host / allow-cidr / allow-port are enforced.
func TestProbeIsolatedNetworkAllowHostRules(t *testing.T) {
	requireNamespaceBackend(t)

	repoRoot := projectRoot(t)
	probePath := buildProbe(t, repoRoot, "./cmd/probe-tcp-connect")

	output, err := runMirage(t, repoRoot,
		"run",
		"--rootfs", "/",
		"--net", "isolated",
		"--",
		"sh", "-c", probePath+" 203.0.113.10:443 || true",
	)
	if err == nil {
		t.Fatalf("expected undeclared isolated attempt to fail policy, got output:\n%s", output)
	}
	if !strings.Contains(output, "isolated network policy blocked attempted connections: 203.0.113.10:443") {
		t.Fatalf("unexpected isolated policy failure output:\n%s", output)
	}

	output, err = runMirage(t, repoRoot,
		"run",
		"--rootfs", "/",
		"--net", "isolated",
		"--allow-host", "203.0.113.10:443",
		"--",
		"sh", "-c", probePath+" 203.0.113.10:443 || true",
	)
	if err != nil {
		t.Fatalf("expected declared isolated attempt to pass policy: %v\noutput:\n%s", err, output)
	}
}

// Will verify that denied network attempts produce durable warn-mode records
// that can later drive preset refinement.
func TestProbeWarnModeRecordsDeniedNetworkAttempt(t *testing.T) {
	requireNamespaceBackend(t)

	repoRoot := projectRoot(t)
	stateDir := t.TempDir()

	cmd := exec.Command(
		"go", "run", "./cmd/mirage",
		"run",
		"--rootfs", "/",
		"--net", "isolated",
		"--warn", "net",
		"--",
		"bash", "-lc", "echo >/dev/tcp/203.0.113.10/443 || true",
	)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "MIRAGE_STATE_DIR="+stateDir)

	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected policy failure with warn record, got output:\n%s", string(output))
	}
	if !strings.Contains(string(output), "isolated network policy blocked attempted connections: 203.0.113.10:443") {
		t.Fatalf("unexpected warn-mode failure output:\n%s", string(output))
	}

	entries, err := os.ReadDir(filepath.Join(stateDir, "warn"))
	if err != nil {
		t.Fatalf("read warn directory: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one warn record")
	}
}

// Will verify that sandbox workloads cannot exceed the configured process-count
// ceiling once PID control is enforced through cgroups.
func TestProbePIDLimitEnforcement(t *testing.T) {
	requireNamespaceBackend(t)
	requireCgroupBackend(t)

	repoRoot := projectRoot(t)
	probePath := buildProbe(t, repoRoot, "./cmd/probe-spawn-many")

	output, err := runMirage(t, repoRoot,
		"run",
		"--rootfs", "/",
		"--net", "host",
		"--pids", "2",
		"--",
		probePath,
		"-count", "3",
		"-sleep", "250ms",
	)
	if err == nil {
		t.Fatalf("expected pid-limited run to fail, got output:\n%s", output)
	}
	if !strings.Contains(output, "spawn-failed") &&
		!strings.Contains(output, "pthread_create failed") &&
		!strings.Contains(output, "failed to create new OS thread") &&
		!strings.Contains(output, "resource temporarily unavailable") {
		t.Fatalf("expected pid-limit failure output, got:\n%s", output)
	}

	output, err = runMirage(t, repoRoot,
		"run",
		"--rootfs", "/",
		"--net", "host",
		"--pids", "32",
		"--",
		probePath,
		"-count", "3",
		"-sleep", "250ms",
	)
	if err != nil {
		t.Fatalf("expected higher pid limit run to succeed: %v\noutput:\n%s", err, output)
	}
	if !strings.Contains(output, "spawn-ok count=3") {
		t.Fatalf("unexpected pid-limit success output:\n%s", output)
	}
}

// Will verify that sandbox workloads cannot exceed the configured memory limit
// once memory control is enforced through cgroups.
func TestProbeMemoryLimitEnforcement(t *testing.T) {
	requireNamespaceBackend(t)
	requireCgroupBackend(t)

	repoRoot := projectRoot(t)

	output, err := runMirage(t, repoRoot,
		"run",
		"--rootfs", "/",
		"--net", "host",
		"--memory", "32M",
		"--",
		"python3", "-c", "a=[b'x'*1024*1024 for _ in range(128)]; print('memory-ok'); import time; time.sleep(0.25)",
	)
	if err == nil {
		t.Fatalf("expected memory-limited run to fail, got output:\n%s", output)
	}

	output, err = runMirage(t, repoRoot,
		"run",
		"--rootfs", "/",
		"--net", "host",
		"--memory", "256M",
		"--",
		"python3", "-c", "a=[b'x'*1024*1024 for _ in range(16)]; print('memory-ok'); import time; time.sleep(0.25)",
	)
	if err != nil {
		t.Fatalf("expected higher memory limit run to succeed: %v\noutput:\n%s", err, output)
	}
	if !strings.Contains(output, "memory-ok") {
		t.Fatalf("unexpected memory-limit success output:\n%s", output)
	}
}

// Will verify that procfs and related mount visibility do not leak broader host
// state than the sandbox should expose.
func TestProbeProcVisibilityHardening(t *testing.T) {
	t.Skip("pending stronger proc and mount isolation hardening")
}

func buildProbe(t *testing.T, repoRoot, pkg string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), filepath.Base(pkg))
	buildProbeBinary(t, repoRoot, pkg, out)
	return out
}

func buildProbeIntoRootfs(t *testing.T, repoRoot, pkg, rootfs, name string) string {
	t.Helper()
	out := filepath.Join(rootfs, name)
	buildProbeBinary(t, repoRoot, pkg, out)
	return out
}

func buildProbeBinary(t *testing.T, repoRoot, pkg, out string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", out, pkg)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	buildOutput, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build probe %s failed: %v\noutput:\n%s", pkg, err, string(buildOutput))
	}
}

func runMirage(t *testing.T, repoRoot string, args ...string) (string, error) {
	t.Helper()
	cmdArgs := append([]string{"run", "./cmd/mirage"}, args...)
	cmd := exec.Command("go", cmdArgs...)
	cmd.Dir = repoRoot

	output, err := cmd.CombinedOutput()
	return string(output), err
}

func requireCgroupBackend(t *testing.T) {
	t.Helper()

	cmd := exec.Command("systemd-run", "--user", "--scope", "--quiet", "--collect", "--", "sh", "-c", "true")
	output, err := cmd.CombinedOutput()
	if err == nil {
		return
	}

	msg := string(output)
	if strings.Contains(msg, "command not found") ||
		strings.Contains(msg, "Failed to connect to bus") ||
		strings.Contains(msg, "No medium found") ||
		strings.Contains(msg, "Access denied") {
		t.Skipf("cgroup backend unsupported in this test environment: %s", strings.TrimSpace(msg))
	}

	t.Fatalf("cgroup capability probe failed unexpectedly: %v\noutput:\n%s", err, msg)
}
