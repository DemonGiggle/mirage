package runner

import (
	"bufio"
	"errors"
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

	"github.com/DemonGiggle/mirage/internal/hostenv"
)

type sandboxLaunchSync struct {
	targetPIDReader *os.File
	targetPIDWriter *os.File
	uidMapReadyFile string
	tempDir         string
}

func prepareSandboxLaunchSync(runAsRoot bool, networkBackend string) (sandboxLaunchSync, error) {
	tempDir, err := os.MkdirTemp("", "mirage-launch-*")
	if err != nil {
		return sandboxLaunchSync{}, fmt.Errorf("create sandbox launch tempdir: %w", err)
	}
	targetPIDReader, targetPIDWriter, err := os.Pipe()
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return sandboxLaunchSync{}, fmt.Errorf("create sandbox target pid pipe: %w", err)
	}
	sync := sandboxLaunchSync{
		targetPIDReader: targetPIDReader,
		targetPIDWriter: targetPIDWriter,
		uidMapReadyFile: filepath.Join(tempDir, "uidmap.ready"),
		tempDir:         tempDir,
	}
	return sync, nil
}

func (s sandboxLaunchSync) cleanup() {
	closeQuietly(s.targetPIDReader)
	closeQuietly(s.targetPIDWriter)
	if s.tempDir != "" {
		_ = os.RemoveAll(s.tempDir)
	}
}

func (s *sandboxLaunchSync) closeTargetPIDWriter() {
	closeQuietly(s.targetPIDWriter)
	s.targetPIDWriter = nil
}

func (s sandboxLaunchSync) signalUIDMapReady() error {
	if s.uidMapReadyFile == "" {
		return nil
	}
	return os.WriteFile(s.uidMapReadyFile, []byte("ready\n"), 0o600)
}

func buildBackendLaunchArgs(baseArgs []string, uidMapReadyFile string, targetPIDFD int) []string {
	var launchArgs []string
	if targetPIDFD >= 0 {
		launchArgs = append(launchArgs, "--target-pid-fd", strconv.Itoa(targetPIDFD))
	}
	if uidMapReadyFile != "" {
		launchArgs = append(launchArgs, "--uid-map-ready-file", uidMapReadyFile)
	}
	if len(launchArgs) == 0 {
		return baseArgs
	}
	newArgs := make([]string, 0, len(baseArgs)+len(launchArgs))
	newArgs = append(newArgs, baseArgs[:2]...)
	newArgs = append(newArgs, launchArgs...)
	newArgs = append(newArgs, baseArgs[2:]...)
	return newArgs
}

func buildUnshareArgs(runAsRoot bool, networkBackend string) ([]string, error) {
	args := []string{
		"--fork",
		"--kill-child",
		"--mount",
		"--uts",
		"--ipc",
		"--pid",
	}
	switch networkBackend {
	case "", backendNetworkPolicyHost:
	case backendNetworkPolicyRouted, backendNetworkPolicyIsolated:
		args = append(args, "--net")
	default:
		return nil, fmt.Errorf("unsupported backend network mode %q", networkBackend)
	}
	args = append(args, "--user")
	// A host-root launcher clears supplementary groups before unshare and can
	// safely lock setgroups afterward. A rootless launcher must keep setgroups
	// available so the mapped namespace root can clear the caller's inherited
	// groups immediately before dropping to the guest identity.
	if !runAsRoot && hostenv.Detect(currentUID()).IsRoot() {
		args = append(args, "--setgroups", "deny")
	}
	return args, nil
}

func waitForSandboxTargetPID(reader *os.File) (int, error) {
	if reader == nil {
		return 0, errors.New("sandbox target pid reader is nil")
	}
	type readResult struct {
		line string
		err  error
	}
	resultCh := make(chan readResult, 1)
	go func() {
		line, err := bufio.NewReader(reader).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			resultCh <- readResult{err: err}
			return
		}
		resultCh <- readResult{line: line}
	}()

	select {
	case result := <-resultCh:
		if result.err != nil {
			return 0, fmt.Errorf("read sandbox target pid from pipe: %w", result.err)
		}
		raw := strings.TrimSpace(result.line)
		pid, err := strconv.Atoi(raw)
		if err != nil {
			return 0, fmt.Errorf("parse sandbox target pid %q from pipe: %w", raw, err)
		}
		if pid <= 0 {
			return 0, fmt.Errorf("sandbox target pid %d from pipe is invalid", pid)
		}
		return pid, nil
	case <-time.After(5 * time.Second):
		return 0, errors.New("timed out waiting for sandbox target pid from pipe")
	}
}

