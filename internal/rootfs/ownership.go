package rootfs

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	keepIDOwnershipMarker = ".mirage-rootfs-ownership"
	keepIDOwnershipValue  = "rootless-keep-id-v1\n"
	keepIDGuestUser       = 1000
	keepIDGuestMax        = 65535
)

var finalizeRootlessOwnership = shiftRootlessOwnership

// RootlessKeepIDMapEntries returns the runtime map used by both rootfs
// finalization and sandbox launch. Guest 1000 is the invoking user; every
// other guest ID is backed by the caller's subordinate range.
func RootlessKeepIDMapEntries(hostUID, hostGID int) ([][3]int, [][3]int, error) {
	uidStart, err := currentSubordinateRangeStart("/etc/subuid", hostUID)
	if err != nil {
		return nil, nil, err
	}
	gidStart, err := currentSubordinateRangeStart("/etc/subgid", hostGID)
	if err != nil {
		return nil, nil, err
	}
	return keepIDEntries(hostUID, uidStart), keepIDEntries(hostGID, gidStart), nil
}

func keepIDEntries(hostID, subordinateStart int) [][3]int {
	return [][3]int{
		{0, subordinateStart, keepIDGuestUser},
		{keepIDGuestUser, hostID, 1},
		{keepIDGuestUser + 1, subordinateStart + keepIDGuestUser, keepIDGuestMax - keepIDGuestUser},
	}
}

