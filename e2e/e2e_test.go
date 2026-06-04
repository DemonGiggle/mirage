package e2e

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if err := os.Setenv("MIRAGE_TEST_SKIP_MMDEBSTRAP", "1"); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

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
		"ping", "-c", "1", "-W", "1", "127.0.0.1",
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
		"--output", rootfs,
	)
	initCmd.Dir = repoRoot

	output, err := initCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mirage rootfs init failed: %v\noutput:\n%s", err, string(output))
	}
	if !strings.Contains(string(output), "mirage rootfs init") {
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
		"rootfs validation: ok",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected doctor output to contain %q, got:\n%s", needle, got)
		}
	}
}

func TestRootfsInitBashProbeSupportsCurrentExampleNetworkPolicies(t *testing.T) {
	requireNamespaceBackend(t)
	requireHostLoopbackListener(t)

	repoRoot := projectRoot(t)
	rootfs := filepath.Join(t.TempDir(), "openclaw-work-rootfs")

	initCmd := exec.Command(
		"go", "run", "./cmd/mirage",
		"rootfs",
		"init",
		"--output", rootfs,
	)
	initCmd.Dir = repoRoot

	output, err := initCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mirage rootfs init failed: %v\noutput:\n%s", err, string(output))
	}
	for _, target := range []string{
		filepath.Join(rootfs, "bin", "bash"),
	} {
		if _, err := os.Stat(target); err != nil {
			t.Fatalf("expected generated rootfs to include %q: %v", target, err)
		}
	}

	policyDir := filepath.Join(repoRoot, "examples", "network-policies")
	entries, err := os.ReadDir(policyDir)
	if err != nil {
		t.Fatalf("read example network policies: %v", err)
	}

	var policyNames []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		policyNames = append(policyNames, entry.Name())
	}
	sort.Strings(policyNames)
	if len(policyNames) == 0 {
		t.Fatalf("expected at least one example network policy in %q", policyDir)
	}

	for _, policyName := range policyNames {
		policyName := policyName
		t.Run(policyName, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen on host loopback: %v", err)
			}
			defer listener.Close()

			accepted := make(chan string, 1)
			acceptErr := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					acceptErr <- err
					return
				}
				defer conn.Close()

				_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
				buffer := make([]byte, 4)
				count, err := conn.Read(buffer)
				if err != nil {
					acceptErr <- err
					return
				}
				if _, err := conn.Write([]byte("pong")); err != nil {
					acceptErr <- err
					return
				}
				accepted <- string(buffer[:count])
			}()

			addr := listener.Addr().(*net.TCPAddr)
			command := "exec 3<>/dev/tcp/127.0.0.1/" + strconv.Itoa(addr.Port) + " && printf ping >&3 && IFS= read -r -N 4 reply <&3 && printf %s \"$reply\""

			output, err := runMirage(t, repoRoot,
				"run",
				"--rootfs", rootfs,
				"--network-policy-file", filepath.Join(policyDir, policyName),
				"--",
				"/bin/bash", "-lc", command,
			)

			switch policyName {
			case "allow-all.yaml":
				if err != nil {
					t.Fatalf("expected allow-all policy to reach the host loopback listener, got %v\noutput:\n%s", err, output)
				}
				if !strings.Contains(output, "pong") {
					t.Fatalf("expected allow-all probe output to contain host reply, got:\n%s", output)
				}
				select {
				case payload := <-accepted:
					if payload != "ping" {
						t.Fatalf("expected host listener payload %q, got %q", "ping", payload)
					}
				case err := <-acceptErr:
					t.Fatalf("host listener accept failed: %v", err)
				case <-time.After(2 * time.Second):
					t.Fatal("expected host listener to accept a connection")
				}
			case "offline.yaml":
				if err == nil {
					t.Fatalf("expected offline policy to block the host loopback probe, got output:\n%s", output)
				}
				if !bashTCPConnectFailed(output) {
					t.Fatalf("expected offline policy probe to fail due to blocked connectivity, got:\n%s", output)
				}
				assertNoUnexpectedListenerAccept(t, accepted, acceptErr)
			case "block-local-egress.yaml":
				if err != nil && routedPolicyBackendUnavailable(output) {
					t.Skipf("routed network backend unavailable in this environment:\n%s", output)
				}
				if err == nil {
					t.Fatalf("expected block-local-egress policy not to reach the host loopback listener, got output:\n%s", output)
				}
				if !bashTCPConnectFailed(output) {
					t.Fatalf("expected block-local-egress probe to fail when targeting host loopback, got:\n%s", output)
				}
				assertNoUnexpectedListenerAccept(t, accepted, acceptErr)
			default:
				t.Fatalf("unexpected example network policy fixture %q; update the e2e expectation matrix", policyName)
			}
		})
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

func requireHostLoopbackListener(t *testing.T) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err == nil {
		_ = listener.Close()
		return
	}

	msg := err.Error()
	if strings.Contains(msg, "operation not permitted") || strings.Contains(msg, "permission denied") {
		t.Skipf("host loopback listener unavailable in this test environment: %v", err)
	}
	t.Fatalf("host loopback listener probe failed unexpectedly: %v", err)
}

func bashTCPConnectFailed(output string) bool {
	return strings.Contains(output, "Connection refused") ||
		strings.Contains(output, "No route to host") ||
		strings.Contains(output, "Network is unreachable") ||
		strings.Contains(output, "Operation not permitted") ||
		strings.Contains(output, "Connection timed out") ||
		strings.Contains(output, "Invalid argument")
}

func routedPolicyBackendUnavailable(output string) bool {
	return strings.Contains(output, "requires CAP_NET_ADMIN on the host") ||
		strings.Contains(output, "requires IPv4 forwarding on the host") ||
		strings.Contains(output, "Failed to initialize nft: Operation not permitted")
}

func assertNoUnexpectedListenerAccept(t *testing.T, accepted <-chan string, acceptErr <-chan error) {
	t.Helper()

	select {
	case payload := <-accepted:
		t.Fatalf("expected no host listener connection, but accepted payload %q", payload)
	case err := <-acceptErr:
		if err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
			t.Fatalf("host listener failed unexpectedly: %v", err)
		}
	case <-time.After(250 * time.Millisecond):
	}
}
