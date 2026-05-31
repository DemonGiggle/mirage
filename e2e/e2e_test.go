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
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
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
		"--network-policy-file", policyFixturePath(repoRoot, "offline.yaml"),
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
		"network-policy-file: " + policyFixturePath(repoRoot, "offline.yaml"),
		"stdout-log: " + stdoutLog,
		"execution: skipped (--dry-run)",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected output to contain %q, got:\n%s", needle, got)
		}
	}
}

func TestRunPropagatesInteractiveStdin(t *testing.T) {
	requireNamespaceBackend(t)

	repoRoot := projectRoot(t)
	cmd := exec.Command(
		"go", "run", "./cmd/mirage",
		"run",
		"--rootfs", "/",
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
		"--",
		"sh", "-c", "IFS= read -r line; printf '%s' \"$line\"",
	)
	cmd.Dir = repoRoot
	cmd.Stdin = strings.NewReader("interactive-ok\n")

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mirage interactive run failed: %v\noutput:\n%s", err, string(output))
	}
	if !strings.Contains(string(output), "interactive-ok") {
		t.Fatalf("expected interactive stdin to reach sandbox command, got:\n%s", string(output))
	}
}

func TestRunReportsUnsupportedPing(t *testing.T) {
	requireNamespaceBackend(t)

	repoRoot := projectRoot(t)
	cmd := exec.Command(
		"go", "run", "./cmd/mirage",
		"run",
		"--rootfs", "/",
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
		"--",
		"/usr/bin/ping", "-c", "1", "-W", "1", "127.0.0.1",
	)
	cmd.Dir = repoRoot

	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected mirage ping run to fail, got output:\n%s", string(output))
	}

	got := string(output)
	if !strings.Contains(got, "ping is not supported in this Mirage sandbox on the current host/kernel because ICMP sockets are not available") {
		t.Fatalf("expected Mirage-owned ping guidance, got:\n%s", got)
	}
	if strings.Contains(got, "socktype: SOCK_RAW") {
		t.Fatalf("expected Mirage to fail before ping emits the low-level socket error, got:\n%s", got)
	}
}

func TestRootfsInitGeneratesRunnableBasicRootfs(t *testing.T) {
	requireNamespaceBackend(t)

	repoRoot := projectRoot(t)
	rootfs := filepath.Join(t.TempDir(), "basic-rootfs")

	initCmd := exec.Command(
		"go", "run", "./cmd/mirage",
		"rootfs",
		"init",
		"--template", "basic",
		"--output", rootfs,
	)
	initCmd.Dir = repoRoot

	output, err := initCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mirage rootfs init failed: %v\noutput:\n%s", err, string(output))
	}
	if !strings.Contains(string(output), "template: basic") {
		t.Fatalf("expected rootfs init output, got:\n%s", string(output))
	}

	runCmd := exec.Command(
		"go", "run", "./cmd/mirage",
		"run",
		"--rootfs", rootfs,
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
		"--",
		"/bin/ls", "/",
	)
	runCmd.Dir = repoRoot

	output, err = runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected generated rootfs run to succeed: %v\noutput:\n%s", err, string(output))
	}

	got := string(output)
	for _, needle := range []string{"bin", "proc", "run", "tmp"} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected generated rootfs listing to contain %q, got:\n%s", needle, got)
		}
	}
}

func TestDoctorValidatesGeneratedBasicRootfs(t *testing.T) {
	repoRoot := projectRoot(t)
	rootfs := filepath.Join(t.TempDir(), "basic-rootfs")

	initCmd := exec.Command(
		"go", "run", "./cmd/mirage",
		"rootfs",
		"init",
		"--template", "basic",
		"--output", rootfs,
	)
	initCmd.Dir = repoRoot

	output, err := initCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mirage rootfs init failed: %v\noutput:\n%s", err, string(output))
	}

	doctorCmd := exec.Command(
		"go", "run", "./cmd/mirage",
		"doctor",
		"--rootfs", rootfs,
		"--command", "/bin/ls",
	)
	doctorCmd.Dir = repoRoot

	output, err = doctorCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mirage doctor failed: %v\noutput:\n%s", err, string(output))
	}

	got := string(output)
	for _, needle := range []string{
		"resolved command: /bin/ls",
		"shared libraries: ok",
		"rootfs validation: ok",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected doctor output to contain %q, got:\n%s", needle, got)
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
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
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
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
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
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
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
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
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

func TestRunFailsWhenBindMountSourceDoesNotExist(t *testing.T) {
	requireNamespaceBackend(t)

	repoRoot := projectRoot(t)
	rootfs := t.TempDir()
	buildProbeBinary(t, repoRoot, "./cmd/probe-file-read", filepath.Join(rootfs, "probe-file-read"))
	missingSource := filepath.Join(t.TempDir(), "missing-source")

	cmd := exec.Command(
		"go", "run", "./cmd/mirage",
		"run",
		"--rootfs", rootfs,
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
		"--ro-bind", missingSource+":/mounted",
		"--",
		"/probe-file-read", "/mounted/data.txt",
	)
	cmd.Dir = repoRoot

	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected missing bind mount source run to fail, got output:\n%s", string(output))
	}

	got := string(output)
	if !strings.Contains(got, `prepare bind mount source "`) || !strings.Contains(got, "no such file or directory") {
		t.Fatalf("expected missing bind source failure, got:\n%s", got)
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
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
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
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
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