func writeTargetPIDFD(fd int) error {
	if fd < 0 {
		return nil
	}
	if fd < 3 {
		return fmt.Errorf("sandbox target pid fd %d is invalid", fd)
	}
	file := os.NewFile(uintptr(fd), "mirage-target-pid")
	if file == nil {
		return fmt.Errorf("open sandbox target pid fd %d", fd)
	}
	defer closeQuietly(file)
	hostPID, err := currentHostPID()
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(file, "%d\n", hostPID); err != nil {
		return fmt.Errorf("write sandbox target pid to fd %d: %w", fd, err)
	}
	return nil
}

func currentHostPID() (int, error) {
	statusPath := filepath.Join(procfsRoot, "self", "status")
	status, err := os.ReadFile(statusPath)
	if err != nil {
		return 0, fmt.Errorf("read host pid from %q: %w", statusPath, err)
	}
	pid, err := hostPIDFromProcStatus(status)
	if err != nil {
		return 0, fmt.Errorf("resolve host pid from %q: %w", statusPath, err)
	}
	return pid, nil
}

func hostPIDFromProcStatus(status []byte) (int, error) {
	for _, line := range strings.Split(string(status), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || key != "NSpid" {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			return 0, errors.New("NSpid field is empty")
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			return 0, fmt.Errorf("parse outermost NSpid %q: %w", fields[0], err)
		}
		if pid <= 0 {
			return 0, fmt.Errorf("outermost NSpid %d is invalid", pid)
		}
		return pid, nil
	}
	return 0, errors.New("NSpid field is missing")
}

func configureSandboxUIDMappings(pid int, runAsRoot bool) error {
	rootHostUID := currentUID()
	rootHostGID := currentGID()
	uidEntries, gidEntries, err := sandboxIDMapEntries(runAsRoot, rootHostUID, rootHostGID)
	if err != nil {
		return err
	}
	if rootHostUID == 0 {
		if err := writeNamespaceIDMap(procfsPathForPID(pid, "uid_map"), uidEntries); err != nil {
			return fmt.Errorf("write uid_map for pid %d: %w", pid, err)
		}
		if err := writeNamespaceIDMap(procfsPathForPID(pid, "gid_map"), gidEntries); err != nil {
			return fmt.Errorf("write gid_map for pid %d: %w", pid, err)
		}
		return nil
	}
	if err := idMapCommandRunner("newuidmap", pid, uidEntries); err != nil {
		return err
	}
	if err := idMapCommandRunner("newgidmap", pid, gidEntries); err != nil {
		return err
	}
	return nil
}

func sandboxIDMapEntries(runAsRoot bool, rootHostUID int, rootHostGID int) ([][3]int, [][3]int, error) {
	if !runAsRoot {
		uidHostID, err := resolveHostSandboxID("/etc/subuid", rootHostUID, sandboxUID)
		if err != nil {
			return nil, nil, err
		}
		gidHostID, err := resolveHostSandboxID("/etc/subgid", rootHostGID, sandboxGID)
		if err != nil {
			return nil, nil, err
		}
		return [][3]int{{0, rootHostUID, 1}, {sandboxUID, uidHostID, 1}},
			[][3]int{{0, rootHostGID, 1}, {sandboxGID, gidHostID, 1}},
			nil
	}

	uidStart, err := resolveHostSandboxRange("/etc/subuid", rootHostUID, 1, maxMappedGuestID)
	if err != nil {
		return nil, nil, err
	}
	gidStart, err := resolveHostSandboxRange("/etc/subgid", rootHostGID, 1, maxMappedGuestID)
	if err != nil {
		return nil, nil, err
	}
	return [][3]int{{0, rootHostUID, 1}, {1, uidStart, maxMappedGuestID}},
		[][3]int{{0, rootHostGID, 1}, {1, gidStart, maxMappedGuestID}},
		nil
}

func procfsPathForPID(pid int, name string) string {
	return filepath.Join(procfsRoot, strconv.Itoa(pid), name)
}

func writeNamespaceIDMap(path string, entries [][3]int) error {
	var builder strings.Builder
	for _, entry := range entries {
		if _, err := fmt.Fprintf(&builder, "%d %d %d\n", entry[0], entry[1], entry[2]); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(builder.String()), 0o644)
}

