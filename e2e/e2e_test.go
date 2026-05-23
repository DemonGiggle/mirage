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
	if !strings.Contains(got, `chroot to "`) || !strings.Contains(got, "no such file or directory") {
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

func TestRunWarnsWhenIsolatedNetworkRulesAreNotEnforced(t *testing.T) {
	requireNamespaceBackend(t)

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
	if !strings.Contains(got, "warning: isolated network namespace is active, but allow rules are not enforced yet") {
		t.Fatalf("expected isolated network warning, got:\n%s", got)
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
