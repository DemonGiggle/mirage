package rootfs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRootfsPassesForGeneratedShellRootfs(t *testing.T) {
	rootfs, _, _, dependencyPath := generatedShellRootfs(t)

	report, err := ValidateRootfs(rootfs, "/bin/sh", "")
	if err != nil {
		t.Fatalf("ValidateRootfs returned error: %v", err)
	}
	if report.ResolvedCommand != "/bin/sh" {
		t.Fatalf("unexpected resolved command: %#v", report)
	}
	if report.DependencyCount == 0 && dependencyPath != "" {
		t.Fatalf("expected dependency count to be tracked, got %#v", report)
	}
}

func TestValidateRootfsAllowsCreatableRuntimePaths(t *testing.T) {
	rootfs := filepath.Join(t.TempDir(), "rootfs")
	if err := os.MkdirAll(rootfs, 0o755); err != nil {
		t.Fatalf("create rootfs: %v", err)
	}

	report, err := ValidateRootfs(rootfs, "", "")
	if err != nil {
		t.Fatalf("ValidateRootfs returned error: %v", err)
	}
	for _, status := range report.RuntimePaths {
		if status.Status != "creatable" {
			t.Fatalf("expected runtime path %q to be creatable, got %#v", status.Path, status)
		}
	}
}

func TestValidateRootfsRejectsMissingCommand(t *testing.T) {
	rootfs := filepath.Join(t.TempDir(), "rootfs")
	if err := os.MkdirAll(rootfs, 0o755); err != nil {
		t.Fatalf("create rootfs: %v", err)
	}

	_, err := ValidateRootfs(rootfs, "/bin/missing", "")
	if err == nil || !strings.Contains(err.Error(), `command "/bin/missing" does not exist inside rootfs`) {
		t.Fatalf("expected missing command error, got %v", err)
	}
}

func TestValidateRootfsRejectsMissingELFInterpreter(t *testing.T) {
	rootfs, _, interpreterPath, _ := generatedShellRootfs(t)
	if interpreterPath == "" {
		t.Skip("host shell does not use an ELF interpreter")
	}
	if err := os.Remove(rootPath(rootfs, interpreterPath)); err != nil {
		t.Fatalf("remove interpreter: %v", err)
	}

	_, err := ValidateRootfs(rootfs, "/bin/sh", "")
	if err == nil || !strings.Contains(err.Error(), "ELF interpreter") {
		t.Fatalf("expected missing interpreter error, got %v", err)
	}
}

func TestValidateRootfsRejectsMissingSharedLibrary(t *testing.T) {
	rootfs, _, _, dependencyPath := generatedShellRootfs(t)
	if dependencyPath == "" {
		t.Skip("host shell does not have a removable shared library dependency")
	}
	if err := os.Remove(rootPath(rootfs, dependencyPath)); err != nil {
		t.Fatalf("remove dependency: %v", err)
	}

	_, err := ValidateRootfs(rootfs, "/bin/sh", "")
	if err == nil || !strings.Contains(err.Error(), "shared library dependencies") {
		t.Fatalf("expected missing shared library error, got %v", err)
	}
}

func TestValidateInitRootfsPassesForSystemdReadyRootfs(t *testing.T) {
	systemdPath, err := exec.LookPath("systemd")
	if err != nil {
		t.Skip("host PATH does not contain systemd")
	}

	rootfs := filepath.Join(t.TempDir(), "rootfs")
	template := Template{
		Version:     TemplateVersionV1,
		Name:        "custom-systemd",
		Description: "Systemd-ready test rootfs",
		Directories: []Directory{
			{Path: "/proc", Mode: 0o755},
			{Path: "/tmp", Mode: 0o1777},
			{Path: "/run", Mode: 0o755},
			{Path: "/dev", Mode: 0o755},
			{Path: "/sys", Mode: 0o755},
			{Path: "/sys/fs/cgroup", Mode: 0o755},
			{Path: "/etc/systemd/system", Mode: 0o755},
			{Path: "/usr/lib/systemd/system", Mode: 0o755},
		},
		Binaries: []Binary{
			{HostPath: systemdPath, TargetPath: "/usr/bin/systemd", CopyDependencies: true},
		},
		GeneratedFiles: []GeneratedFile{
			{TargetPath: "/etc/machine-id", Mode: 0o644},
			{TargetPath: "/etc/systemd/system/openclaw.service", Content: "[Unit]\nDescription=OpenClaw\n[Service]\nExecStart=/usr/bin/true\n", Mode: 0o644},
		},
	}
	if err := Generate(rootfs, template); err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	report, err := ValidateInitRootfs(rootfs, "/usr/bin/systemd", "openclaw.service")
	if err != nil {
		t.Fatalf("ValidateInitRootfs returned error: %v", err)
	}
	if report.ResolvedInit != "/usr/bin/systemd" {
		t.Fatalf("unexpected resolved init: %#v", report)
	}
	if report.MachineIDPath != "/etc/machine-id" {
		t.Fatalf("unexpected machine-id path: %#v", report)
	}
	if report.ServiceUnitPath != "/etc/systemd/system/openclaw.service" {
		t.Fatalf("unexpected service unit path: %#v", report)
	}
}

func generatedShellRootfs(t *testing.T) (string, string, string, string) {
	t.Helper()

	shellPath, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("host PATH does not contain sh")
	}

	outputRoot := filepath.Join(t.TempDir(), "rootfs")
	template := Template{
		Version:     TemplateVersionV1,
		Name:        "custom",
		Description: "Custom template",
		Binaries: []Binary{
			{
				HostPath:         shellPath,
				TargetPath:       "/bin/sh",
				CopyDependencies: true,
			},
		},
	}
	if err := Generate(outputRoot, template); err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	inspection, isELF, err := inspectELFBinary(shellPath, "/bin/sh")
	if err != nil {
		t.Fatalf("inspectELFBinary returned error: %v", err)
	}
	if !isELF {
		t.Skip("host shell is not an ELF binary")
	}

	var dependencyPath string
	for _, library := range inspection.Libraries {
		found, ok, err := findLibraryInRootfs(outputRoot, library, inspection.SearchPaths)
		if err != nil {
			t.Fatalf("findLibraryInRootfs returned error: %v", err)
		}
		if ok && found != inspection.Interpreter {
			dependencyPath = found
			break
		}
	}
	return outputRoot, shellPath, inspection.Interpreter, dependencyPath
}
