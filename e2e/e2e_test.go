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
