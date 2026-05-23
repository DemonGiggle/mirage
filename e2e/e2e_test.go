package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunExportsLogsToHost(t *testing.T) {
	requireNamespaceBackend(t)

	repoRoot := projectRoot(t)
	tmp := t.TempDir()
	stdoutLog := filepath.Join(tmp, "stdout.log")
	stderrLog := filepath.Join(tmp, "stderr.log")

	cmd := exec.Command(
		"go", "run", "./cmd/mirage",
		"run",
		"--rootfs", "/",
		"--net", "host",
		"--stdout-log", stdoutLog,
		"--stderr-log", stderrLog,
		"--",
		"sh", "-c", "printf 'hello-out'; printf 'hello-err' >&2",
	)
	cmd.Dir = repoRoot

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mirage run failed: %v\noutput:\n%s", err, string(output))
	}

	stdoutData, err := os.ReadFile(stdoutLog)
	if err != nil {
		t.Fatalf("read stdout log: %v", err)
	}
	if string(stdoutData) != "hello-out" {
		t.Fatalf("unexpected stdout log content: %q", string(stdoutData))
	}

	stderrData, err := os.ReadFile(stderrLog)
	if err != nil {
		t.Fatalf("read stderr log: %v", err)
	}
	if string(stderrData) != "hello-err" {
		t.Fatalf("unexpected stderr log content: %q", string(stderrData))
	}
}

func TestRunDryRunE2E(t *testing.T) {
	repoRoot := projectRoot(t)
	tmp := t.TempDir()
	stdoutLog := filepath.Join(tmp, "stdout.log")

	cmd := exec.Command(
		"go", "run", "./cmd/mirage",
		"run",
		"--rootfs", "/",
		"--preset", "openai",
		"--warn", "net",
		"--stdout-log", stdoutLog,
		"--dry-run",
		"--",
		"echo", "hello",
	)
	cmd.Dir = repoRoot

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mirage dry-run failed: %v\noutput:\n%s", err, string(output))
	}

	got := string(output)
	for _, needle := range []string{
		"mirage run preview",
		"preset: openai",
		"stdout-log: " + stdoutLog,
		"execution: skipped (--dry-run)",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected output to contain %q, got:\n%s", needle, got)
		}
	}
}

func TestRunCreatesIsolatedProcessTree(t *testing.T) {
	requireNamespaceBackend(t)

	repoRoot := projectRoot(t)

	cmd := exec.Command(
		"go", "run", "./cmd/mirage",
		"run",
		"--rootfs", "/",
		"--net", "host",
		"--",
		"sh", "-c", `printf 'parent=%s ' "$$"; sh -c 'printf "child=%s ppid=%s" "$$" "$PPID"'`,
	)
	cmd.Dir = repoRoot

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mirage process-tree run failed: %v\noutput:\n%s", err, string(output))
	}

	got := string(output)
	if !strings.Contains(got, "parent=1") {
		t.Fatalf("expected namespace init process to be pid 1, got:\n%s", got)
	}
	if !strings.Contains(got, "child=") || !strings.Contains(got, "ppid=1") {
		t.Fatalf("expected child process to stay under the same namespace root, got:\n%s", got)
	}
}

func TestRunFailsWhenRootfsDoesNotExist(t *testing.T) {
	requireNamespaceBackend(t)

	repoRoot := projectRoot(t)
	missingRootfs := filepath.Join(t.TempDir(), "missing-rootfs")

	cmd := exec.Command(
		"go", "run", "./cmd/mirage",
		"run",
		"--rootfs", missingRootfs,
		"--net", "host",
		"--",
		"/bin/sh", "-c", "true",
	)
	cmd.Dir = repoRoot

	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected missing rootfs run to fail, got output:\n%s", string(output))
	}

	got := string(output)
	if !strings.Contains(got, `prepare rootfs "`) || !strings.Contains(got, "no such file or directory") {
		t.Fatalf("expected missing rootfs chroot failure, got:\n%s", got)
	}
}

