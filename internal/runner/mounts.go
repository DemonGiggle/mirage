package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

type bindMount struct {
	Source   string
	Target   string
	ReadOnly bool
}

func makeMountNamespacePrivate() error {
	if err := syscall.Mount("", "/", "", syscall.MS_REC|syscall.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("set mount propagation private: %w", err)
	}
	return nil
}

func applyBindMounts(rootfs string, roEntries, rwEntries []string) error {
	mounts, err := collectBindMounts(roEntries, rwEntries)
	if err != nil {
		return err
	}
	for _, mount := range mounts {
		if err := applyBindMount(rootfs, mount); err != nil {
			return err
		}
	}
	return nil
}

func collectBindMounts(roEntries, rwEntries []string) ([]bindMount, error) {
	var mounts []bindMount
	for _, entry := range roEntries {
		mount, err := parseBindMount(entry, true)
		if err != nil {
			return nil, err
		}
		mounts = append(mounts, mount)
	}
	for _, entry := range rwEntries {
		mount, err := parseBindMount(entry, false)
		if err != nil {
			return nil, err
		}
		mounts = append(mounts, mount)
	}
	return mounts, nil
}

func parseBindMount(entry string, readOnly bool) (bindMount, error) {
	source, target, ok := strings.Cut(entry, ":")
	if !ok || source == "" || target == "" {
		return bindMount{}, fmt.Errorf("parse bind mount %q: expected host:guest", entry)
	}
	if !filepath.IsAbs(source) {
		return bindMount{}, fmt.Errorf("parse bind mount %q: host path must be absolute", entry)
	}
	if !filepath.IsAbs(target) {
		return bindMount{}, fmt.Errorf("parse bind mount %q: guest path must be absolute", entry)
	}
	cleanTarget := filepath.Clean(target)
	if cleanTarget == "/" {
		return bindMount{}, fmt.Errorf("parse bind mount %q: guest path must not be /", entry)
	}
	return bindMount{
		Source:   filepath.Clean(source),
		Target:   cleanTarget,
		ReadOnly: readOnly,
	}, nil
}

func applyBindMount(rootfs string, mount bindMount) error {
	sourceInfo, err := os.Stat(mount.Source)
	if err != nil {
		return fmt.Errorf("prepare bind mount source %q: %w", mount.Source, err)
	}
	targetPath := bindMountTargetPath(rootfs, mount.Target)
	if err := prepareBindTarget(rootfs, targetPath, sourceInfo.IsDir()); err != nil {
		return fmt.Errorf("prepare bind mount target %q: %w", targetPath, err)
	}
	if err := syscall.Mount(mount.Source, targetPath, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
		return fmt.Errorf("bind mount %q to %q: %w", mount.Source, mount.Target, err)
	}
	if mount.ReadOnly {
		if err := remountBindReadOnly(targetPath); err != nil {
			return fmt.Errorf("remount read-only bind %q at %q: %w", mount.Source, mount.Target, err)
		}
	}
	return nil
}

func bindMountTargetPath(rootfs string, target string) string {
	if rootfs == "/" {
		return target
	}
	return filepath.Join(rootfs, strings.TrimPrefix(target, "/"))
}

func prepareBindTarget(rootfs string, targetPath string, sourceIsDir bool) error {
	info, err := os.Lstat(targetPath)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("target exists as a symlink")
		}
		if sourceIsDir && !info.IsDir() {
			return errors.New("target exists as a file")
		}
		if !sourceIsDir && info.IsDir() {
			return errors.New("target exists as a directory")
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	if rootfs == "/" {
		return errors.New("target does not exist under host rootfs; create it explicitly before mounting")
	}

	if sourceIsDir {
		return ensureDir(targetPath, 0o755)
	}

	if err := ensureDir(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(targetPath, os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	return file.Close()
}

func remountBindReadOnly(targetPath string) error {
	if err := mountSetattrReadOnly(targetPath); err == nil {
		return nil
	} else if !errors.Is(err, syscall.ENOSYS) && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.EOPNOTSUPP) {
		return err
	}
	return syscall.Mount("", targetPath, "", syscall.MS_BIND|syscall.MS_REMOUNT|syscall.MS_RDONLY|syscall.MS_REC, "")
}

func mountSetattrReadOnly(targetPath string) error {
	type mountAttr struct {
		AttrSet     uint64
		AttrClr     uint64
		Propagation uint64
		UsernsFd    uint64
	}

	attr := mountAttr{AttrSet: mountAttrReadOnly}
	targetPtr, err := syscall.BytePtrFromString(targetPath)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall6(sysMountSetattr, atFDCWDUintptr, uintptr(unsafe.Pointer(targetPtr)), uintptr(atRecursive), uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func ensureDir(path string, mode os.FileMode) error {
	if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func ensureOwnedDir(path string, mode os.FileMode, uid int, gid int) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("target %q exists as a symlink", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := ensureDir(path, mode); err != nil {
		return err
	}
	if err := chownFunc(path, uid, gid); err != nil {
		return err
	}
	return nil
}

func ensureMountpointDir(path string, mode os.FileMode) error {
	info, err := os.Stat(path)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%q exists but is not a directory", path)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return ensureDir(path, mode)
}

func mountProc(target string) error {
	if err := syscall.Mount("proc", target, "proc", 0, ""); err != nil {
		return fmt.Errorf("mount proc at %q: %w", target, err)
	}
	return nil
}

func mountTmpfs(target string, data string) error {
	if err := syscall.Mount("tmpfs", target, "tmpfs", 0, data); err != nil {
		return fmt.Errorf("mount tmpfs at %q: %w", target, err)
	}
	return nil
}

func mountDevPTS(target string, data string) error {
	if err := syscall.Mount("devpts", target, "devpts", 0, data); err != nil {
		return fmt.Errorf("mount devpts at %q: %w", target, err)
	}
	return nil
}

func bindOptionalMount(rootfs string, source string, target string, readOnly bool) error {
	if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat optional bind source %q: %w", source, err)
	}
	if err := applyBindMount(rootfs, bindMount{Source: source, Target: target, ReadOnly: readOnly}); err != nil {
		return err
	}
	return nil
}

func ensureSandboxSymlink(path string, target string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			resolved, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("read sandbox symlink %q: %w", path, err)
			}
			if resolved == target {
				return nil
			}
		}
		if info.IsDir() {
			return fmt.Errorf("prepare sandbox symlink %q: path is a directory", path)
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("replace sandbox path %q with symlink: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat sandbox symlink path %q: %w", path, err)
	}
	if err := os.Symlink(target, path); err != nil {
		return fmt.Errorf("create sandbox symlink %q -> %q: %w", path, target, err)
	}
	return nil
}
