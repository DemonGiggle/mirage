package rootfs

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

type deferredArchiveDirectory struct {
	path string
	mode os.FileMode
}

type deferredArchiveHardlink struct {
	path   string
	target string
}

func extractRootlessArchive(root string, archivePath string) error {
	archiveFile, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive %q: %w", archivePath, err)
	}
	defer archiveFile.Close()

	rootInfo, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("lstat extraction root %q: %w", root, err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return fmt.Errorf("extraction root %q must be a real directory", root)
	}

	reader := tar.NewReader(archiveFile)
	var directories []deferredArchiveDirectory
	var hardlinks []deferredArchiveHardlink
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read archive entry: %w", err)
		}

		name, err := cleanArchivePath(header.Name)
		if err != nil {
			return err
		}
		if name == "." {
			directories = append(directories, deferredArchiveDirectory{path: root, mode: archiveMode(header)})
			continue
		}
		target, err := prepareArchiveTarget(root, name)
		if err != nil {
			return fmt.Errorf("prepare archive entry %q: %w", header.Name, err)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.Mkdir(target, 0o755); err != nil {
				return fmt.Errorf("create archive directory %q: %w", header.Name, err)
			}
			directories = append(directories, deferredArchiveDirectory{path: target, mode: archiveMode(header)})
		case tar.TypeReg, tar.TypeRegA:
			if err := createArchiveRegularFile(target, archiveMode(header), reader); err != nil {
				return fmt.Errorf("create archive file %q: %w", header.Name, err)
			}
		case tar.TypeSymlink:
			if err := os.Symlink(header.Linkname, target); err != nil {
				return fmt.Errorf("create archive symlink %q: %w", header.Name, err)
			}
		case tar.TypeLink:
			linkName, err := cleanArchivePath(header.Linkname)
			if err != nil || linkName == "." {
				return fmt.Errorf("archive hardlink %q has unsafe target %q", header.Name, header.Linkname)
			}
			hardlinks = append(hardlinks, deferredArchiveHardlink{path: target, target: linkName})
		case tar.TypeFifo:
			if err := syscall.Mkfifo(target, uint32(archiveMode(header).Perm())); err != nil {
				return fmt.Errorf("create archive fifo %q: %w", header.Name, err)
			}
		case tar.TypeChar, tar.TypeBlock:
			if name != "dev" && !strings.HasPrefix(name, "dev/") {
				return fmt.Errorf("archive device node %q is outside managed /dev", header.Name)
			}
			// Mirage replaces /dev with a managed tmpfs at runtime. Rootless hosts
			// cannot safely recreate archive device nodes on the host filesystem.
		case tar.TypeXHeader, tar.TypeXGlobalHeader:
			continue
		default:
			return fmt.Errorf("archive entry %q has unsupported type %d", header.Name, header.Typeflag)
		}
	}

	for _, link := range hardlinks {
		source, err := existingArchiveTarget(root, link.target)
		if err != nil {
			return fmt.Errorf("resolve archive hardlink target %q: %w", link.target, err)
		}
		info, err := os.Lstat(source)
		if err != nil {
			return fmt.Errorf("lstat archive hardlink target %q: %w", link.target, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("archive hardlink target %q is not a regular file", link.target)
		}
		if err := os.Link(source, link.path); err != nil {
			return fmt.Errorf("create archive hardlink %q: %w", link.path, err)
		}
	}

	slices.Reverse(directories)
	for _, directory := range directories {
		if err := os.Chmod(directory.path, directory.mode); err != nil {
			return fmt.Errorf("set archive directory mode for %q: %w", directory.path, err)
		}
	}
	return nil
}

func cleanArchivePath(raw string) (string, error) {
	if raw == "" || strings.ContainsRune(raw, '\x00') || path.IsAbs(raw) {
		return "", fmt.Errorf("archive path %q is not a safe relative path", raw)
	}
	cleaned := path.Clean(raw)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("archive path %q escapes the rootfs", raw)
	}
	return filepath.FromSlash(cleaned), nil
}

func prepareArchiveTarget(root string, name string) (string, error) {
	parts := strings.Split(name, string(filepath.Separator))
	current := root
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		switch {
		case errors.Is(err, os.ErrNotExist):
			if err := os.Mkdir(current, 0o755); err != nil {
				return "", err
			}
		case err != nil:
			return "", err
		case info.Mode()&os.ModeSymlink != 0:
			return "", fmt.Errorf("ancestor %q is a symlink", current)
		case !info.IsDir():
			return "", fmt.Errorf("ancestor %q is not a directory", current)
		}
	}

	target := filepath.Join(root, name)
	if _, err := os.Lstat(target); err == nil {
		return "", fmt.Errorf("target %q already exists", target)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return target, nil
}

func existingArchiveTarget(root string, name string) (string, error) {
	parts := strings.Split(name, string(filepath.Separator))
	current := root
	for _, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("path component %q is a symlink", current)
		}
	}
	return current, nil
}

func createArchiveRegularFile(target string, mode os.FileMode, reader io.Reader) error {
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, reader); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Chmod(target, mode)
}

func archiveMode(header *tar.Header) os.FileMode {
	return header.FileInfo().Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
}
