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

func installBuiltInTemplate(t *testing.T, template rootfs.Template) {
	t.Helper()

	previous, existed := rootfs.BuiltInTemplates[template.Name]
	rootfs.BuiltInTemplates[template.Name] = template
	t.Cleanup(func() {
		if existed {
			rootfs.BuiltInTemplates[template.Name] = previous
			return
		}
		delete(rootfs.BuiltInTemplates, template.Name)
	})
}

func TestPresetList(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	if err := Run([]string{"preset", "list"}, &out, &errBuf); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	for _, name := range []string{"offline", "openclaw-offline"} {
		if !strings.Contains(got, name) {
			t.Fatalf("expected preset list to contain %q, got %q", name, got)
		}
	}
}

func TestPresetListWithPresetFile(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	presetFile := filepath.Join(t.TempDir(), "presets.yaml")
	if err := os.WriteFile(presetFile, []byte(`presets:
  - name: team-offline
    network: none
    description: Team preset
`), 0o644); err != nil {
		t.Fatalf("write preset file: %v", err)
	}

	if err := Run([]string{"preset", "list", "--preset-file", presetFile}, &out, &errBuf); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "team-offline") {
		t.Fatalf("expected preset list to contain team preset, got %q", got)
	}
}

func TestRunDryRun(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{
		"run",
		"--rootfs", "/srv/rootfs",
		"--preset", "offline",
		"--dry-run",
		"--",
		"echo", "hello",
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "preset: offline") {
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

	presetFile := filepath.Join(t.TempDir(), "presets.yaml")
	if err := os.WriteFile(presetFile, []byte(`presets:
  - name: team-offline
    network: none
    description: Team preset
`), 0o644); err != nil {
		t.Fatalf("write preset file: %v", err)
	}

	err := Run([]string{
		"run",
		"--rootfs", "/srv/rootfs",
		"--preset-file", presetFile,
		"--preset", "team-offline",
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
	if !strings.Contains(got, "preset: team-offline") || !strings.Contains(got, "net: none") {
		t.Fatalf("expected dry run output to mention simplified preset networking, got %q", got)
	}
}

func TestRunRejectsUnsupportedNetworkMode(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{
		"run",
		"--rootfs", "/srv/rootfs",
		"--net", "isolated",
		"--",
		"echo", "hello",
	}, &out, &errBuf)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), `invalid network mode "isolated"`) {
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
	if !strings.Contains(err.Error(), "runtime-mode init requires a dedicated rootfs") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunRejectsInitModeWithUnsupportedNetworkMode(t *testing.T) {
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
	if !strings.Contains(err.Error(), `invalid network mode "isolated"`) {
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

func TestRunRejectsManagedRuntimeMountTargetInInitMode(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{
		"run",
		"--rootfs", "/srv/rootfs",
		"--net", "host",
		"--runtime-mode", "init",
		"--ro-bind", "/host/path:/dev/null",
		"--",
		"/usr/lib/systemd/systemd",
	}, &out, &errBuf)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), `runtime-mode init manages guest path "/dev/null"`) {
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

func TestRootfsInitAllowOverwrite(t *testing.T) {
	const templateName = "test-allow-overwrite"
	rootfs.BuiltInTemplates[templateName] = rootfs.Template{
		Version:     rootfs.TemplateVersionV1,
		Name:        templateName,
		Description: "Test template for overwrite behavior",
		GeneratedFiles: []rootfs.GeneratedFile{
			{TargetPath: "/etc/demo.conf", Content: "updated=yes\n", Mode: 0o600},
		},
	}
	t.Cleanup(func() {
		delete(rootfs.BuiltInTemplates, templateName)
	})

	outputRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outputRoot, "etc"), 0o755); err != nil {
		t.Fatalf("create existing etc dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outputRoot, "etc", "demo.conf"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("write existing target file: %v", err)
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	err := Run([]string{
		"rootfs",
		"init",
		"--template", templateName,
		"--output", outputRoot,
		"--allow-overwrite",
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outputRoot, "etc", "demo.conf"))
	if err != nil {
		t.Fatalf("read overwritten file: %v", err)
	}
	if string(data) != "updated=yes\n" {
		t.Fatalf("expected overwritten file content, got %q", string(data))
	}
}

func TestRootfsInitReportsMissingAssets(t *testing.T) {
	const templateName = "test-missing-assets"
	installBuiltInTemplate(t, rootfs.Template{
		Version:     rootfs.TemplateVersionV1,
		Name:        templateName,
		Description: "Test template for missing host assets",
		Directories: []rootfs.Directory{{Path: "/work", Mode: 0o755}},
		RuntimeFiles: []rootfs.RuntimeFile{
			{
				HostPath:   filepath.Join(t.TempDir(), "missing-hosts"),
				TargetPath: "/etc/hosts",
			},
		},
	})

	var out bytes.Buffer
	var errBuf bytes.Buffer
	outputRoot := filepath.Join(t.TempDir(), "rootfs")
	err := Run([]string{
		"rootfs",
		"init",
		"--template", templateName,
		"--output", outputRoot,
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "warnings: 1") {
		t.Fatalf("expected rootfs init output to include warning count, got %q", got)
	}
	if !strings.Contains(got, `warning: missing host asset "`) || !strings.Contains(got, `for "/etc/hosts"`) {
		t.Fatalf("expected rootfs init output to report missing asset, got %q", got)
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
		Preset: "openclaw-offline",
	}
	var errBuf bytes.Buffer
	if err := ensurePresetRootfs(cfg, &errBuf); err != nil {
		t.Fatalf("ensurePresetRootfs returned error: %v", err)
	}

	for _, target := range []string{"usr/bin/node", "usr/bin/npm", "workspace", "home"} {
		if _, err := os.Stat(filepath.Join(rootfsPath, target)); err != nil {
			t.Fatalf("expected generated target %q to exist: %v", target, err)
		}
	}
}

func TestEnsurePresetRootfsReportsMissingAssets(t *testing.T) {
	const templateName = "test-preset-missing-assets"
	installBuiltInTemplate(t, rootfs.Template{
		Version:     rootfs.TemplateVersionV1,
		Name:        templateName,
		Description: "Test template for preset-generated missing assets",
		Directories: []rootfs.Directory{{Path: "/work", Mode: 0o755}},
		RuntimeFiles: []rootfs.RuntimeFile{
			{
				HostPath:   filepath.Join(t.TempDir(), "missing-hosts"),
				TargetPath: "/etc/hosts",
			},
		},
	})

	presetFile := filepath.Join(t.TempDir(), "presets.yaml")
	if err := os.WriteFile(presetFile, []byte(`presets:
  - name: team-missing
    network: none
    rootfs:
      template: `+templateName+`
`), 0o644); err != nil {
		t.Fatalf("write preset file: %v", err)
	}

	rootfsPath := filepath.Join(t.TempDir(), "preset-rootfs")
	cfg := spec.Config{
		RootFS:     rootfsPath,
		Preset:     "team-missing",
		PresetFile: presetFile,
	}

	var errBuf bytes.Buffer
	if err := ensurePresetRootfs(cfg, &errBuf); err != nil {
		t.Fatalf("ensurePresetRootfs returned error: %v", err)
	}

	got := errBuf.String()
	if !strings.Contains(got, "warnings: 1") {
		t.Fatalf("expected preset rootfs warning count, got %q", got)
	}
	if !strings.Contains(got, "warning: preset rootfs missing host asset") {
		t.Fatalf("expected preset rootfs warning output, got %q", got)
	}
}

func TestDoctorReportsStableNetworkModes(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	if err := Run([]string{"doctor"}, &out, &errBuf); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "stable network modes: host, none") {
		t.Fatalf("expected doctor output to report stable network modes, got %q", got)
	}
}

func TestDoctorRejectsInvalidRuntimeMode(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{"doctor", "--rootfs", "/tmp/rootfs", "--runtime-mode", "invalid"}, &out, &errBuf)
	if err == nil {
		t.Fatal("expected invalid runtime-mode error")
	}
	if !strings.Contains(err.Error(), `invalid runtime-mode "invalid"`) {
		t.Fatalf("unexpected error: %v", err)
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

func TestDoctorPrintsDeduplicatedPresetCommands(t *testing.T) {
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

	presetFile := filepath.Join(t.TempDir(), "presets.yaml")
	if err := os.WriteFile(presetFile, []byte(`presets:
  - name: team-basic
    network: none
    rootfs:
      required_commands:
        - " /bin/ls "
        - /bin/sh
        - /bin/ls
        - ""
    description: Basic rootfs validation preset.
`), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	err := Run([]string{
		"doctor",
		"--rootfs", rootfsPath,
		"--preset-file", presetFile,
		"--preset", "team-basic",
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "- preset required rootfs commands: /bin/ls, /bin/sh") {
		t.Fatalf("expected deduplicated preset command list, got %q", got)
	}
	if strings.Count(got, "- preset required command /bin/ls: ok") != 1 {
		t.Fatalf("expected /bin/ls to be validated once, got %q", got)
	}
}

func TestDoctorValidatesInitRootfs(t *testing.T) {
	systemdPath, err := exec.LookPath("systemd")
	if err != nil {
		t.Skip("host PATH does not contain systemd")
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer

	rootfsPath := filepath.Join(t.TempDir(), "rootfs")
	err = rootfs.Generate(rootfsPath, rootfs.Template{
		Version:     rootfs.TemplateVersionV1,
		Name:        "custom-systemd",
		Description: "Systemd-ready test rootfs",
		Directories: []rootfs.Directory{
			{Path: "/proc", Mode: 0o755},
			{Path: "/tmp", Mode: 0o1777},
			{Path: "/run", Mode: 0o755},
			{Path: "/dev", Mode: 0o755},
			{Path: "/sys", Mode: 0o755},
			{Path: "/sys/fs/cgroup", Mode: 0o755},
			{Path: "/etc/systemd/system", Mode: 0o755},
			{Path: "/usr/lib/systemd/system", Mode: 0o755},
		},
		Binaries: []rootfs.Binary{
			{HostPath: systemdPath, TargetPath: "/usr/bin/systemd", CopyDependencies: true},
		},
		GeneratedFiles: []rootfs.GeneratedFile{
			{TargetPath: "/etc/machine-id", Mode: 0o644},
			{TargetPath: "/etc/systemd/system/openclaw.service", Content: "[Unit]\nDescription=OpenClaw\n[Service]\nExecStart=/usr/bin/true\n", Mode: 0o644},
		},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	err = Run([]string{
		"doctor",
		"--rootfs", rootfsPath,
		"--runtime-mode", "init",
		"--service-unit", "openclaw.service",
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	for _, needle := range []string{
		"resolved init command: /usr/bin/systemd",
		"systemd machine-id: /etc/machine-id",
		"systemd unit openclaw.service: ok (/etc/systemd/system/openclaw.service)",
		"init rootfs validation: ok",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected doctor output to contain %q, got %q", needle, got)
		}
	}
}

func TestDoctorRejectsMissingInitServiceUnit(t *testing.T) {
	systemdPath, err := exec.LookPath("systemd")
	if err != nil {
		t.Skip("host PATH does not contain systemd")
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer

	rootfsPath := filepath.Join(t.TempDir(), "rootfs")
	err = rootfs.Generate(rootfsPath, rootfs.Template{
		Version:     rootfs.TemplateVersionV1,
		Name:        "custom-systemd",
		Description: "Systemd-ready test rootfs",
		Directories: []rootfs.Directory{
			{Path: "/proc", Mode: 0o755},
			{Path: "/tmp", Mode: 0o1777},
			{Path: "/run", Mode: 0o755},
			{Path: "/dev", Mode: 0o755},
			{Path: "/sys", Mode: 0o755},
			{Path: "/sys/fs/cgroup", Mode: 0o755},
			{Path: "/etc/systemd/system", Mode: 0o755},
			{Path: "/usr/lib/systemd/system", Mode: 0o755},
		},
		Binaries: []rootfs.Binary{
			{HostPath: systemdPath, TargetPath: "/usr/bin/systemd", CopyDependencies: true},
		},
		GeneratedFiles: []rootfs.GeneratedFile{
			{TargetPath: "/etc/machine-id", Mode: 0o644},
		},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	err = Run([]string{
		"doctor",
		"--rootfs", rootfsPath,
		"--runtime-mode", "init",
		"--service-unit", "openclaw.service",
	}, &out, &errBuf)
	if err == nil {
		t.Fatal("expected init doctor validation to fail on missing service unit")
	}
	if !strings.Contains(err.Error(), `systemd unit "openclaw.service" was not found`) {
		t.Fatalf("unexpected error: %v", err)
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
		"--preset", "openclaw-offline",
	}, &out, &errBuf)
	if err == nil {
		t.Fatal("expected preset rootfs validation to fail on missing node")
	}

	got := out.String()
	if !strings.Contains(got, "preset recommended rootfs template: openclaw-developer") {
		t.Fatalf("expected preset template hint, got %q", got)
	}
	if !strings.Contains(err.Error(), `command "node"`) {
		t.Fatalf("expected missing preset command error, got %v", err)
	}
}
