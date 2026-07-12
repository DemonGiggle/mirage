package release

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreatePackageDirectoryLayout(t *testing.T) {
	binary := writeTestBinary(t)
	output := filepath.Join(t.TempDir(), "mirage-release")

	report, err := CreatePackage(PackageOptions{OutputPath: output, BinaryPath: binary})
	if err != nil {
		t.Fatalf("CreatePackage() error = %v", err)
	}
	if report.Format != "dir" || report.PackageRoot != output {
		t.Fatalf("unexpected report: %#v", report)
	}

	assertPackageFile(t, filepath.Join(output, "bin", "mirage"), 0o755)
	assertDirectoryHasYAML(t, filepath.Join(output, "share", "mirage", "network-policies"))
	assertDirectoryHasYAML(t, filepath.Join(output, "share", "mirage", "presets"))
}

func TestCreatePackageArchiveLayout(t *testing.T) {
	binary := writeTestBinary(t)
	output := filepath.Join(t.TempDir(), "mirage-test-linux-x86_64.tar.gz")

	report, err := CreatePackage(PackageOptions{OutputPath: output, BinaryPath: binary})
	if err != nil {
		t.Fatalf("CreatePackage() error = %v", err)
	}
	if report.Format != "tar.gz" || report.PackageRoot != "mirage-test-linux-x86_64" {
		t.Fatalf("unexpected report: %#v", report)
	}

	entries := archiveEntries(t, output)
	for _, suffix := range []string{"/bin/mirage", "/share/mirage/network-policies/", "/share/mirage/presets/"} {
		if !containsArchiveSuffix(entries, suffix) {
			t.Fatalf("archive entries do not contain suffix %q: %v", suffix, entries)
		}
	}
}

func TestCreatePackageOverwritePreservesUnmanagedFiles(t *testing.T) {
	binary := writeTestBinary(t)
	output := filepath.Join(t.TempDir(), "mirage-release")
	if err := os.MkdirAll(output, 0o755); err != nil {
		t.Fatal(err)
	}
	unmanaged := filepath.Join(output, "operator-notes.txt")
	if err := os.WriteFile(unmanaged, []byte("keep me\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	if _, err := CreatePackage(PackageOptions{OutputPath: output, BinaryPath: binary, AllowOverwrite: true}); err != nil {
		t.Fatalf("CreatePackage() error = %v", err)
	}
	content, err := os.ReadFile(unmanaged)
	if err != nil {
		t.Fatalf("read unmanaged file: %v", err)
	}
	if string(content) != "keep me\n" {
		t.Fatalf("unmanaged content = %q", content)
	}
}

func TestCreatePackageRejectsSymlinkOutput(t *testing.T) {
	binary := writeTestBinary(t)
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "output")
	if err := os.Symlink(target, output); err != nil {
		t.Fatal(err)
	}

	_, err := CreatePackage(PackageOptions{OutputPath: output, BinaryPath: binary, AllowOverwrite: true})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("CreatePackage() error = %v, want symlink rejection", err)
	}
}

func writeTestBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mirage")
	if err := os.WriteFile(path, []byte("test binary\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertPackageFile(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	if info.Mode().Perm() != mode {
		t.Fatalf("mode for %q = %o, want %o", path, info.Mode().Perm(), mode)
	}
}

func assertDirectoryHasYAML(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".yaml") {
			return
		}
	}
	t.Fatalf("directory %q has no YAML files", path)
}

func archiveEntries(t *testing.T, path string) []string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	var entries []string
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			return entries
		}
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, header.Name)
	}
}

func containsArchiveSuffix(entries []string, suffix string) bool {
	for _, entry := range entries {
		if strings.HasSuffix(entry, suffix) {
			return true
		}
	}
	return false
}
