package rootfs

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLDDOutput(t *testing.T) {
	output := []byte(`
linux-vdso.so.1 (0x00007ffd0d1f0000)
libselinux.so.1 => /lib/x86_64-linux-gnu/libselinux.so.1 (0x00007f96c3a9f000)
libc.so.6 => /lib/x86_64-linux-gnu/libc.so.6 (0x00007f96c38be000)
/lib64/ld-linux-x86-64.so.2 (0x00007f96c3b0e000)
`)

	got, err := parseLDDOutput(output)
	if err != nil {
		t.Fatalf("parseLDDOutput returned error: %v", err)
	}
	want := []string{
		"/lib/x86_64-linux-gnu/libselinux.so.1",
		"/lib/x86_64-linux-gnu/libc.so.6",
		"/lib64/ld-linux-x86-64.so.2",
	}
	if len(got) != len(want) {
		t.Fatalf("unexpected dependency count: got %d want %d (%v)", len(got), len(want), got)
	}
	for idx, dep := range want {
		if got[idx] != dep {
			t.Fatalf("unexpected dependency at %d: got %q want %q", idx, got[idx], dep)
		}
	}
}

func TestParseLDDOutputRejectsMissingDependency(t *testing.T) {
	_, err := parseLDDOutput([]byte("libmissing.so.1 => not found"))
	if err == nil || !strings.Contains(err.Error(), "missing shared library dependency") {
		t.Fatalf("expected missing dependency error, got %v", err)
	}
}

func TestGenerateCopiesFilesAndDependencies(t *testing.T) {
	shellPath, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("host PATH does not contain sh")
	}

	runtimeDir := t.TempDir()
	runtimeSource := filepath.Join(runtimeDir, "hosts")
	if err := os.WriteFile(runtimeSource, []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
		t.Fatalf("write runtime source: %v", err)
	}

	template := Template{
		Version:     TemplateVersionV1,
		Name:        "custom",
		Description: "Custom template",
		Directories: []Directory{{Path: "/work", Mode: 0o755}},
		Binaries: []Binary{
			{
				HostPath:         shellPath,
				TargetPath:       "/bin/sh",
				CopyDependencies: true,
			},
		},
		RuntimeFiles: []RuntimeFile{
			{
				HostPath:   runtimeSource,
				TargetPath: "/etc/hosts",
			},
		},
	}

	outputRoot := filepath.Join(t.TempDir(), "rootfs")
	if err := Generate(outputRoot, template); err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	for _, target := range []string{
		"/work",
		"/bin/sh",
		"/etc/hosts",
	} {
		if _, err := os.Stat(filepath.Join(outputRoot, strings.TrimPrefix(target, "/"))); err != nil {
			t.Fatalf("expected generated target %q to exist: %v", target, err)
		}
	}

	dependencies, err := lddDependencies(shellPath)
	if err != nil {
		t.Fatalf("lddDependencies returned error: %v", err)
	}
	if len(dependencies) == 0 {
		t.Fatalf("expected %q to have at least one dynamic dependency", shellPath)
	}
	firstDependency := filepath.Join(outputRoot, strings.TrimPrefix(dependencies[0], "/"))
	if _, err := os.Stat(firstDependency); err != nil {
		t.Fatalf("expected copied dependency %q to exist: %v", firstDependency, err)
	}
}

