package rootfs

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestBootstrapTinyCoreDownloadsVerifiesAndExtracts(t *testing.T) {
	t.Setenv(testSkipBootstrapEnv, "0")
	previousExtensionSupport := prepareTinyCoreExtensionSupport
	prepareTinyCoreExtensionSupport = func(root, release string, logOutput io.Writer) error { return nil }
	t.Cleanup(func() { prepareTinyCoreExtensionSupport = previousExtensionSupport })
	archive := gzipTestNewcArchive(t, []testNewcEntry{
		{name: ".", mode: syscall.S_IFDIR | 0o755},
		{name: "bin", mode: syscall.S_IFDIR | 0o755},
		{name: "bin/sh", mode: syscall.S_IFREG | 0o755, data: "shell"},
	})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write(archive)
	}))
	defer server.Close()

	hash := sha256.Sum256(archive)
	previousConfigs := tinyCoreReleaseConfigs
	tinyCoreReleaseConfigs = map[string]tinyCoreReleaseConfig{
		"test": {architecture: "x86_64", archiveURL: server.URL, sha256: hex.EncodeToString(hash[:])},
	}
	t.Cleanup(func() { tinyCoreReleaseConfigs = previousConfigs })

	root := t.TempDir()
	var log bytes.Buffer
	if err := bootstrapTinyCoreBaseRootfs(root, "x86_64", "test", &log); err != nil {
		t.Fatalf("bootstrapTinyCoreBaseRootfs returned error: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, "bin", "sh"))
	if err != nil || string(content) != "shell" {
		t.Fatalf("unexpected extracted shell %q, err=%v", content, err)
	}
	if !strings.Contains(log.String(), "expected-sha256: "+hex.EncodeToString(hash[:])) {
		t.Fatalf("expected checksum in log, got %q", log.String())
	}
}

func TestBootstrapTinyCoreRejectsChecksumMismatch(t *testing.T) {
	t.Setenv(testSkipBootstrapEnv, "0")
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte("not the expected archive"))
	}))
	defer server.Close()

	previousConfigs := tinyCoreReleaseConfigs
	tinyCoreReleaseConfigs = map[string]tinyCoreReleaseConfig{
		"test": {architecture: "x86_64", archiveURL: server.URL, sha256: strings.Repeat("0", 64)},
	}
	t.Cleanup(func() { tinyCoreReleaseConfigs = previousConfigs })

	err := bootstrapTinyCoreBaseRootfs(t.TempDir(), "x86_64", "test", nil)
	if err == nil || !strings.Contains(err.Error(), "verify Tiny Core rootfs SHA-256") {
		t.Fatalf("expected checksum verification error, got %v", err)
	}
}

func TestBootstrapWithTinyCoreOptions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tinycore-rootfs")
	report, err := BootstrapWithReportWithOptions(root, GenerateOptions{
		Distribution: "tinycore",
		Architecture: "x86_64",
	})
	if err != nil {
		t.Fatalf("BootstrapWithReportWithOptions returned error: %v", err)
	}
	if report.Distribution != "tinycore" || report.Release != defaultTinyCoreRelease {
		t.Fatalf("unexpected Tiny Core report: %#v", report)
	}
	if _, err := os.Stat(filepath.Join(root, "etc", "apt")); !os.IsNotExist(err) {
		t.Fatalf("Tiny Core rootfs unexpectedly contains Debian apt state: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "bin", "sh")); err != nil {
		t.Fatalf("Tiny Core rootfs is missing /bin/sh: %v", err)
	}
}

func TestTinyCoreOptionsRejectDebianOnlyFeaturesBeforeOverwrite(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "keep-me")
	if err := os.WriteFile(marker, []byte("present"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := BootstrapWithReportWithOptions(root, GenerateOptions{
		AllowOverwrite: true,
		Distribution:   "tinycore",
		Architecture:   "x86_64",
		ExtraPackages:  []string{"curl"},
	})
	if err == nil || !strings.Contains(err.Error(), "--extra-pkg is not supported") {
		t.Fatalf("expected Tiny Core package rejection, got %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("invalid options modified the existing output: %v", err)
	}
}

func TestPatchTinyCoreTCELoadUsesUnsquashfsCopyMode(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "usr", "bin", "tce-load")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := `#!/bin/sh
copyInstall() {
	sudo mount "$1" /mnt/test -t squashfs -o loop,ro
}

update_system() {
	:
}
`
	if err := os.WriteFile(path, []byte(original), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := patchTinyCoreTCELoad(root); err != nil {
		t.Fatalf("patchTinyCoreTCELoad returned error: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sudo /usr/bin/env LD_LIBRARY_PATH=/usr/local/lib /usr/local/bin/unsquashfs", "sudo /bin/cp -ai", "update_system()"} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("patched tce-load is missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(string(content), "sudo mount") {
		t.Fatalf("patched tce-load still contains the SquashFS mount:\n%s", content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat patched tce-load: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("patched tce-load mode=%v", info.Mode())
	}
}

func TestPrepareTinyCoreTCEDirectoryModes(t *testing.T) {
	root := t.TempDir()
	if err := prepareTinyCoreTCEDirectory(root); err != nil {
		t.Fatalf("prepareTinyCoreTCEDirectory returned error: %v", err)
	}

	wantModes := map[string]os.FileMode{
		"etc/sysconfig/tcedir":             0o755,
		"etc/sysconfig/tcedir/optional":    0o777,
		"etc/sysconfig/tcedir/ondemand":    0o777,
		"etc/sysconfig/tcedir/onboot.lst":  0o666,
		"etc/sysconfig/tcedir/copy2fs.flg": 0o644,
	}
	for relativePath, wantMode := range wantModes {
		info, err := os.Stat(filepath.Join(root, relativePath))
		if err != nil {
			t.Fatalf("stat %s: %v", relativePath, err)
		}
		if got := info.Mode().Perm(); got != wantMode {
			t.Errorf("%s mode=%#o, want %#o", relativePath, got, wantMode)
		}
	}
}

func gzipTestNewcArchive(t *testing.T, entries []testNewcEntry) []byte {
	t.Helper()
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(buildTestNewcArchive(t, entries)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}
