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

func TestProbeSpawnChildStaysInSandboxTree(t *testing.T) {
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

func TestProbeFileReadRespectsRootfsBoundary(t *testing.T) {
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

func TestProbeFileWriteRespectsRootfsBoundary(t *testing.T) {
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

func TestProbeTCPConnectHonorsNetworkMode(t *testing.T) {
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
