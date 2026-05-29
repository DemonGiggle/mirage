package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDoctorValidatesInitRootfsServiceFixtureE2E(t *testing.T) {
	repoRoot := projectRoot(t)
	rootfs := prepareInitRootfs(t, repoRoot)

	unitPath := filepath.Join(rootfs, "etc", "systemd", "system", "openclaw.service")
	if err := os.WriteFile(unitPath, []byte(`[Unit]
Description=OpenClaw fixture

[Service]
Type=oneshot
ExecStart=/bin/sh -c 'printf fixture-started'
`), 0o644); err != nil {
		t.Fatalf("write service fixture: %v", err)
	}

	output, err := runMirage(t, repoRoot,
		"doctor",
		"--rootfs", rootfs,
		"--runtime-mode", "init",
		"--command", "/bin/sh",
		"--service-unit", "openclaw.service",
	)
	if err != nil {
		t.Fatalf("expected init doctor to succeed: %v\noutput:\n%s", err, output)
	}

	for _, needle := range []string{
		"resolved init command: /bin/sh",
		"systemd machine-id: /etc/machine-id",
		"systemd unit openclaw.service: ok (/etc/systemd/system/openclaw.service)",
		"init rootfs validation: ok",
	} {
		if !strings.Contains(output, needle) {
			t.Fatalf("expected doctor output to contain %q, got:\n%s", needle, output)
		}
	}
}

func TestSandboxLifecycleForInitModeShellE2E(t *testing.T) {
	requireNamespaceBackend(t)
	requireCgroupBackend(t)

	repoRoot := projectRoot(t)
	rootfs := prepareInitRootfs(t, repoRoot)
	stateDir := t.TempDir()
	name := "demo"

	_, err := runMirage(t, repoRoot,
		"sandbox",
		"start",
		"--name", name,
		"--state-dir", stateDir,
		"--rootfs", rootfs,
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
		"--",
		"/bin/sh", "-c", "echo shell-ready; trap 'exit 0' TERM; while :; do sleep 1; done",
	)
	if err != nil {
		t.Fatalf("expected sandbox start to succeed: %v", err)
	}
	defer func() {
		_, _ = runMirage(t, repoRoot, "sandbox", "stop", "--name", name, "--state-dir", stateDir, "--timeout", "2s")
	}()

	if err := waitForSandboxState(t, repoRoot, stateDir, name, "active"); err != nil {
		t.Fatal(err)
	}

	logs, err := runMirage(t, repoRoot,
		"sandbox",
		"logs",
		"--name", name,
		"--state-dir", stateDir,
		"--stdout",
		"--lines", "20",
	)
	if err != nil {
		t.Fatalf("expected sandbox logs to succeed: %v\noutput:\n%s", err, logs)
	}
	if !strings.Contains(logs, "shell-ready") {
		t.Fatalf("expected sandbox stdout logs to contain shell marker, got:\n%s", logs)
	}

	stopOutput, err := runMirage(t, repoRoot,
		"sandbox",
		"stop",
		"--name", name,
		"--state-dir", stateDir,
		"--timeout", "2s",
	)
	if err != nil {
		t.Fatalf("expected sandbox stop to succeed: %v\noutput:\n%s", err, stopOutput)
	}
	if !strings.Contains(stopOutput, "active-state: inactive") {
		t.Fatalf("expected sandbox stop output to report inactive state, got:\n%s", stopOutput)
	}
}

func TestSandboxStartReportsMissingInitContractE2E(t *testing.T) {
	repoRoot := projectRoot(t)
	rootfs := filepath.Join(t.TempDir(), "basic-rootfs")

	output, err := runMirage(t, repoRoot,
		"rootfs",
		"init",
		"--template", "basic",
		"--output", rootfs,
	)
	if err != nil {
		t.Fatalf("expected basic rootfs init to succeed: %v\noutput:\n%s", err, output)
	}

	output, err = runMirage(t, repoRoot,
		"sandbox",
		"start",
		"--name", "broken",
		"--state-dir", t.TempDir(),
		"--rootfs", rootfs,
		"--network-policy-file", policyFixturePath(repoRoot, "allow-all.yaml"),
		"--",
		"/bin/sh",
	)
	if err == nil {
		t.Fatalf("expected sandbox start to fail for incomplete init rootfs, got output:\n%s", output)
	}
	for _, needle := range []string{
		`runtime path "/usr/lib/systemd/system" is missing`,
		`systemd machine-id file "/etc/machine-id" is missing`,
	} {
		if !strings.Contains(output, needle) {
			t.Fatalf("expected sandbox start failure to mention %q, got:\n%s", needle, output)
		}
	}
}

func prepareInitRootfs(t *testing.T, repoRoot string) string {
	t.Helper()

	rootfs := filepath.Join(t.TempDir(), "init-rootfs")
	output, err := runMirage(t, repoRoot,
		"rootfs",
		"init",
		"--template", "basic",
		"--output", rootfs,
	)
	if err != nil {
		t.Fatalf("expected init rootfs preparation to succeed: %v\noutput:\n%s", err, output)
	}
	for _, dir := range []string{
		"dev",
		"sys",
		filepath.Join("sys", "fs"),
		filepath.Join("sys", "fs", "cgroup"),
		filepath.Join("etc", "systemd", "system"),
		filepath.Join("usr", "lib", "systemd", "system"),
	} {
		if err := os.MkdirAll(filepath.Join(rootfs, dir), 0o755); err != nil {
			t.Fatalf("prepare init directory %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(rootfs, "etc", "machine-id"), []byte("0123456789abcdef0123456789abcdef\n"), 0o644); err != nil {
		t.Fatalf("write machine-id: %v", err)
	}
	return rootfs
}

func waitForSandboxState(t *testing.T, repoRoot string, stateDir string, name string, want string) error {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		output, err := runMirage(t, repoRoot, "sandbox", "status", "--name", name, "--state-dir", stateDir)
		if err == nil && strings.Contains(output, "active-state: "+want) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	output, err := runMirage(t, repoRoot, "sandbox", "status", "--name", name, "--state-dir", stateDir)
	if err != nil {
		return err
	}
	return &sandboxWaitError{output: output, want: want}
}

type sandboxWaitError struct {
	output string
	want   string
}

func (e *sandboxWaitError) Error() string {
	return "sandbox did not reach active-state " + e.want + ":\n" + e.output
}
