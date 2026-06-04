package release

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/DemonGiggle/mirage/examples"
)

type PackageOptions struct {
	OutputPath string
	BinaryPath string
}

type PackageReport struct {
	Format      string
	OutputPath  string
	BinaryPath  string
	PackageRoot string
}

func CreatePackage(opts PackageOptions) (PackageReport, error) {
	if strings.TrimSpace(opts.OutputPath) == "" {
		return PackageReport{}, fmt.Errorf("output path is required")
	}
	if strings.TrimSpace(opts.BinaryPath) == "" {
		return PackageReport{}, fmt.Errorf("binary path is required")
	}

	binaryPath, err := filepath.Abs(opts.BinaryPath)
	if err != nil {
		return PackageReport{}, fmt.Errorf("resolve binary path %q: %w", opts.BinaryPath, err)
	}
	info, err := os.Stat(binaryPath)
	if err != nil {
		return PackageReport{}, fmt.Errorf("stat binary path %q: %w", binaryPath, err)
	}
	if !info.Mode().IsRegular() {
		return PackageReport{}, fmt.Errorf("binary path %q must be a regular file", binaryPath)
	}

	outputPath, err := filepath.Abs(opts.OutputPath)
	if err != nil {
		return PackageReport{}, fmt.Errorf("resolve output path %q: %w", opts.OutputPath, err)
	}

	report := PackageReport{
		OutputPath: outputPath,
		BinaryPath: binaryPath,
	}

	if isArchivePath(outputPath) {
		report.Format = "tar.gz"
		report.PackageRoot = archiveRootName(outputPath)
		if err := createArchivePackage(outputPath, report.PackageRoot, binaryPath, info.Mode().Perm()); err != nil {
			return PackageReport{}, err
		}
		return report, nil
	}

	report.Format = "dir"
	report.PackageRoot = outputPath
	if err := createDirectoryPackage(outputPath, binaryPath, info.Mode().Perm()); err != nil {
		return PackageReport{}, err
	}
	return report, nil
}

func createDirectoryPackage(outputPath, binaryPath string, binaryMode fs.FileMode) error {
	if err := ensureEmptyDir(outputPath); err != nil {
		return err
	}
	return populatePackage(outputPath, binaryPath, binaryMode)
}

func createArchivePackage(outputPath, packageRoot, binaryPath string, binaryMode fs.FileMode) error {
	parent := filepath.Dir(outputPath)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create archive parent directory %q: %w", parent, err)
	}

	stagingRoot, err := os.MkdirTemp("", "mirage-package-*")
	if err != nil {
		return fmt.Errorf("create staging directory: %w", err)
	}
	defer os.RemoveAll(stagingRoot)

	stagedPackageRoot := filepath.Join(stagingRoot, packageRoot)
	if err := populatePackage(stagedPackageRoot, binaryPath, binaryMode); err != nil {
		return err
	}
	if err := writeTarGz(outputPath, stagingRoot, packageRoot); err != nil {
		return err
	}
	return nil
}

func populatePackage(packageRoot, binaryPath string, binaryMode fs.FileMode) error {
	binDir := filepath.Join(packageRoot, "bin")
	networkPolicyDir := filepath.Join(packageRoot, "share", "mirage", "network-policies")
	presetDir := filepath.Join(packageRoot, "share", "mirage", "presets")

	for _, dir := range []string{binDir, networkPolicyDir, presetDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create package directory %q: %w", dir, err)
		}
	}

	if err := copyFile(binaryPath, filepath.Join(binDir, "mirage"), binaryMode); err != nil {
		return err
	}
	if err := examples.ExportNetworkPolicies(networkPolicyDir); err != nil {
		return err
	}
	if err := examples.ExportPresets(presetDir); err != nil {
		return err
	}
	return nil
}

func ensureEmptyDir(path string) error {
	info, err := os.Stat(path)
	switch {
	case err == nil:
		if !info.IsDir() {
			return fmt.Errorf("output path %q already exists and is not a directory", path)
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return fmt.Errorf("read output directory %q: %w", path, err)
		}
		if len(entries) > 0 {
			return fmt.Errorf("output directory %q must be empty", path)
		}
		return nil
	case os.IsNotExist(err):
		if err := os.MkdirAll(path, 0o755); err != nil {
			return fmt.Errorf("create output directory %q: %w", path, err)
		}
		return nil
	default:
		return fmt.Errorf("stat output directory %q: %w", path, err)
	}
}

func copyFile(srcPath, destPath string, mode fs.FileMode) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open source file %q: %w", srcPath, err)
	}
	defer src.Close()

	dest, err := os.OpenFile(destPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("create destination file %q: %w", destPath, err)
	}
	defer dest.Close()

	if _, err := io.Copy(dest, src); err != nil {
		return fmt.Errorf("copy %q to %q: %w", srcPath, destPath, err)
	}
	return nil
}

func writeTarGz(outputPath, rootDir, packageRoot string) error {
	out, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create archive %q: %w", outputPath, err)
	}
	defer out.Close()

	gzipWriter := gzip.NewWriter(out)
	defer gzipWriter.Close()

	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()

	return filepath.Walk(filepath.Join(rootDir, packageRoot), func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relativePath, err := filepath.Rel(rootDir, path)
		if err != nil {
			return fmt.Errorf("compute archive path for %q: %w", path, err)
		}
		archiveName := filepath.ToSlash(relativePath)
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return fmt.Errorf("create tar header for %q: %w", path, err)
		}
		header.Name = archiveName
		if info.IsDir() {
			header.Name += "/"
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return fmt.Errorf("write tar header for %q: %w", path, err)
		}
		if info.IsDir() {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open staged file %q: %w", path, err)
		}
		if _, err := io.Copy(tarWriter, file); err != nil {
			_ = file.Close()
			return fmt.Errorf("write archive file %q: %w", path, err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close staged file %q: %w", path, err)
		}
		return nil
	})
}

func isArchivePath(path string) bool {
	return strings.HasSuffix(path, ".tar.gz") || strings.HasSuffix(path, ".tgz")
}

func archiveRootName(outputPath string) string {
	base := filepath.Base(outputPath)
	for _, suffix := range []string{".tar.gz", ".tgz"} {
		if strings.HasSuffix(base, suffix) {
			base = strings.TrimSuffix(base, suffix)
			break
		}
	}
	if strings.TrimSpace(base) == "" {
		return "mirage-release"
	}
	return base
}
