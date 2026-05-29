package e2e

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
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

// Verifies that dedicated rootfs sandboxes provide the managed /dev layout
// needed for child process spawning inside the guest.
func TestProbeSpawnChildWorksInPreparedRootfs(t *testing.T) {
	requireNamespaceBackend(t)

	repoRoot := projectRoot(t)
	rootfs := t.TempDir()
	buildProbeIntoRootfs(t, repoRoot, "./cmd/probe-spawn-child", rootfs, "probe-spawn-child")

	output, err := runMirage(t, repoRoot,
		"run",
		"--rootfs", rootfs,
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
		"--",
		"/probe-spawn-child",
	)
	if err != nil {
		t.Fatalf("expected prepared-rootfs child spawn to succeed: %v\noutput:\n%s", err, output)
	}

	if !strings.Contains(output, "parent pid=1") {
		t.Fatalf("expected sandbox parent pid 1, got:\n%s", output)
	}
	if !strings.Contains(output, "child pid=") || !strings.Contains(output, "ppid=1") {
		t.Fatalf("expected child process to stay under sandbox init, got:\n%s", output)
	}
}

// Verifies that dedicated rootfs sandboxes expose the PTY control device needed
// by PTY-backed workloads such as OpenClaw shell execution.
func TestProbeOpenPTMXWorksInPreparedRootfs(t *testing.T) {
	requireNamespaceBackend(t)

	repoRoot := projectRoot(t)
	rootfs := t.TempDir()
	buildProbeIntoRootfs(t, repoRoot, "./cmd/probe-open-ptmx", rootfs, "probe-open-ptmx")

	output, err := runMirage(t, repoRoot,
		"run",
		"--rootfs", rootfs,
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
		"--",
		"/probe-open-ptmx",
	)
	if err != nil {
		t.Fatalf("expected prepared-rootfs PTY probe to succeed: %v\noutput:\n%s", err, output)
	}
	if !strings.Contains(output, "open-ok path=/dev/ptmx") {
		t.Fatalf("unexpected PTY probe output:\n%s", output)
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
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
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
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
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
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
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
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
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

// Verifies that the standalone policy fixtures cover the old host/offline cases.
func TestProbeTCPConnectHonorsNetworkPolicyFiles(t *testing.T) {
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
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
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
		"--network-policy-file", policyFixturePath(repoRoot, "offline.yaml"),
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

func TestProbeTCPConnectLoadsAllowAllPolicyFile(t *testing.T) {
	requireNamespaceBackend(t)

	repoRoot := projectRoot(t)
	probePath := buildProbe(t, repoRoot, "./cmd/probe-tcp-connect")

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	output, err := runMirage(t, repoRoot,
		"run",
		"--rootfs", "/",
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
		"--",
		probePath,
		listener.Addr().String(),
	)
	if err != nil {
		t.Fatalf("expected allow-all policy file connect to succeed: %v\noutput:\n%s", err, output)
	}
	if !strings.Contains(output, "connect-ok addr=") {
		t.Fatalf("unexpected connect success output:\n%s", output)
	}
}

func TestProbeTCPConnectLoadsOfflinePolicyPresetFile(t *testing.T) {
	requireNamespaceBackend(t)

	repoRoot := projectRoot(t)
	probePath := buildProbe(t, repoRoot, "./cmd/probe-tcp-connect")
	presetFile := writePolicyPresetFileE2E(t, repoRoot, "/", "offline.yaml")

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	output, err := runMirage(t, repoRoot,
		"run",
		"--preset-file", presetFile,
		"--",
		probePath,
		listener.Addr().String(),
	)
	if err == nil {
		t.Fatalf("expected offline preset-file connect to fail, got output:\n%s", output)
	}
	if !strings.Contains(output, "connect-failed") {
		t.Fatalf("expected connect-failed output, got:\n%s", output)
	}
}

func TestProbeTCPConnectRejectsLoopbackWhenPolicyDeniesIt(t *testing.T) {
	requireNamespaceBackend(t)

	repoRoot := projectRoot(t)
	probePath := buildProbe(t, repoRoot, "./cmd/probe-tcp-connect")

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	output, err := runMirage(t, repoRoot,
		"run",
		"--rootfs", "/",
		"--network-policy-file", policyFixturePath(repoRoot, "loopback-deny-offline.yaml"),
		"--",
		probePath,
		listener.Addr().String(),
	)
	if err == nil {
		t.Fatalf("expected loopback-deny policy connect to fail, got output:\n%s", output)
	}
	if !strings.Contains(output, "connect-failed") {
		t.Fatalf("expected connect-failed output, got:\n%s", output)
	}
}

func TestRunRejectsUnsupportedEgressPolicyFileE2E(t *testing.T) {
	repoRoot := projectRoot(t)

	output, err := runMirage(t, repoRoot,
		"run",
		"--rootfs", "/",
		"--network-policy-file", policyFixturePath(repoRoot, "allow-private-egress.yaml"),
		"--",
		"echo", "hello",
	)
	if err == nil {
		t.Fatalf("expected unsupported egress policy to fail, got output:\n%s", output)
	}
	if !strings.Contains(output, "allow semantics this backend cannot enforce yet") {
		t.Fatalf("expected unsupported egress policy error, got:\n%s", output)
	}
}

func TestRunRejectsUnsupportedIngressPolicyFileE2E(t *testing.T) {
	repoRoot := projectRoot(t)

	output, err := runMirage(t, repoRoot,
		"run",
		"--rootfs", "/",
		"--network-policy-file", policyFixturePath(repoRoot, "default-ingress-allow.yaml"),
		"--",
		"echo", "hello",
	)
	if err == nil {
		t.Fatalf("expected unsupported ingress policy to fail, got output:\n%s", output)
	}
	if !strings.Contains(output, "ingress.default=allow") {
		t.Fatalf("expected unsupported ingress policy error, got:\n%s", output)
	}
}

func TestRunRejectsDomainPolicyFileE2E(t *testing.T) {
	repoRoot := projectRoot(t)

	output, err := runMirage(t, repoRoot,
		"run",
		"--rootfs", "/",
		"--network-policy-file", policyFixturePath(repoRoot, "domain-egress.yaml"),
		"--",
		"echo", "hello",
	)
	if err == nil {
		t.Fatalf("expected domain-backed policy to fail, got output:\n%s", output)
	}
	if !strings.Contains(output, "destination.domain is documented but not enforceable") {
		t.Fatalf("expected deferred domain error, got:\n%s", output)
	}
}

// Verifies that explicitly provided environment variables are visible inside the
// sandbox while missing values fail loudly.
func TestProbeEnvReadSeesExplicitEnv(t *testing.T) {
	requireNamespaceBackend(t)

	repoRoot := projectRoot(t)
	probePath := buildProbe(t, repoRoot, "./cmd/probe-env-read")

	output, err := runMirage(t, repoRoot,
		"run",
		"--rootfs", "/",
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
		"--env", "MIRAGE_SAMPLE_ENV=sandbox-value",
		"--",
		probePath,
		"MIRAGE_SAMPLE_ENV",
	)
	if err != nil {
		t.Fatalf("expected env probe to succeed: %v\noutput:\n%s", err, output)
	}
	if !strings.Contains(output, "env-ok name=MIRAGE_SAMPLE_ENV value=sandbox-value") {
		t.Fatalf("unexpected env probe success output:\n%s", output)
	}

	output, err = runMirage(t, repoRoot,
		"run",
		"--rootfs", "/",
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
		"--",
		probePath,
		"MIRAGE_SAMPLE_ENV",
	)
	if err == nil {
		t.Fatalf("expected env probe to fail without explicit env, got output:\n%s", output)
	}
	if !strings.Contains(output, "env-missing name=MIRAGE_SAMPLE_ENV") {
		t.Fatalf("unexpected env probe failure output:\n%s", output)
	}
}

// Verifies that proc visibility reflects the sandbox PID namespace rather than
// the host process list.
func TestProbeListProcsReflectsSandboxPIDNamespace(t *testing.T) {
	requireNamespaceBackend(t)

	repoRoot := projectRoot(t)
	rootfs := t.TempDir()
	buildProbeIntoRootfs(t, repoRoot, "./cmd/probe-list-procs", rootfs, "probe-list-procs")

	output, err := runMirage(t, repoRoot,
		"run",
		"--rootfs", rootfs,
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
		"--",
		"/probe-list-procs",
	)
	if err != nil {
		t.Fatalf("expected proc probe to succeed: %v\noutput:\n%s", err, output)
	}
	if !strings.Contains(output, "proc-ok count=") || !strings.Contains(output, "pids=1") {
		t.Fatalf("unexpected proc probe output:\n%s", output)
	}

	hostPID := strconv.Itoa(os.Getpid())
	if strings.Contains(strings.TrimSpace(output), "pids="+hostPID) || strings.Contains(output, ","+hostPID) {
		t.Fatalf("expected host pid %s to stay out of sandbox proc listing, got:\n%s", hostPID, output)
	}
}

// Verifies that a symlink target can be inspected from inside the sandbox with
// a dedicated narrow probe.
func TestProbeReadlinkReportsSymlinkTarget(t *testing.T) {
	requireNamespaceBackend(t)

	repoRoot := projectRoot(t)
	rootfs := t.TempDir()
	buildProbeIntoRootfs(t, repoRoot, "./cmd/probe-readlink", rootfs, "probe-readlink")

	if err := os.WriteFile(filepath.Join(rootfs, "target.txt"), []byte("target"), 0o644); err != nil {
		t.Fatalf("write readlink target: %v", err)
	}
	if err := os.Symlink("target.txt", filepath.Join(rootfs, "link.txt")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	output, err := runMirage(t, repoRoot,
		"run",
		"--rootfs", rootfs,
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
		"--",
		"/probe-readlink",
		"/link.txt",
	)
	if err != nil {
		t.Fatalf("expected readlink probe to succeed: %v\noutput:\n%s", err, output)
	}
	if !strings.Contains(output, "readlink-ok path=/link.txt target=target.txt") {
		t.Fatalf("unexpected readlink probe output:\n%s", output)
	}
}

// Verifies that HTTP-level egress follows the selected policy file.
func TestProbeHTTPGetHonorsNetworkPolicyFiles(t *testing.T) {
	requireNamespaceBackend(t)

	repoRoot := projectRoot(t)
	probePath := buildProbe(t, repoRoot, "./cmd/probe-http-get")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	output, err := runMirage(t, repoRoot,
		"run",
		"--rootfs", "/",
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
		"--",
		probePath,
		server.URL,
	)
	if err != nil {
		t.Fatalf("expected host-network HTTP GET to succeed: %v\noutput:\n%s", err, output)
	}
	if !strings.Contains(output, "http-ok url="+server.URL+" status=200") {
		t.Fatalf("unexpected HTTP success output:\n%s", output)
	}

	output, err = runMirage(t, repoRoot,
		"run",
		"--rootfs", "/",
		"--network-policy-file", policyFixturePath(repoRoot, "offline.yaml"),
		"--",
		probePath,
		server.URL,
	)
	if err == nil {
		t.Fatalf("expected no-network HTTP GET to fail, got output:\n%s", output)
	}
	if !strings.Contains(output, "http-failed url="+server.URL) {
		t.Fatalf("unexpected HTTP failure output:\n%s", output)
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
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
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
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
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
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
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
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
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
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
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
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
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
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
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

func policyFixturePath(repoRoot string, name string) string {
	return filepath.Join(repoRoot, "testdata", "network-policies", name)
}

func writePolicyPresetFileE2E(t *testing.T, repoRoot string, rootfsPath string, fixtureName string) string {
	t.Helper()
	body := "rootfs:\n  path: " + rootfsPath + "\nnetworkPolicyFile: " + policyFixturePath(repoRoot, fixtureName) + "\ndescription: Policy fixture preset\n"
	path := filepath.Join(t.TempDir(), "preset.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write preset file: %v", err)
	}
	return path
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