func TestGenerateHonorsCopyDependenciesFlag(t *testing.T) {
	shellPath, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("host PATH does not contain sh")
	}

	dependencies, err := lddDependencies(shellPath)
	if err != nil {
		t.Fatalf("lddDependencies returned error: %v", err)
	}
	if len(dependencies) == 0 {
		t.Fatalf("expected %q to have at least one dynamic dependency", shellPath)
	}

	outputRoot := filepath.Join(t.TempDir(), "rootfs")
	err = Generate(outputRoot, Template{
		Version:     TemplateVersionV1,
		Name:        "custom",
		Description: "Custom template",
		Binaries: []Binary{
			{
				HostPath:         shellPath,
				TargetPath:       "/bin/sh",
				CopyDependencies: false,
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(outputRoot, "bin", "sh")); err != nil {
		t.Fatalf("expected copied shell to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputRoot, strings.TrimPrefix(dependencies[0], "/"))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected dependency %q to be skipped when copy_dependencies is false, stat err=%v", dependencies[0], err)
	}
}

func TestGenerateRejectsNonEmptyOutputRoot(t *testing.T) {
	outputRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(outputRoot, "existing"), []byte("data"), 0o644); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	err := Generate(outputRoot, Template{
		Version:     TemplateVersionV1,
		Name:        "custom",
		Description: "Custom template",
	})
	if err == nil || !strings.Contains(err.Error(), "already exists and is not empty") {
		t.Fatalf("expected non-empty output root rejection, got %v", err)
	}
}

func TestGenerateWritesGeneratedFiles(t *testing.T) {
	outputRoot := filepath.Join(t.TempDir(), "rootfs")
	err := Generate(outputRoot, Template{
		Version:     TemplateVersionV1,
		Name:        "custom",
		Description: "Custom template",
		GeneratedFiles: []GeneratedFile{
			{TargetPath: "/etc/machine-id", Content: "", Mode: 0o644},
			{TargetPath: "/etc/demo.conf", Content: "demo=yes\n", Mode: 0o600},
		},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	for target, wantMode := range map[string]os.FileMode{
		filepath.Join(outputRoot, "etc", "machine-id"): 0o644,
		filepath.Join(outputRoot, "etc", "demo.conf"):  0o600,
	} {
		info, err := os.Stat(target)
		if err != nil {
			t.Fatalf("expected generated file %q to exist: %v", target, err)
		}
		if info.Mode().Perm() != wantMode {
			t.Fatalf("expected generated file %q to have mode %o, got %o", target, wantMode, info.Mode().Perm())
		}
	}
}

func TestGenerateCopiesSymlinkedNodeModuleLaunchers(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("host PATH does not contain node")
	}

	hostRoot := filepath.Join(t.TempDir(), "host")
	for _, dir := range []string{
		filepath.Join(hostRoot, "bin"),
		filepath.Join(hostRoot, "lib", "node_modules", "demo", "bin"),
		filepath.Join(hostRoot, "lib", "node_modules", "demo", "lib"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create fixture dir %q: %v", dir, err)
		}
	}

	launcherPath := filepath.Join(hostRoot, "lib", "node_modules", "demo", "bin", "demo.js")
	launcher := "#!/usr/bin/env node\nrequire('../lib/cli.js')\n"
	if err := os.WriteFile(launcherPath, []byte(launcher), 0o755); err != nil {
		t.Fatalf("write launcher: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hostRoot, "lib", "node_modules", "demo", "lib", "cli.js"), []byte("module.exports = () => {}\n"), 0o644); err != nil {
		t.Fatalf("write cli support file: %v", err)
	}
	if err := os.Symlink("../lib/node_modules/demo/bin/demo.js", filepath.Join(hostRoot, "bin", "demo")); err != nil {
		t.Fatalf("create launcher symlink: %v", err)
	}

	outputRoot := filepath.Join(t.TempDir(), "rootfs")
	err = Generate(outputRoot, Template{
		Version:     TemplateVersionV1,
		Name:        "custom",
		Description: "Custom template",
		Binaries: []Binary{
			{
				HostPath:         filepath.Join(hostRoot, "bin", "demo"),
				TargetPath:       "/usr/bin/demo",
				CopyDependencies: true,
			},
			{
				HostPath:         nodePath,
				TargetPath:       "/usr/bin/node",
				CopyDependencies: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	linkTarget, err := os.Readlink(filepath.Join(outputRoot, "usr", "bin", "demo"))
	if err != nil {
		t.Fatalf("read generated symlink: %v", err)
	}
	if linkTarget != "../lib/node_modules/demo/bin/demo.js" {
		t.Fatalf("unexpected generated symlink target: %q", linkTarget)
	}
	for _, target := range []string{
		filepath.Join(outputRoot, "usr", "lib", "node_modules", "demo", "bin", "demo.js"),
		filepath.Join(outputRoot, "usr", "lib", "node_modules", "demo", "lib", "cli.js"),
		filepath.Join(outputRoot, "usr", "bin", "node"),
	} {
		if _, err := os.Stat(target); err != nil {
			t.Fatalf("expected generated target %q to exist: %v", target, err)
		}
	}
}
