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
	logLine(logOutput, "download: "+config.archiveURL)
	logLine(logOutput, "expected-sha256: "+config.sha256)

	if os.Getenv(testSkipBootstrapEnv) == "1" {
		return prepareFakeTinyCoreRootfs(root)
	}

	archive, err := os.CreateTemp(filepath.Dir(root), ".mirage-tinycore-*.gz")
	if err != nil {
		return fmt.Errorf("create temporary Tiny Core archive beside %q: %w", root, err)
	}
	archivePath := archive.Name()
	defer os.Remove(archivePath)

	response, err := tinyCoreHTTPClient.Get(config.archiveURL)
	if err != nil {
		_ = archive.Close()
		return fmt.Errorf("download Tiny Core rootfs: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_ = archive.Close()
		return fmt.Errorf("download Tiny Core rootfs: server returned %s", response.Status)
	}
	hash := sha256.New()
	limited := io.LimitReader(response.Body, tinyCoreMaximumArchiveSize+1)
	written, err := io.Copy(io.MultiWriter(archive, hash), limited)
	if err != nil {
		_ = archive.Close()
		return fmt.Errorf("download Tiny Core rootfs: %w", err)
	}
	if err := archive.Close(); err != nil {
		return fmt.Errorf("close Tiny Core archive: %w", err)
	}
	if written > tinyCoreMaximumArchiveSize {
		return fmt.Errorf("Tiny Core rootfs archive exceeds the %d MiB size limit", tinyCoreMaximumArchiveSize>>20)
	}
	actualHash := hex.EncodeToString(hash.Sum(nil))
	if actualHash != config.sha256 {
		return fmt.Errorf("verify Tiny Core rootfs SHA-256: got %s, want %s", actualHash, config.sha256)
	}

	archive, err = os.Open(archivePath)
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
	return nil
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
