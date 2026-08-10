package rootfs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DemonGiggle/mirage/internal/hostenv"
)

type bootstrapStrategy interface {
	hostEnvironment() hostenv.Kind
	prepareOutput(root string, allowOverwrite bool) error
	bootstrap(root string, args []string, logOutput io.Writer) error
}

type rootHostBootstrapStrategy struct{}

func (rootHostBootstrapStrategy) hostEnvironment() hostenv.Kind {
	return hostenv.Root
}

func (rootHostBootstrapStrategy) prepareOutput(root string, allowOverwrite bool) error {
	return prepareOutputRoot(root, allowOverwrite)
}

func (rootHostBootstrapStrategy) bootstrap(_ string, args []string, logOutput io.Writer) error {
	logCommand(logOutput, "mmdebstrap", args...)
	return runMmdebstrap(args, logOutput)
}

type rootlessHostBootstrapStrategy struct{}

func (rootlessHostBootstrapStrategy) hostEnvironment() hostenv.Kind {
	return hostenv.Rootless
}

func (rootlessHostBootstrapStrategy) prepareOutput(root string, allowOverwrite bool) error {
	info, err := os.Lstat(root)
	switch {
	case err == nil && info.Mode()&os.ModeSymlink != 0:
		return fmt.Errorf("rootless output rootfs %q must not be a symlink", root)
	case err == nil:
	case errors.Is(err, os.ErrNotExist):
	default:
		return fmt.Errorf("lstat rootless output rootfs %q: %w", root, err)
	}
	if err == nil && allowOverwrite {
		keepID, markerErr := HasKeepIDOwnership(root)
		if markerErr != nil {
			return fmt.Errorf("inspect rootless output ownership: %w", markerErr)
		}
		if keepID {
			if validateErr := ValidateKeepIDOwnership(root, os.Getuid(), os.Getgid()); validateErr != nil {
				return validateErr
			}
			if reclaimErr := reclaimRootlessOwnership(root); reclaimErr != nil {
				return fmt.Errorf("reclaim rootless output rootfs %q: %w", root, reclaimErr)
			}
		}
	}
	return prepareOutputRoot(root, allowOverwrite)
}

func (rootlessHostBootstrapStrategy) bootstrap(root string, args []string, logOutput io.Writer) error {
	if err := rejectSymlinkOutputRoot(root); err != nil {
		return err
	}

	archiveFile, err := os.CreateTemp(filepath.Dir(root), ".mirage-rootfs-*.tar")
	if err != nil {
		return fmt.Errorf("create temporary rootfs archive beside %q: %w", root, err)
	}
	archivePath := archiveFile.Name()
	if err := archiveFile.Close(); err != nil {
		_ = os.Remove(archivePath)
		return fmt.Errorf("close temporary rootfs archive %q: %w", archivePath, err)
	}
	if err := os.Remove(archivePath); err != nil {
		return fmt.Errorf("prepare temporary rootfs archive %q: %w", archivePath, err)
	}
	defer func() {
		_ = os.Remove(archivePath)
	}()

	rootlessArgs := make([]string, 0, len(args)+2)
	rootlessArgs = append(rootlessArgs, "--mode=unshare", "--format=tar")
	rootlessArgs = append(rootlessArgs, args...)
	rootlessArgs[len(rootlessArgs)-2] = archivePath
	logCommand(logOutput, "mmdebstrap", rootlessArgs...)
	if err := runMmdebstrap(rootlessArgs, logOutput); err != nil {
		return err
	}
	if err := extractRootlessArchive(root, archivePath); err != nil {
		return fmt.Errorf("extract rootless rootfs archive: %w", err)
	}
	return nil
}

func selectBootstrapStrategy(euid int) bootstrapStrategy {
	if hostenv.Detect(euid).IsRoot() {
		return rootHostBootstrapStrategy{}
	}
	return rootlessHostBootstrapStrategy{}
}

func runMmdebstrap(args []string, logOutput io.Writer) error {
	var stderrBuf strings.Builder
	cmd := exec.Command("mmdebstrap", args...)
	if logOutput != nil {
		cmd.Stdout = logOutput
		cmd.Stderr = logOutput
	} else {
		cmd.Stderr = &stderrBuf
	}
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return errors.New("mmdebstrap is required on the host to initialize a rootfs")
		}
		if stderr := strings.TrimSpace(stderrBuf.String()); stderr != "" {
			return fmt.Errorf("bootstrap rootfs with mmdebstrap: %w: %s", err, stderr)
		}
		return fmt.Errorf("bootstrap rootfs with mmdebstrap: %w", err)
	}
	return nil
}

func rejectSymlinkOutputRoot(root string) error {
	info, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("lstat rootless output rootfs %q: %w", root, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("rootless output rootfs %q must not be a symlink", root)
	}
	if !info.IsDir() {
		return fmt.Errorf("rootless output rootfs %q is not a directory", root)
	}
	return nil
}