func runIDMapCommand(name string, pid int, entries [][3]int) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("default non-root sandbox requires %s on PATH: %w", name, err)
	}
	args := []string{strconv.Itoa(pid)}
	for _, entry := range entries {
		args = append(args, strconv.Itoa(entry[0]), strconv.Itoa(entry[1]), strconv.Itoa(entry[2]))
	}
	cmd := exec.Command(path, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s for pid %d failed: %w%s", name, pid, err, formatCommandOutput(output))
	}
	return nil
}

func resolveHostSandboxID(path string, reservedHostID int, containerID int) (int, error) {
	if currentUID() == 0 {
		return containerID, nil
	}

	currentUser, err := user.Current()
	if err != nil {
		return 0, fmt.Errorf("resolve current user for %s: %w", path, err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", path, err)
	}
	return resolveHostSandboxIDForUser(path, currentUser.Username, reservedHostID, content)
}

func resolveHostSandboxRange(path string, reservedHostID int, containerStart int, containerSize int) (int, error) {
	if currentUID() == 0 {
		return containerStart, nil
	}

	currentUser, err := user.Current()
	if err != nil {
		return 0, fmt.Errorf("resolve current user for %s: %w", path, err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", path, err)
	}
	hostStart, _, err := resolveHostSandboxRangeForUser(path, currentUser.Username, reservedHostID, containerStart, containerSize, content)
	return hostStart, err
}

func resolveHostSandboxIDForUser(path string, username string, reservedHostID int, content []byte) (int, error) {
	hostID, _, err := resolveHostSandboxRangeForUser(path, username, reservedHostID, sandboxUID, 1, content)
	return hostID, err
}

func resolveHostSandboxRangeForUser(path string, username string, reservedHostID int, containerStart int, containerSize int, content []byte) (int, int, error) {
	var lastUsableErr error
	for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		parts := strings.Split(line, ":")
		if len(parts) != 3 {
			return 0, 0, fmt.Errorf("parse %s entry %q", path, line)
		}
		if parts[0] != username {
			continue
		}
		start, err := strconv.Atoi(parts[1])
		if err != nil {
			return 0, 0, fmt.Errorf("parse %s start %q: %w", path, parts[1], err)
		}
		size, err := strconv.Atoi(parts[2])
		if err != nil {
			return 0, 0, fmt.Errorf("parse %s size %q: %w", path, parts[2], err)
		}
		hostID, hostSize, err := selectHostSandboxRange(path, line, start, size, reservedHostID, containerStart, containerSize)
		if err == nil {
			return hostID, hostSize, nil
		}
		lastUsableErr = err
	}

	if lastUsableErr != nil {
		return 0, 0, lastUsableErr
	}
	return 0, 0, fmt.Errorf("default non-root sandbox requires a subordinate ID range in %s for user %q", path, username)
}

func selectHostSandboxRange(path string, line string, start int, size int, reservedHostID int, containerStart int, containerSize int) (int, int, error) {
	if containerSize < 1 || size < 1 {
		return 0, 0, fmt.Errorf("%s entry %q has invalid size", path, line)
	}
	hostStart := start
	if reservedHostID >= hostStart && reservedHostID < hostStart+containerSize {
		hostStart = reservedHostID + 1
	}
	if hostStart+containerSize > start+size {
		if reservedHostID >= start && reservedHostID < start+size {
			return 0, 0, fmt.Errorf("%s entry %q overlaps reserved host ID %d and does not leave another ID for the sandbox user", path, line, reservedHostID)
		}
		return 0, 0, fmt.Errorf("%s entry %q does not provide %d subordinate IDs", path, line, containerSize)
	}
	return hostStart, containerSize, nil
}

func waitForUIDMapReady(path string) error {
	if path == "" {
		return nil
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("check uid map readiness file %q: %w", path, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for uid map readiness file %q", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func reexecBackendWithMappedRoot(rootfs string, cwd string, hostname string, networkBackend string, policyConfig string, routedInterface string, routedAddress string, routedGateway string, networkReadyFD int, roBind []string, rwBind []string, envItems []string, runAsRoot bool, command []string) error {
	self := selfReexecPath()

	args := []string{self, "__backend-exec", "--rootfs", rootfs, "--network-backend", networkBackend, "--mapped-root-ready"}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	if hostname != "" {
		args = append(args, "--hostname", hostname)
	}
	if policyConfig != "" {
		args = append(args, "--policy-config", policyConfig)
	}
	if routedInterface != "" {
		args = append(args, "--routed-interface", routedInterface)
	}
	if routedAddress != "" {
		args = append(args, "--routed-address", routedAddress)
	}
	if routedGateway != "" {
		args = append(args, "--routed-gateway", routedGateway)
	}
	if networkReadyFD >= 0 {
		args = append(args, "--network-ready-fd", strconv.Itoa(networkReadyFD))
	}
	for _, item := range roBind {
		args = append(args, "--ro-bind", item)
	}
	for _, item := range rwBind {
		args = append(args, "--rw-bind", item)
	}
	for _, item := range envItems {
		args = append(args, "--env", item)
	}
	if runAsRoot {
		args = append(args, "--run-as-root")
	}
	args = append(args, "--")
	args = append(args, command...)

	return syscall.Exec(self, args, os.Environ())
}

func prepareRootfsMountLayout(rootfs string) error {
	info, err := os.Stat(rootfs)
	if err != nil {
		return fmt.Errorf("prepare rootfs %q: %w", rootfs, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("prepare rootfs %q: not a directory", rootfs)
	}

	if err := ensureDir(filepath.Join(rootfs, "proc"), 0o755); err != nil {
		return fmt.Errorf("prepare proc mountpoint: %w", err)
	}
	if err := ensureDir(filepath.Join(rootfs, "tmp"), 0o1777); err != nil {
		return fmt.Errorf("prepare tmp mountpoint: %w", err)
	}
	if err := ensureDir(filepath.Join(rootfs, "run"), 0o755); err != nil {
		return fmt.Errorf("prepare run mountpoint: %w", err)
	}
	if rootfs != "/" {
		if err := ensureDir(filepath.Join(rootfs, strings.TrimPrefix(defaultSandboxHome, "/")), 0o755); err != nil {
			return fmt.Errorf("prepare sandbox home directory: %w", err)
		}
	}

	if err := mountProc(filepath.Join(rootfs, "proc")); err != nil {
		return err
	}
	if err := mountTmpfs(filepath.Join(rootfs, "tmp"), "mode=1777"); err != nil {
		return err
	}
	if err := mountTmpfs(filepath.Join(rootfs, "run"), "mode=0755"); err != nil {
		return err
	}
	if err := prepareSandboxDevLayout(rootfs); err != nil {
		return err
	}
	return nil
}

func prepareSandboxDevLayout(rootfs string) error {
	devRoot := bindMountTargetPath(rootfs, "/dev")
	if err := ensureMountpointDir(devRoot, 0o755); err != nil {
		return fmt.Errorf("prepare sandbox /dev mountpoint: %w", err)
	}
	if err := mountTmpfs(devRoot, "mode=0755"); err != nil {
		return fmt.Errorf("mount sandbox /dev tmpfs: %w", err)
	}

	shmRoot := bindMountTargetPath(rootfs, "/dev/shm")
	if err := ensureMountpointDir(shmRoot, 0o1777); err != nil {
		return fmt.Errorf("prepare sandbox /dev/shm mountpoint: %w", err)
	}
	if err := mountTmpfs(shmRoot, "mode=1777"); err != nil {
		return fmt.Errorf("mount sandbox /dev/shm tmpfs: %w", err)
	}

	ptsRoot := bindMountTargetPath(rootfs, "/dev/pts")
	if err := ensureMountpointDir(ptsRoot, 0o755); err != nil {
		return fmt.Errorf("prepare sandbox /dev/pts mountpoint: %w", err)
	}
	if err := mountDevPTS(ptsRoot, "newinstance,ptmxmode=0666,mode=0620"); err != nil {
		return err
	}

	for _, path := range []string{"/dev/null", "/dev/zero", "/dev/full", "/dev/random", "/dev/urandom", "/dev/tty"} {
		if err := applyBindMount(rootfs, bindMount{Source: path, Target: path}); err != nil {
			return fmt.Errorf("prepare sandbox device node %q: %w", path, err)
		}
	}
	if err := bindOptionalMount(rootfs, "/dev/console", "/dev/console", false); err != nil {
		return err
	}

	for target, link := range map[string]string{
		"/dev/fd":     "/proc/self/fd",
		"/dev/stdin":  "/proc/self/fd/0",
		"/dev/stdout": "/proc/self/fd/1",
		"/dev/stderr": "/proc/self/fd/2",
		"/dev/ptmx":   "pts/ptmx",
	} {
		if err := ensureSandboxSymlink(bindMountTargetPath(rootfs, target), link); err != nil {
			return err
		}
	}
	return nil
}