func currentSubordinateRangeStart(path string, reservedID int) (int, error) {
	currentUser, err := user.Current()
	if err != nil {
		return 0, fmt.Errorf("resolve current user for %s: %w", path, err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", path, err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		parts := strings.Split(line, ":")
		if len(parts) != 3 || parts[0] != currentUser.Username {
			continue
		}
		start, startErr := strconv.Atoi(parts[1])
		size, sizeErr := strconv.Atoi(parts[2])
		if startErr != nil || sizeErr != nil {
			return 0, fmt.Errorf("parse %s entry %q", path, line)
		}
		if size < keepIDGuestMax {
			continue
		}
		end := start + size
		if reservedID >= start && reservedID < start+keepIDGuestMax {
			start = reservedID + 1
		}
		if start+keepIDGuestMax <= end {
			return start, nil
		}
	}
	return 0, fmt.Errorf("rootless keep-ID mode requires %d subordinate IDs in %s for user %q", keepIDGuestMax, path, currentUser.Username)
}

func WriteKeepIDOwnershipMarker(root string) error {
	path := filepath.Join(root, keepIDOwnershipMarker)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(file, keepIDOwnershipValue); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func HasKeepIDOwnership(root string) (bool, error) {
	path := filepath.Join(root, keepIDOwnershipMarker)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("rootfs ownership marker %q is not a regular file", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return string(content) == keepIDOwnershipValue, nil
}

func ValidateKeepIDOwnership(root string, hostUID, hostGID int) error {
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("keep-ID rootfs %q is not a real directory", root)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("inspect keep-ID rootfs %q ownership: unsupported stat result", root)
	}
	uidEntries, gidEntries, err := RootlessKeepIDMapEntries(hostUID, hostGID)
	if err != nil {
		return err
	}
	if stat.Uid != uint32(uidEntries[0][1]) || stat.Gid != uint32(gidEntries[0][1]) {
		return fmt.Errorf("keep-ID rootfs %q has owner %d:%d, want subordinate root owner %d:%d; rebuild it with mirage rootfs init", root, stat.Uid, stat.Gid, uidEntries[0][1], gidEntries[0][1])
	}
	return nil
}

func shiftRootlessOwnership(root string) error {
	if err := RequireRootlessIDMapSupport(); err != nil {
		return err
	}
	if err := WriteKeepIDOwnershipMarker(root); err != nil {
		return fmt.Errorf("write rootfs ownership marker: %w", err)
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = os.Remove(filepath.Join(root, keepIDOwnershipMarker))
		}
	}()
	if err := changeRootlessOwnership(root, keepIDGuestUser); err != nil {
		return err
	}
	succeeded = true
	return nil
}

func RequireRootlessIDMapSupport() error {
	for _, name := range []string{"unshare", "newuidmap", "newgidmap"} {
		if _, err := exec.LookPath(name); err != nil {
			return fmt.Errorf("rootless keep-ID mode requires %s on PATH: %w", name, err)
		}
	}
	return nil
}

func reclaimRootlessOwnership(root string) error {
	return changeRootlessOwnership(root, 0)
}

func changeRootlessOwnership(root string, owner int) error {
	keepUIDEntries, keepGIDEntries, err := RootlessKeepIDMapEntries(os.Getuid(), os.Getgid())
	if err != nil {
		return err
	}
	uidEntries := [][3]int{{0, os.Getuid(), 1}, {keepIDGuestUser, keepUIDEntries[0][1], 1}}
	gidEntries := [][3]int{{0, os.Getgid(), 1}, {keepIDGuestUser, keepGIDEntries[0][1], 1}}
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	defer reader.Close()
	defer writer.Close()
	readyDir, err := os.MkdirTemp("", "mirage-rootfs-map-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(readyDir)
	ready := filepath.Join(readyDir, "ready")
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command("unshare", "--fork", "--kill-child", "--user", self, "__rootfs-ownership", "--pid-fd", "3", "--ready", ready, "--root", root, "--owner", strconv.Itoa(owner))
	cmd.ExtraFiles = []*os.File{writer}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start rootfs ownership helper: %w", err)
	}
	_ = writer.Close()
	line, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("read rootfs ownership helper pid: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	if err := runIDMap("newuidmap", pid, uidEntries); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	if err := runIDMap("newgidmap", pid, gidEntries); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	if err := os.WriteFile(ready, []byte("ready\n"), 0o600); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("rootfs ownership helper failed: %w", err)
	}
	return nil
}

func runIDMap(name string, pid int, entries [][3]int) error {
	args := []string{strconv.Itoa(pid)}
	for _, entry := range entries {
		args = append(args, strconv.Itoa(entry[0]), strconv.Itoa(entry[1]), strconv.Itoa(entry[2]))
	}
	output, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s for pid %d failed: %w: %s", name, pid, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func RunOwnershipHelper(args []string, stdout, stderr io.Writer) error {
	_ = stdout
	_ = stderr
	fs := flag.NewFlagSet("__rootfs-ownership", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var pidFD int
	var ready, root string
	var owner int
	var mappedRootReady bool
	fs.IntVar(&pidFD, "pid-fd", -1, "host pid output")
	fs.StringVar(&ready, "ready", "", "mapping readiness file")
	fs.StringVar(&root, "root", "", "rootfs path")
	fs.IntVar(&owner, "owner", -1, "guest owner assigned recursively")
	fs.BoolVar(&mappedRootReady, "mapped-root-ready", false, "mapping handoff completion marker")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if owner != 0 && owner != keepIDGuestUser {
		return fmt.Errorf("unsupported rootfs ownership target %d", owner)
	}
	if !mappedRootReady {
		file := os.NewFile(uintptr(pidFD), "rootfs-map-pid")
		if file == nil {
			return errors.New("invalid rootfs ownership pid fd")
		}
		status, err := os.ReadFile("/proc/self/status")
		if err != nil {
			return err
		}
		hostPID, err := outermostPID(status)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(file, "%d\n", hostPID); err != nil {
			return err
		}
		_ = file.Close()
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := os.Stat(ready); err == nil {
				break
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if time.Now().After(deadline) {
				return errors.New("timed out waiting for rootfs ownership mapping")
			}
			time.Sleep(10 * time.Millisecond)
		}
		reexecArgs := []string{"/proc/self/exe", "__rootfs-ownership", "--mapped-root-ready"}
		reexecArgs = append(reexecArgs, args...)
		return syscall.Exec("/proc/self/exe", reexecArgs, os.Environ())
	}
	_ = syscall.Setgroups(nil)
	if err := syscall.Setgid(0); err != nil {
		return fmt.Errorf("become mapped root gid: %w", err)
	}
	if err := syscall.Setuid(0); err != nil {
		return fmt.Errorf("become mapped root uid: %w", err)
	}
	rootFD, err := syscall.Open(root, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open rootfs ownership target %q without following symlinks: %w", root, err)
	}
	if err := syscall.Fchdir(rootFD); err != nil {
		_ = syscall.Close(rootFD)
		return fmt.Errorf("enter rootfs ownership target %q: %w", root, err)
	}
	if err := syscall.Chroot("."); err != nil {
		_ = syscall.Close(rootFD)
		return fmt.Errorf("confine rootfs ownership target %q: %w", root, err)
	}
	if err := syscall.Chdir("/"); err != nil {
		_ = syscall.Close(rootFD)
		return fmt.Errorf("enter confined rootfs ownership target %q: %w", root, err)
	}
	_ = syscall.Close(rootFD)
	root = "/"
	type ownershipPath struct {
		path string
		mode os.FileMode
	}
	var paths []ownershipPath
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		paths = append(paths, ownershipPath{path: path, mode: info.Mode()})
		return nil
	}); err != nil {
		return err
	}
	for idx := len(paths) - 1; idx >= 0; idx-- {
		item := paths[idx]
		if err := os.Lchown(item.path, owner, owner); err != nil {
			return fmt.Errorf("shift rootfs ownership for %q: %w", item.path, err)
		}
		if item.mode&os.ModeSymlink == 0 {
			if err := os.Chmod(item.path, item.mode); err != nil {
				return fmt.Errorf("restore rootfs mode for %q: %w", item.path, err)
			}
		}
	}
	return nil
}

func outermostPID(status []byte) (int, error) {
	for _, line := range strings.Split(string(status), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok && key == "NSpid" {
			fields := strings.Fields(value)
			if len(fields) > 0 {
				return strconv.Atoi(fields[0])
			}
		}
	}
	return 0, errors.New("NSpid field is missing")
}
