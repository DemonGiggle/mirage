package rootfs

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
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
