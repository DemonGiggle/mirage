package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DemonGiggle/mirage/internal/rootfs"
	"github.com/DemonGiggle/mirage/internal/spec"
)

func TestPresetList(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	if err := Run([]string{"preset", "list"}, &out, &errBuf); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	for _, name := range []string{"offline", "github", "openai", "openclaw-offline", "openclaw-openai"} {
		if !strings.Contains(got, name) {
			t.Fatalf("expected preset list to contain %q, got %q", name, got)
		}
	}
}

func TestPresetListWithPresetFile(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	presetFile := filepath.Join(t.TempDir(), "presets.json")
	if err := os.WriteFile(presetFile, []byte(`{
  "presets": [
    {
      "name": "team-openai",
      "network": "isolated",
      "allow_hosts": ["example.com:443"],
      "description": "Team preset"
    }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write preset file: %v", err)
	}

	if err := Run([]string{"preset", "list", "--preset-file", presetFile}, &out, &errBuf); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "team-openai") {
		t.Fatalf("expected preset list to contain team preset, got %q", got)
	}
}

func TestRunDryRun(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{
		"run",
		"--rootfs", "/srv/rootfs",
		"--preset", "openai",
		"--warn", "net",
		"--dry-run",
		"--",
		"echo", "hello",
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "preset: openai") {
		t.Fatalf("expected dry run output to mention preset, got %q", got)
	}
	if !strings.Contains(got, "execution: skipped (--dry-run)") {
		t.Fatalf("expected dry run output to mention skipped execution, got %q", got)
	}
	if !strings.Contains(got, "note: execution backend: linux namespace runner") {
		t.Fatalf("expected dry run output to mention execution backend, got %q", got)
	}
	if !strings.Contains(got, "note: one sandbox = one isolated process tree") {
		t.Fatalf("expected dry run output to mention process tree model, got %q", got)
	}
	if !strings.Contains(got, "runtime-mode: direct") {
		t.Fatalf("expected dry run output to mention runtime mode, got %q", got)
	}
}

func TestRunDryRunWithPresetFile(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	presetFile := filepath.Join(t.TempDir(), "presets.json")
	if err := os.WriteFile(presetFile, []byte(`{
  "presets": [
    {
      "name": "team-openai",
      "network": "isolated",
      "allow_hosts": ["example.com:443"],
      "description": "Team preset"
    }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write preset file: %v", err)
	}

	err := Run([]string{
		"run",
		"--rootfs", "/srv/rootfs",
		"--preset-file", presetFile,
		"--preset", "team-openai",
		"--dry-run",
		"--",
		"echo", "hello",
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "preset-file: "+presetFile) {
		t.Fatalf("expected dry run output to mention preset file, got %q", got)
	}
	if !strings.Contains(got, "allow-host:") || !strings.Contains(got, "example.com:443") {
		t.Fatalf("expected dry run output to mention preset host, got %q", got)
	}
}

func TestRunRejectsAllowRulesWithNetNone(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{
		"run",
		"--rootfs", "/srv/rootfs",
		"--net", "none",
		"--allow-host", "github.com:443",
		"--",
		"echo", "hello",
	}, &out, &errBuf)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "allow rules are incompatible with --net none") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunRejectsInitModeWithHostRootfs(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{
		"run",
		"--rootfs", "/",
		"--net", "host",
		"--runtime-mode", "init",
		"--",
		"/usr/lib/systemd/systemd",
	}, &out, &errBuf)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "runtime-mode init currently requires a dedicated rootfs") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunRejectsInitModeWithObservedNetworking(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{
		"run",
		"--rootfs", "/srv/rootfs",
		"--net", "isolated",
		"--runtime-mode", "init",
		"--",
		"/usr/lib/systemd/systemd",
	}, &out, &errBuf)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "runtime-mode init is incompatible with observed networking") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunRejectsBindMountOverGuestCgroupTreeInInitMode(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{
		"run",
		"--rootfs", "/srv/rootfs",
		"--net", "host",
		"--runtime-mode", "init",
		"--rw-bind", "/host/path:/sys/fs/cgroup",
		"--",
		"/usr/lib/systemd/systemd",
	}, &out, &errBuf)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), `runtime-mode init reserves guest path "/sys/fs/cgroup"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunDryRunWithInitMode(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{
		"run",
		"--rootfs", "/srv/rootfs",
		"--net", "host",
		"--runtime-mode", "init",
		"--dry-run",
		"--",
		"/usr/lib/systemd/systemd",
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	for _, needle := range []string{
		"runtime-mode: init",
		"note: execution mode: guest init command becomes sandbox PID 1",
		"note: one sandbox = one isolated process tree rooted at guest init",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected dry run output to contain %q, got %q", needle, got)
		}
	}
}

func TestRunDryRunShowsLogExport(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	stdoutLog := filepath.Join(t.TempDir(), "stdout.log")

	err := Run([]string{
		"run",
		"--rootfs", "/srv/rootfs",
		"--net", "host",
		"--stdout-log", stdoutLog,
		"--dry-run",
		"--",
		"echo", "hello",
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "stdout-log: "+stdoutLog) {
		t.Fatalf("expected stdout log path in preview, got %q", got)
	}
	if !strings.Contains(got, "note: host log export: stdout") {
		t.Fatalf("expected host log export note, got %q", got)
	}
}

func TestRootfsInit(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	outputRoot := filepath.Join(t.TempDir(), "rootfs")
	err := Run([]string{
		"rootfs",
		"init",
		"--template", "basic",
		"--output", outputRoot,
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "mirage rootfs init") || !strings.Contains(got, "template: basic") {
		t.Fatalf("expected rootfs init output, got %q", got)
	}
	for _, target := range []string{"bin/ls", "bin/sh", "proc", "tmp", "run"} {
		if _, err := os.Stat(filepath.Join(outputRoot, target)); err != nil {
			t.Fatalf("expected generated target %q to exist: %v", target, err)
		}
	}
}

func TestEnsurePresetRootfsAutoGeneratesRecommendedTemplate(t *testing.T) {
	for _, binary := range []string{"node", "npm", "bash", "git"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("host PATH does not contain %s", binary)
		}
	}

	rootfsPath := filepath.Join(t.TempDir(), "openclaw-rootfs")
	cfg := spec.Config{
		RootFS: rootfsPath,
		Preset: "openclaw-openai",
	}
	if err := ensurePresetRootfs(cfg); err != nil {
		t.Fatalf("ensurePresetRootfs returned error: %v", err)
	}

	for _, target := range []string{"usr/bin/node", "usr/bin/npm", "workspace", "home"} {
		if _, err := os.Stat(filepath.Join(rootfsPath, target)); err != nil {
			t.Fatalf("expected generated target %q to exist: %v", target, err)
		}
	}
}

func TestDoctorReportsObservedNetworkingUnavailableWithoutStrace(t *testing.T) {
	t.Setenv("PATH", "")

	var out bytes.Buffer
	var errBuf bytes.Buffer

	if err := Run([]string{"doctor"}, &out, &errBuf); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "observed isolated networking: unavailable") {
		t.Fatalf("expected doctor output to report observed networking as unavailable, got %q", got)
	}
	if !strings.Contains(got, "requires strace on PATH") {
		t.Fatalf("expected doctor output to mention missing strace, got %q", got)
	}
}

func TestDoctorValidatesRootfsCommand(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	rootfsPath := filepath.Join(t.TempDir(), "rootfs")
	template, ok := rootfs.LookupTemplate("basic")
	if !ok {
		t.Fatal("expected basic template to exist")
	}
	if err := rootfs.Generate(rootfsPath, template); err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	err := Run([]string{
		"doctor",
		"--rootfs", rootfsPath,
		"--command", "/bin/ls",
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	for _, needle := range []string{
		"resolved command: /bin/ls",
		"rootfs validation: ok",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected doctor output to contain %q, got %q", needle, got)
		}
	}
}

func TestDoctorUsesPresetRootfsRequirements(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	rootfsPath := filepath.Join(t.TempDir(), "rootfs")
	template, ok := rootfs.LookupTemplate("basic")
	if !ok {
		t.Fatal("expected basic template to exist")
	}
	if err := rootfs.Generate(rootfsPath, template); err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	err := Run([]string{
		"doctor",
		"--rootfs", rootfsPath,
		"--preset", "openclaw-openai",
	}, &out, &errBuf)
	if err == nil {
		t.Fatal("expected preset rootfs validation to fail on missing node")
	}

	got := out.String()
	if !strings.Contains(got, "preset recommended rootfs template: openclaw") {
		t.Fatalf("expected preset template hint, got %q", got)
	}
	if !strings.Contains(err.Error(), `command "node"`) {
		t.Fatalf("expected missing preset command error, got %v", err)
	}
}
