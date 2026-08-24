package rootfs

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultTinyCoreRelease     = "16.1"
	tinyCoreMaximumArchiveSize = 64 << 20
)

var tinyCoreHTTPClient = &http.Client{Timeout: 2 * time.Minute}
var prepareTinyCoreExtensionSupport = installTinyCoreExtensionSupport

type tinyCoreReleaseConfig struct {
	architecture string
	archiveURL   string
	sha256       string
}

var tinyCoreReleaseConfigs = map[string]tinyCoreReleaseConfig{
	"16.1": {
		architecture: "x86_64",
		archiveURL:   "http://tinycorelinux.net/16.x/x86_64/archive/16.1/distribution_files/rootfs64.gz",
		sha256:       "4b19230201bbdc3b5925c4d2e9f361f2123b207dff6a711452b5155d16496d13",
	},
}

func DefaultTinyCoreRelease() string {
	return defaultTinyCoreRelease
}

func normalizeTinyCoreRelease(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return defaultTinyCoreRelease, nil
	}
	if _, ok := tinyCoreReleaseConfigs[value]; !ok {
		return "", fmt.Errorf("unsupported Tiny Core release %q (supported: %s)", raw, defaultTinyCoreRelease)
	}
	return value, nil
}

func bootstrapTinyCoreBaseRootfs(root, architecture, release string, logOutput io.Writer) error {
	config, ok := tinyCoreReleaseConfigs[release]
	if !ok {
		return fmt.Errorf("unsupported Tiny Core release %q", release)
	}
	if architecture != config.architecture {
		return fmt.Errorf("Tiny Core %s rootfs initialization supports only x86_64, got %q", release, architecture)
	}
	if os.Getenv(testSkipBootstrapEnv) == "1" {
		logLine(logOutput, "download: "+config.archiveURL)
		logLine(logOutput, "expected-sha256: "+config.sha256)
		return prepareFakeTinyCoreRootfs(root)
	}

	archivePath, err := downloadTinyCoreArtifact(filepath.Dir(root), "rootfs", config.archiveURL, config.sha256, tinyCoreMaximumArchiveSize, logOutput)
	if err != nil {
		return err
	}
	defer os.Remove(archivePath)

	archive, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open downloaded Tiny Core archive: %w", err)
	}
	defer archive.Close()
	gzipReader, err := gzip.NewReader(archive)
	if err != nil {
		return fmt.Errorf("open Tiny Core gzip stream: %w", err)
	}
	if err := extractNewcArchive(root, gzipReader); err != nil {
		_ = gzipReader.Close()
		return fmt.Errorf("extract Tiny Core rootfs: %w", err)
	}
	if err := gzipReader.Close(); err != nil {
		return fmt.Errorf("close Tiny Core gzip stream: %w", err)
	}
	if err := prepareTinyCoreExtensionSupport(root, release, logOutput); err != nil {
		return err
	}
	return seedTinyCoreResolver(root, logOutput)
}

func seedTinyCoreResolver(root string, logOutput io.Writer) error {
	data, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		if os.IsNotExist(err) {
			logLine(logOutput, "warning: host /etc/resolv.conf is unavailable; Tiny Core DNS remains unconfigured")
			return nil
		}
		return fmt.Errorf("read host resolver configuration for Tiny Core: %w", err)
	}
	if len(data) == 0 {
		logLine(logOutput, "warning: host /etc/resolv.conf is empty; Tiny Core DNS remains unconfigured")
		return nil
	}
	target := filepath.Join(root, "etc", "resolv.conf")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create Tiny Core resolver directory: %w", err)
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return fmt.Errorf("write Tiny Core resolver configuration: %w", err)
	}
	if err := os.Chmod(target, 0o644); err != nil {
		return fmt.Errorf("set Tiny Core resolver configuration mode: %w", err)
	}
	return nil
}

func downloadTinyCoreArtifact(parent, name, archiveURL, expectedHash string, maximumSize int64, logOutput io.Writer) (string, error) {
	logLine(logOutput, "download: "+archiveURL)
	logLine(logOutput, "expected-sha256: "+expectedHash)

	archive, err := os.CreateTemp(parent, ".mirage-tinycore-"+name+"-*")
	if err != nil {
		return "", fmt.Errorf("create temporary Tiny Core %s archive: %w", name, err)
	}
	archivePath := archive.Name()
	removeArchive := true
	defer func() {
		_ = archive.Close()
		if removeArchive {
			_ = os.Remove(archivePath)
		}
	}()

	response, err := tinyCoreHTTPClient.Get(archiveURL)
	if err != nil {
		return "", fmt.Errorf("download Tiny Core %s: %w", name, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download Tiny Core %s: server returned %s", name, response.Status)
	}
	hash := sha256.New()
	limited := io.LimitReader(response.Body, maximumSize+1)
	written, err := io.Copy(io.MultiWriter(archive, hash), limited)
	if err != nil {
		return "", fmt.Errorf("download Tiny Core %s: %w", name, err)
	}
	if err := archive.Close(); err != nil {
		return "", fmt.Errorf("close Tiny Core %s archive: %w", name, err)
	}
	if written > maximumSize {
		return "", fmt.Errorf("Tiny Core %s archive exceeds the %d MiB size limit", name, maximumSize>>20)
	}
	actualHash := hex.EncodeToString(hash.Sum(nil))
	if actualHash != expectedHash {
		return "", fmt.Errorf("verify Tiny Core %s SHA-256: got %s, want %s", name, actualHash, expectedHash)
	}
	removeArchive = false
	return archivePath, nil
}

func prepareFakeTinyCoreRootfs(root string) error {
	for _, dir := range []string{"proc", "tmp", "run", "dev", "etc", "usr/bin", "usr/lib", "usr/lib64"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			return fmt.Errorf("prepare fake Tiny Core directory %q: %w", dir, err)
		}
	}
	for _, name := range []string{"sh", "ls"} {
		source, err := findBootstrapCommand(name)
		if err != nil {
			return err
		}
		if err := copyBootstrapBinary(root, source, filepath.Join("/bin", name)); err != nil {
			return fmt.Errorf("prepare fake Tiny Core command %q: %w", name, err)
		}
	}
	return os.WriteFile(filepath.Join(root, "etc", "os-release"), []byte("NAME=TinyCore\nVERSION_ID=16.1\n"), 0o644)
}

func findBootstrapCommand(name string) (string, error) {
	source, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("resolve fake Tiny Core command %q: %w", name, err)
	}
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		return "", fmt.Errorf("resolve fake Tiny Core command symlink %q: %w", name, err)
	}
	return resolved, nil
}