func TestRunFailsWhenCwdDoesNotExistInsideRootfs(t *testing.T) {
	requireNamespaceBackend(t)

	repoRoot := projectRoot(t)
	rootfs := t.TempDir()
	probePath := filepath.Join(rootfs, "probe-file-read")
	buildProbeBinary(t, repoRoot, "./cmd/probe-file-read", probePath)

	cmd := exec.Command(
		"go", "run", "./cmd/mirage",
		"run",
		"--rootfs", rootfs,
		"--net", "host",
		"--cwd", "/missing-dir",
		"--",
		"/probe-file-read", "/missing-file",
	)
	cmd.Dir = repoRoot

	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected missing cwd run to fail, got output:\n%s", string(output))
	}

	got := string(output)
	if !strings.Contains(got, `chdir to "/missing-dir"`) || !strings.Contains(got, "no such file or directory") {
		t.Fatalf("expected missing cwd failure, got:\n%s", got)
	}
}

func TestRunMissingCommandShowsRootfsHint(t *testing.T) {
	requireNamespaceBackend(t)

	repoRoot := projectRoot(t)
	rootfs := t.TempDir()

	cmd := exec.Command(
		"go", "run", "./cmd/mirage",
		"run",
		"--rootfs", rootfs,
		"--net", "host",
		"--",
		"echo", "hello",
	)
	cmd.Dir = repoRoot

	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected missing command run to fail, got output:\n%s", string(output))
	}

	got := string(output)
	for _, needle := range []string{
		`resolve command "echo" inside rootfs "`,
		`install the executable in the rootfs, set PATH for the sandbox, or invoke it by absolute path inside the rootfs`,
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected missing command failure to mention %q, got:\n%s", needle, got)
		}
	}
}

func TestRunMountsProcInsidePreparedRootfs(t *testing.T) {
	requireNamespaceBackend(t)

	repoRoot := projectRoot(t)
	rootfs := t.TempDir()
	buildProbeBinary(t, repoRoot, "./cmd/probe-file-read", filepath.Join(rootfs, "probe-file-read"))

	cmd := exec.Command(
		"go", "run", "./cmd/mirage",
		"run",
		"--rootfs", rootfs,
		"--net", "host",
		"--",
		"/probe-file-read", "/proc/mounts",
	)
	cmd.Dir = repoRoot

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected proc mount read to succeed: %v\noutput:\n%s", err, string(output))
	}
	if !strings.Contains(string(output), "read-ok path=/proc/mounts") {
		t.Fatalf("unexpected proc mount output:\n%s", string(output))
	}
}

func TestRunUsesTmpfsForSandboxTmp(t *testing.T) {
	requireNamespaceBackend(t)

	repoRoot := projectRoot(t)
	rootfs := t.TempDir()
	buildProbeBinary(t, repoRoot, "./cmd/probe-file-write", filepath.Join(rootfs, "probe-file-write"))

	hostTmp := filepath.Join(rootfs, "tmp")
	if err := os.MkdirAll(hostTmp, 0o755); err != nil {
		t.Fatalf("prepare host tmp: %v", err)
	}

	cmd := exec.Command(
		"go", "run", "./cmd/mirage",
		"run",
		"--rootfs", rootfs,
		"--net", "host",
		"--",
		"/probe-file-write", "/tmp/sandbox-only.txt", "sandbox-data",
	)
	cmd.Dir = repoRoot

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected tmpfs write to succeed: %v\noutput:\n%s", err, string(output))
	}
	if !strings.Contains(string(output), "write-ok path=/tmp/sandbox-only.txt") {
		t.Fatalf("unexpected tmpfs write output:\n%s", string(output))
	}

	if _, err := os.Stat(filepath.Join(hostTmp, "sandbox-only.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected sandbox tmp write to stay off host rootfs, stat err=%v", err)
	}
}

