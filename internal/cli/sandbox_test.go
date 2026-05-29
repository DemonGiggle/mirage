package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DemonGiggle/mirage/internal/rootfs"
)

func TestSandboxStartWritesStateAndLaunchesNamedScope(t *testing.T) {
	stateDir := t.TempDir()
	rootfsPath := filepath.Join(t.TempDir(), "rootfs")
	if err := os.MkdirAll(rootfsPath, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}

	origValidate := sandboxValidateInitRootfs
	origLaunch := sandboxLaunchProcess
	origShow := sandboxShowUserUnit
	origStateRoot := sandboxStateRootDir
	t.Cleanup(func() {
		sandboxValidateInitRootfs = origValidate
		sandboxLaunchProcess = origLaunch
		sandboxShowUserUnit = origShow
		sandboxStateRootDir = origStateRoot
	})

	sandboxStateRootDir = func(override string) (string, error) {
		return defaultSandboxStateRootDir(stateDir)
	}
	sandboxValidateInitRootfs = func(rootfsPath string, command string, serviceUnit string) (rootfs.InitValidationReport, error) {
		return rootfs.InitValidationReport{
			Rootfs:       rootfsPath,
			ResolvedInit: "/usr/lib/systemd/systemd",
		}, nil
	}

	var launched sandboxLaunchRequest
	sandboxLaunchProcess = func(req sandboxLaunchRequest) error {
		launched = req
		return nil
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	err := Run([]string{
		"sandbox",
		"start",
		"--name", "demo",
		"--state-dir", stateDir,
		"--rootfs", rootfsPath,
		"--service-unit", "openclaw.service",
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	state, err := readSandboxState(filepath.Join(stateDir, "demo", "state.json"))
	if err != nil {
		t.Fatalf("readSandboxState returned error: %v", err)
	}
	if state.Unit != "mirage-sandbox-demo.scope" {
		t.Fatalf("expected named scope unit, got %#v", state)
	}
	if len(state.Command) != 1 || state.Command[0] != "/usr/lib/systemd/systemd" {
		t.Fatalf("expected resolved init command in state, got %#v", state.Command)
	}

	got := strings.Join(launched.RunArgs, " ")
	for _, needle := range []string{
		"run --rootfs " + rootfsPath,
		"--runtime-mode init",
		"--scope-name mirage-sandbox-demo.scope",
		"--preset allow-all",
		"--stdout-log " + filepath.Join(stateDir, "demo", "stdout.log"),
		"--stderr-log " + filepath.Join(stateDir, "demo", "stderr.log"),
		"-- /usr/lib/systemd/systemd",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected launched args to contain %q, got %q", needle, got)
		}
	}

	output := out.String()
	if !strings.Contains(output, "service-unit: openclaw.service") || !strings.Contains(output, "status: starting") {
		t.Fatalf("expected sandbox start output, got %q", output)
	}
}

func TestSandboxStartForwardsNetworkPolicyFile(t *testing.T) {
	stateDir := t.TempDir()
	rootfsPath := filepath.Join(t.TempDir(), "rootfs")
	if err := os.MkdirAll(rootfsPath, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}

	origValidate := sandboxValidateInitRootfs
	origLaunch := sandboxLaunchProcess
	origStateRoot := sandboxStateRootDir
	t.Cleanup(func() {
		sandboxValidateInitRootfs = origValidate
		sandboxLaunchProcess = origLaunch
		sandboxStateRootDir = origStateRoot
	})

	sandboxStateRootDir = func(override string) (string, error) {
		return defaultSandboxStateRootDir(stateDir)
	}
	sandboxValidateInitRootfs = func(rootfsPath string, command string, serviceUnit string) (rootfs.InitValidationReport, error) {
		return rootfs.InitValidationReport{
			Rootfs:       rootfsPath,
			ResolvedInit: "/usr/lib/systemd/systemd",
		}, nil
	}

	var launched sandboxLaunchRequest
	sandboxLaunchProcess = func(req sandboxLaunchRequest) error {
		launched = req
		return nil
	}

	policyFile := filepath.Join("..", "..", "testdata", "network-policies", "offline.yaml")
	var out bytes.Buffer
	var errBuf bytes.Buffer
	err := Run([]string{
		"sandbox",
		"start",
		"--name", "demo-policy",
		"--state-dir", stateDir,
		"--rootfs", rootfsPath,
		"--network-policy-file", policyFile,
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := strings.Join(launched.RunArgs, " ")
	if !strings.Contains(got, "--network-policy-file "+policyFile) {
		t.Fatalf("expected launched args to forward network policy file, got %q", got)
	}
}

func TestSandboxStartRejectsRunningSandboxName(t *testing.T) {
	stateDir := t.TempDir()
	origShow := sandboxShowUserUnit
	origStateRoot := sandboxStateRootDir
	t.Cleanup(func() {
		sandboxShowUserUnit = origShow
		sandboxStateRootDir = origStateRoot
	})
	sandboxStateRootDir = func(override string) (string, error) {
		return defaultSandboxStateRootDir(stateDir)
	}
	sandboxShowUserUnit = func(unit string) (sandboxUnitStatus, error) {
		return sandboxUnitStatus{LoadState: "loaded", ActiveState: "active", SubState: "running"}, nil
	}

	sandboxDir := filepath.Join(stateDir, "demo")
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := writeSandboxState(filepath.Join(sandboxDir, "state.json"), sandboxState{
		Name:      "demo",
		Unit:      "mirage-sandbox-demo.scope",
		RootFS:    "/srv/rootfs",
		StdoutLog: filepath.Join(sandboxDir, "stdout.log"),
		StderrLog: filepath.Join(sandboxDir, "stderr.log"),
		LaunchLog: filepath.Join(sandboxDir, "launch.log"),
		Command:   []string{"/usr/lib/systemd/systemd"},
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("writeSandboxState returned error: %v", err)
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	err := Run([]string{
		"sandbox",
		"start",
		"--name", "demo",
		"--state-dir", stateDir,
		"--rootfs", filepath.Join(t.TempDir(), "rootfs"),
	}, &out, &errBuf)
	if err == nil || !strings.Contains(err.Error(), `sandbox "demo" is already running`) {
		t.Fatalf("expected running sandbox name error, got %v", err)
	}
}

func TestSandboxStatusReportsTrackedUnit(t *testing.T) {
	stateDir := t.TempDir()
	origShow := sandboxShowUserUnit
	origStateRoot := sandboxStateRootDir
	t.Cleanup(func() {
		sandboxShowUserUnit = origShow
		sandboxStateRootDir = origStateRoot
	})
	sandboxStateRootDir = func(override string) (string, error) {
		return defaultSandboxStateRootDir(stateDir)
	}
	sandboxShowUserUnit = func(unit string) (sandboxUnitStatus, error) {
		return sandboxUnitStatus{
			LoadState:   "loaded",
			ActiveState: "active",
			SubState:    "running",
			Result:      "success",
		}, nil
	}

	sandboxDir := filepath.Join(stateDir, "demo")
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := writeSandboxState(filepath.Join(sandboxDir, "state.json"), sandboxState{
		Name:        "demo",
		Unit:        "mirage-sandbox-demo.scope",
		RootFS:      "/srv/rootfs",
		ServiceUnit: "openclaw.service",
		StdoutLog:   filepath.Join(sandboxDir, "stdout.log"),
		StderrLog:   filepath.Join(sandboxDir, "stderr.log"),
		LaunchLog:   filepath.Join(sandboxDir, "launch.log"),
		Command:     []string{"/usr/lib/systemd/systemd"},
		StartedAt:   time.Date(2026, time.May, 25, 1, 2, 3, 0, time.UTC),
	}); err != nil {
		t.Fatalf("writeSandboxState returned error: %v", err)
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := Run([]string{"sandbox", "status", "--name", "demo", "--state-dir", stateDir}, &out, &errBuf); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	got := out.String()
	for _, needle := range []string{
		"active-state: active",
		"sub-state: running",
		"result: success",
		"service-unit: openclaw.service",
		"command: /usr/lib/systemd/systemd",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected sandbox status output to contain %q, got %q", needle, got)
		}
	}
}

func TestSandboxStopStopsUnitAndUpdatesState(t *testing.T) {
	stateDir := t.TempDir()
	origShow := sandboxShowUserUnit
	origStop := sandboxStopUserUnit
	origKill := sandboxKillUserUnit
	origStateRoot := sandboxStateRootDir
	t.Cleanup(func() {
		sandboxShowUserUnit = origShow
		sandboxStopUserUnit = origStop
		sandboxKillUserUnit = origKill
		sandboxStateRootDir = origStateRoot
	})
	sandboxStateRootDir = func(override string) (string, error) {
		return defaultSandboxStateRootDir(stateDir)
	}

	showCalls := 0
	sandboxShowUserUnit = func(unit string) (sandboxUnitStatus, error) {
		showCalls++
		if showCalls == 1 {
			return sandboxUnitStatus{LoadState: "loaded", ActiveState: "active", SubState: "running"}, nil
		}
		return sandboxUnitStatus{LoadState: "loaded", ActiveState: "inactive", SubState: "dead"}, nil
	}

	var stoppedUnit string
	sandboxStopUserUnit = func(unit string) error {
		stoppedUnit = unit
		return nil
	}
	sandboxKillUserUnit = func(unit string) error {
		t.Fatalf("did not expect SIGKILL escalation for %s", unit)
		return nil
	}

	sandboxDir := filepath.Join(stateDir, "demo")
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	statePath := filepath.Join(sandboxDir, "state.json")
	if err := writeSandboxState(statePath, sandboxState{
		Name:      "demo",
		Unit:      "mirage-sandbox-demo.scope",
		RootFS:    "/srv/rootfs",
		StdoutLog: filepath.Join(sandboxDir, "stdout.log"),
		StderrLog: filepath.Join(sandboxDir, "stderr.log"),
		LaunchLog: filepath.Join(sandboxDir, "launch.log"),
		Command:   []string{"/usr/lib/systemd/systemd"},
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("writeSandboxState returned error: %v", err)
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := Run([]string{"sandbox", "stop", "--name", "demo", "--state-dir", stateDir, "--timeout", "1s"}, &out, &errBuf); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if stoppedUnit != "mirage-sandbox-demo.scope" {
		t.Fatalf("expected stop to target named scope, got %q", stoppedUnit)
	}

	updated, err := readSandboxState(statePath)
	if err != nil {
		t.Fatalf("readSandboxState returned error: %v", err)
	}
	if updated.StoppedAt.IsZero() {
		t.Fatalf("expected stop to persist stopped time, got %#v", updated)
	}
}

func TestSandboxLogsTailsStdoutAndStderr(t *testing.T) {
	stateDir := t.TempDir()
	origStateRoot := sandboxStateRootDir
	t.Cleanup(func() {
		sandboxStateRootDir = origStateRoot
	})
	sandboxStateRootDir = func(override string) (string, error) {
		return defaultSandboxStateRootDir(stateDir)
	}

	sandboxDir := filepath.Join(stateDir, "demo")
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	stdoutPath := filepath.Join(sandboxDir, "stdout.log")
	stderrPath := filepath.Join(sandboxDir, "stderr.log")
	if err := os.WriteFile(stdoutPath, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if err := os.WriteFile(stderrPath, []byte("err-one\nerr-two\n"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if err := writeSandboxState(filepath.Join(sandboxDir, "state.json"), sandboxState{
		Name:      "demo",
		Unit:      "mirage-sandbox-demo.scope",
		RootFS:    "/srv/rootfs",
		StdoutLog: stdoutPath,
		StderrLog: stderrPath,
		LaunchLog: filepath.Join(sandboxDir, "launch.log"),
		Command:   []string{"/usr/lib/systemd/systemd"},
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("writeSandboxState returned error: %v", err)
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := Run([]string{"sandbox", "logs", "--name", "demo", "--state-dir", stateDir, "--lines", "2"}, &out, &errBuf); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	got := out.String()
	for _, needle := range []string{
		"== stdout ==",
		"two",
		"three",
		"== stderr ==",
		"err-one",
		"err-two",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected sandbox logs output to contain %q, got %q", needle, got)
		}
	}
}

func TestSandboxStatusHandlesMissingUnit(t *testing.T) {
	stateDir := t.TempDir()
	origShow := sandboxShowUserUnit
	origStateRoot := sandboxStateRootDir
	t.Cleanup(func() {
		sandboxShowUserUnit = origShow
		sandboxStateRootDir = origStateRoot
	})
	sandboxStateRootDir = func(override string) (string, error) {
		return defaultSandboxStateRootDir(stateDir)
	}
	sandboxShowUserUnit = func(unit string) (sandboxUnitStatus, error) {
		return sandboxUnitStatus{}, errSandboxUnitNotFound
	}

	sandboxDir := filepath.Join(stateDir, "demo")
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := writeSandboxState(filepath.Join(sandboxDir, "state.json"), sandboxState{
		Name:      "demo",
		Unit:      "mirage-sandbox-demo.scope",
		RootFS:    "/srv/rootfs",
		StdoutLog: filepath.Join(sandboxDir, "stdout.log"),
		StderrLog: filepath.Join(sandboxDir, "stderr.log"),
		LaunchLog: filepath.Join(sandboxDir, "launch.log"),
		Command:   []string{"/usr/lib/systemd/systemd"},
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("writeSandboxState returned error: %v", err)
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := Run([]string{"sandbox", "status", "--name", "demo", "--state-dir", stateDir}, &out, &errBuf); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(out.String(), "sub-state: not-found") {
		t.Fatalf("expected missing-unit status output, got %q", out.String())
	}
}

func TestPrepareSandboxStatePathRejectsUnreadableExistingState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(statePath, []byte("{"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if err := prepareSandboxStatePath(statePath); err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Fatalf("expected unreadable state error, got %v", err)
	}
}

func TestTailFileMissingPath(t *testing.T) {
	got, err := tailFile(filepath.Join(t.TempDir(), "missing.log"), 10)
	if err != nil {
		t.Fatalf("tailFile returned error: %v", err)
	}
	if !strings.Contains(got, "does not exist") {
		t.Fatalf("expected missing log message, got %q", got)
	}
}

func TestSandboxStopReturnsUnexpectedShowError(t *testing.T) {
	stateDir := t.TempDir()
	origShow := sandboxShowUserUnit
	origStateRoot := sandboxStateRootDir
	t.Cleanup(func() {
		sandboxShowUserUnit = origShow
		sandboxStateRootDir = origStateRoot
	})
	sandboxStateRootDir = func(override string) (string, error) {
		return defaultSandboxStateRootDir(stateDir)
	}
	sandboxShowUserUnit = func(unit string) (sandboxUnitStatus, error) {
		return sandboxUnitStatus{}, errors.New("boom")
	}

	sandboxDir := filepath.Join(stateDir, "demo")
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := writeSandboxState(filepath.Join(sandboxDir, "state.json"), sandboxState{
		Name:      "demo",
		Unit:      "mirage-sandbox-demo.scope",
		RootFS:    "/srv/rootfs",
		StdoutLog: filepath.Join(sandboxDir, "stdout.log"),
		StderrLog: filepath.Join(sandboxDir, "stderr.log"),
		LaunchLog: filepath.Join(sandboxDir, "launch.log"),
		Command:   []string{"/usr/lib/systemd/systemd"},
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("writeSandboxState returned error: %v", err)
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := Run([]string{"sandbox", "stop", "--name", "demo", "--state-dir", stateDir}, &out, &errBuf); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected propagated status error, got %v", err)
	}
}