func TestRunWarnsWhenIsolatedNetworkRulesAreNotEnforced(t *testing.T) {
	requireNamespaceBackend(t)
	requireObservedNetworkBackend(t)

	repoRoot := projectRoot(t)
	tmp := t.TempDir()
	stdoutLog := filepath.Join(tmp, "stdout.log")

	cmd := exec.Command(
		"go", "run", "./cmd/mirage",
		"run",
		"--rootfs", "/",
		"--net", "isolated",
		"--stdout-log", stdoutLog,
		"--",
		"sh", "-c", "printf 'isolated-ok'",
	)
	cmd.Dir = repoRoot

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected isolated network run to succeed: %v\noutput:\n%s", err, string(output))
	}

	got := string(output)
	if !strings.Contains(got, "note: network backend: dedicated net namespace with observed policy enforcement") {
		t.Fatalf("expected isolated network note, got:\n%s", got)
	}
	if !strings.Contains(got, "isolated-ok") {
		t.Fatalf("expected workload output, got:\n%s", got)
	}

	stdoutData, err := os.ReadFile(stdoutLog)
	if err != nil {
		t.Fatalf("read isolated stdout log: %v", err)
	}
	if string(stdoutData) != "isolated-ok" {
		t.Fatalf("unexpected isolated stdout log content: %q", string(stdoutData))
	}
}

func TestRunIsolatedNetworkRejectsUndeclaredConnectAttempt(t *testing.T) {
	requireNamespaceBackend(t)
	requireObservedNetworkBackend(t)

	repoRoot := projectRoot(t)
	cmd := exec.Command(
		"go", "run", "./cmd/mirage",
		"run",
		"--rootfs", "/",
		"--net", "isolated",
		"--",
		"bash", "-lc", "echo >/dev/tcp/203.0.113.10/443 || true",
	)
	cmd.Dir = repoRoot

	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected undeclared isolated connect attempt to fail policy, got output:\n%s", string(output))
	}
	if !strings.Contains(string(output), "isolated network policy blocked attempted connections: 203.0.113.10:443") {
		t.Fatalf("unexpected policy failure output:\n%s", string(output))
	}
}

func TestRunIsolatedNetworkAllowsDeclaredConnectAttempt(t *testing.T) {
	requireNamespaceBackend(t)
	requireObservedNetworkBackend(t)

	repoRoot := projectRoot(t)
	cmd := exec.Command(
		"go", "run", "./cmd/mirage",
		"run",
		"--rootfs", "/",
		"--net", "isolated",
		"--allow-host", "203.0.113.10:443",
		"--",
		"bash", "-lc", "echo >/dev/tcp/203.0.113.10/443 || true",
	)
	cmd.Dir = repoRoot

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected declared isolated connect attempt to pass policy: %v\noutput:\n%s", err, string(output))
	}
	if !strings.Contains(string(output), "note: network backend: dedicated net namespace with observed policy enforcement") {
		t.Fatalf("expected observed-policy note, got:\n%s", string(output))
	}
}

func projectRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve project root: %v", err)
	}
	return root
}

func requireNamespaceBackend(t *testing.T) {
	t.Helper()

	cmd := exec.Command("unshare", "--user", "--map-root-user", "--pid", "--fork", "sh", "-c", "true")
	output, err := cmd.CombinedOutput()
	if err == nil {
		return
	}

	msg := string(output)
	if strings.Contains(msg, "Operation not permitted") || strings.Contains(msg, "write failed /proc/self/uid_map") {
		t.Skipf("namespace backend unsupported in this test environment: %s", strings.TrimSpace(msg))
	}

	t.Fatalf("namespace capability probe failed unexpectedly: %v\noutput:\n%s", err, msg)
}

func requireObservedNetworkBackend(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("strace"); err != nil {
		t.Skip("observed isolated network backend requires strace on PATH")
	}
}
