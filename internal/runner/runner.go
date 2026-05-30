package runner

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"github.com/DemonGiggle/mirage/internal/spec"
)

const (
	mountAttrReadOnly  = 0x00000001
	atRecursive        = 0x00008000
	atFDCWDUintptr     = ^uintptr(99)
	sysMountSetattr    = 442
	defaultSandboxPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	defaultSandboxUser = "mirage"
	defaultSandboxHome = "/home/" + defaultSandboxUser
)

func Execute(cfg spec.Config, stdout, stderr io.Writer) error {
	return execute(cfg, stdout, stderr)
}

func execute(cfg spec.Config, stdout, stderr io.Writer) error {
	if runtimeUnsupported() {
		return errors.New("sandbox backend currently supports Linux only")
	}

	if cfg.NetworkPolicy == nil {
		return errors.New("network policy backend plan is missing")
	}
	policyPlan, err := planNetworkPolicyBackend(cfg)
	if err != nil {
		return err
	}
	unshareArgs, err := buildUnshareArgs(cfg)
	if err != nil {
		return err
	}

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve mirage executable: %w", err)
	}

	backendArgs := []string{self, "__backend-exec", "--rootfs", cfg.RootFS, "--network-backend", policyPlan.BackendMode}
	if policyPlan.SerializedPolicy != "" {
		backendArgs = append(backendArgs, "--policy-config", policyPlan.SerializedPolicy)
	}
	if cfg.Cwd != "" {
		backendArgs = append(backendArgs, "--cwd", cfg.Cwd)
	}
	if cfg.Hostname != "" {
		backendArgs = append(backendArgs, "--hostname", cfg.Hostname)
	}
	for _, item := range cfg.ROBind {
		backendArgs = append(backendArgs, "--ro-bind", item)
	}
	for _, item := range cfg.RWBind {
		backendArgs = append(backendArgs, "--rw-bind", item)
	}
	for _, item := range cfg.Env {
		backendArgs = append(backendArgs, "--env", item)
	}
	backendArgs = append(backendArgs, "--")
	backendArgs = append(backendArgs, cfg.Command...)

	commandName := "unshare"
	commandArgs := append(unshareArgs, backendArgs...)
	var cmd *exec.Cmd
	if requiresCgroupScope(cfg) {
		cgroupArgs := []string{self, "__cgroup-exec"}
		if cfg.Memory != "" {
			cgroupArgs = append(cgroupArgs, "--memory", cfg.Memory)
		}
		if cfg.Pids > 0 {
			cgroupArgs = append(cgroupArgs, "--pids", strconv.Itoa(cfg.Pids))
		}
		cgroupArgs = append(cgroupArgs, "--", commandName)
		cgroupArgs = append(cgroupArgs, commandArgs...)
		cmd, err = buildDelegatedScopeCommand(cfg.ScopeName, cgroupArgs...)
		if err != nil {
			return err
		}
	} else {
		cmd = exec.Command(commandName, commandArgs...)
	}

	stdoutCloser, stdoutTarget, err := prepareLogWriter(cfg.StdoutLog, stdout)
	if err != nil {
		return err
	}
	defer closeQuietly(stdoutCloser)

	stderrCloser, stderrTarget, err := prepareLogWriter(cfg.StderrLog, stderr)
	if err != nil {
		return err
	}
	defer closeQuietly(stderrCloser)

	cmd.Stdout = stdoutTarget
	cmd.Stderr = stderrTarget
	cmd.Stdin = os.Stdin
	cmd.Env = os.Environ()

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("backend command failed: %w", err)
	}
	return nil
}

func RunCgroupHelper(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("__cgroup-exec", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var memory string
	var pids int

	fs.StringVar(&memory, "memory", "", "cgroup memory limit")
	fs.IntVar(&pids, "pids", 0, "cgroup pid limit")

	if err := fs.Parse(args); err != nil {
		return err
	}
	command := fs.Args()
	if len(command) == 0 {
		return errors.New("cgroup helper requires a command")
	}

	cleanup, err := enterCgroupLeaf(memory, pids)
	if err != nil {
		return err
	}
	defer cleanup()

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cgroup command failed: %w", err)
	}
	return nil
}

func RunBackendHelper(args []string, stdout, stderr io.Writer) error {
	_ = stdout
	_ = stderr

	fs := flag.NewFlagSet("__backend-exec", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var rootfs string
	var cwd string
	var hostname string
	var networkBackend string
	var policyConfig string
	var roBind []string
	var rwBind []string
	var envItems []string

	fs.StringVar(&rootfs, "rootfs", "", "backend rootfs")
	fs.StringVar(&cwd, "cwd", "", "backend cwd")
	fs.StringVar(&hostname, "hostname", "", "backend hostname")
	fs.StringVar(&networkBackend, "network-backend", "", "backend network mode")
	fs.StringVar(&policyConfig, "policy-config", "", "backend policy configuration")
	fs.Var(stringSliceValue{target: &roBind}, "ro-bind", "backend read-only bind mount")
	fs.Var(stringSliceValue{target: &rwBind}, "rw-bind", "backend read-write bind mount")
	fs.Var(stringSliceValue{target: &envItems}, "env", "backend environment variable")

	if err := fs.Parse(args); err != nil {
		return err
	}
	command := fs.Args()
	if len(command) == 0 {
		return errors.New("backend helper requires a command")
	}
	if networkBackend != backendNetworkPolicyHost && networkBackend != backendNetworkPolicyIsolated {
		return fmt.Errorf("unsupported backend network mode %q", networkBackend)
	}
	if networkBackend == backendNetworkPolicyIsolated {
		if err := configurePolicyNetworkBackend(policyConfig); err != nil {
			return err
		}
	}
	if hostname != "" {
		if err := syscall.Sethostname([]byte(hostname)); err != nil {
			return fmt.Errorf("set hostname: %w", err)
		}
	}

	if rootfs != "/" || len(roBind) > 0 || len(rwBind) > 0 {
		if err := makeMountNamespacePrivate(); err != nil {
			return err
		}
	}
	if rootfs != "" && rootfs != "/" {
		if err := prepareRootfsMountLayout(rootfs); err != nil {
			return err
		}
	}
	if len(roBind) > 0 || len(rwBind) > 0 {
		if err := applyBindMounts(rootfs, roBind, rwBind); err != nil {
			return err
		}
	}
	if rootfs != "" && rootfs != "/" {
		if err := syscall.Chroot(rootfs); err != nil {
			return fmt.Errorf("chroot to %q: %w", rootfs, err)
		}
		if err := os.Chdir("/"); err != nil {
			return fmt.Errorf("chdir after chroot: %w", err)
		}
	}

	if cwd != "" {
		if err := os.Chdir(cwd); err != nil {
			return fmt.Errorf("chdir to %q: %w", cwd, err)
		}
	}

	sandboxEnv, err := buildSandboxEnv(envItems, rootfs)
	if err != nil {
		return err
	}
	return runDirectCommand(command, rootfs, sandboxEnv)
}

func runDirectCommand(command []string, rootfs string, sandboxEnv []string) error {
	return execCommandInSandbox(command, rootfs, sandboxEnv)
}

func execCommandInSandbox(command []string, rootfs string, sandboxEnv []string) error {
	binary, err := resolveCommandBinary(command[0], rootfs, sandboxEnv)
	if err != nil {
		return err
	}
	return syscall.Exec(binary, command, sandboxEnv)
}

func requiresCgroupScope(cfg spec.Config) bool {
	return cfg.Memory != "" || cfg.Pids > 0
}

func buildDelegatedScopeCommand(unitName string, args ...string) (*exec.Cmd, error) {
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return nil, fmt.Errorf("cgroup limits require systemd-run on PATH: %w", err)
	}
	return exec.Command("systemd-run", delegatedScopeArgs(unitName, args...)...), nil
}

func delegatedScopeArgs(unitName string, args ...string) []string {
	scopeArgs := []string{"--user", "--scope", "--quiet", "--collect", "-p", "Delegate=yes", "--"}
	if unitName != "" {
		scopeArgs = append(scopeArgs[:len(scopeArgs)-1], "--unit="+unitName, "--")
	}
	scopeArgs = append(scopeArgs, args...)
	return scopeArgs
}

func enterCgroupLeaf(memory string, pids int) (cleanup func(), err error) {
	cgroupPath, err := currentCgroupPath()
	if err != nil {
		return nil, err
	}
	parentPath := filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(cgroupPath, "/"))
	leafPath := filepath.Join(parentPath, fmt.Sprintf("mirage-%d", os.Getpid()))
	if err := os.Mkdir(leafPath, 0o755); err != nil {
		return nil, fmt.Errorf("create cgroup leaf %q: %w", leafPath, err)
	}

	selfPID := strconv.Itoa(os.Getpid())
	selfInLeaf := false
	cleanup = func() {
		if selfInLeaf {
			if err := os.WriteFile(filepath.Join(parentPath, "cgroup.procs"), []byte(selfPID), 0o644); err == nil {
				selfInLeaf = false
			}
		}
		if err := os.Remove(leafPath); err != nil && !os.IsNotExist(err) && !selfInLeaf {
			_ = killCgroup(leafPath)
			_ = os.Remove(leafPath)
		}
	}
	defer func() {
		if err != nil && cleanup != nil {
			cleanup()
		}
	}()

	if err := os.WriteFile(filepath.Join(leafPath, "cgroup.procs"), []byte(selfPID), 0o644); err != nil {
		return nil, fmt.Errorf("move helper into cgroup leaf: %w", err)
	}
	selfInLeaf = true

	var controllers []string
	if memory != "" {
		controllers = append(controllers, "+memory")
	}
	if pids > 0 {
		controllers = append(controllers, "+pids")
	}
	if len(controllers) > 0 {
		if err := os.WriteFile(filepath.Join(parentPath, "cgroup.subtree_control"), []byte(strings.Join(controllers, " ")+"\n"), 0o644); err != nil {
			return nil, fmt.Errorf("enable cgroup controllers on %q: %w", parentPath, err)
		}
	}
	if memory != "" {
		if err := os.WriteFile(filepath.Join(leafPath, "memory.max"), []byte(memory+"\n"), 0o644); err != nil {
			return nil, fmt.Errorf("set memory limit on %q: %w", leafPath, err)
		}
		if err := writeOptionalCgroupFile(filepath.Join(leafPath, "memory.swap.max"), "0\n"); err != nil {
			return nil, fmt.Errorf("disable swap for %q: %w", leafPath, err)
		}
	}
	if pids > 0 {
		if err := os.WriteFile(filepath.Join(leafPath, "pids.max"), []byte(strconv.Itoa(pids)+"\n"), 0o644); err != nil {
			return nil, fmt.Errorf("set pid limit on %q: %w", leafPath, err)
		}
	}

	return cleanup, nil
}

func writeOptionalCgroupFile(path string, value string) error {
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func killCgroup(path string) error {
	if err := writeOptionalCgroupFile(filepath.Join(path, "cgroup.kill"), "1\n"); err != nil {
		return err
	}
	return nil
}

func currentCgroupPath() (string, error) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", fmt.Errorf("read current cgroup: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "0::") {
			path := strings.TrimPrefix(line, "0::")
			if path == "" {
				return "/", nil
			}
			return path, nil
		}
	}
	return "", errors.New("resolve current cgroup: unified cgroup v2 path not found")
}

func resolveCommandBinary(commandName string, rootfs string, sandboxEnv []string) (string, error) {
	binary, err := lookPathInEnv(commandName, envValue(sandboxEnv, "PATH", defaultSandboxPath))
	if err == nil {
		return binary, nil
	}
	if rootfs != "" && rootfs != "/" && !strings.ContainsRune(commandName, os.PathSeparator) {
		pathHint := fmt.Sprintf("using sandbox PATH %q", envValue(sandboxEnv, "PATH", defaultSandboxPath))
		return "", fmt.Errorf(
			"resolve command %q inside rootfs %q %s: %w; install the executable in the rootfs, set PATH for the sandbox, or invoke it by absolute path inside the rootfs",
			commandName,
			rootfs,
			pathHint,
			err,
		)
	}
	return "", fmt.Errorf("resolve command %q: %w", commandName, err)
}

func buildUnshareArgs(cfg spec.Config) ([]string, error) {
	args := []string{
		"--user",
		"--map-root-user",
		"--fork",
		"--pid",
		"--mount",
		"--uts",
		"--ipc",
	}

	plan, err := planNetworkPolicyBackend(cfg)
	if err != nil {
		return nil, err
	}
	if plan.BackendMode == backendNetworkPolicyIsolated {
		args = append(args, "--net")
	}
	return args, nil
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

func buildSandboxEnv(items []string, rootfs string) ([]string, error) {
	home, user, err := defaultSandboxIdentity(rootfs)
	if err != nil {
		return nil, err
	}
	env := []string{
		"PATH=" + defaultSandboxPath,
		"HOME=" + home,
		"USER=" + user,
		"LOGNAME=" + user,
	}
	for _, item := range items {
		key, _, ok := strings.Cut(item, "=")
		if !ok || key == "" {
			continue
		}
		env = setEnvValue(env, key, item)
	}
	return env, nil
}

func defaultSandboxIdentity(rootfs string) (home string, user string, err error) {
	if rootfs != "" && rootfs != "/" {
		return defaultSandboxHome, defaultSandboxUser, nil
	}

	home, err = os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("resolve host sandbox home: %w", err)
	}
	home = strings.TrimSpace(home)
	if home == "" {
		return "", "", errors.New("resolve host sandbox home: empty home directory")
	}

	user = strings.TrimSpace(os.Getenv("USER"))
	if user == "" {
		user = filepath.Base(home)
	}
	if user == "." || user == string(os.PathSeparator) || strings.TrimSpace(user) == "" {
		user = defaultSandboxUser
	}
	return home, user, nil
}

func envValue(items []string, key string, fallback string) string {
	prefix := key + "="
	for idx := len(items) - 1; idx >= 0; idx-- {
		if strings.HasPrefix(items[idx], prefix) {
			return strings.TrimPrefix(items[idx], prefix)
		}
	}
	return fallback
}

func setEnvValue(items []string, key string, value string) []string {
	prefix := key + "="
	for idx, item := range items {
		if strings.HasPrefix(item, prefix) {
			items[idx] = value
			return items
		}
	}
	return append(items, value)
}

func lookPathInEnv(file string, searchPath string) (string, error) {
	if strings.ContainsRune(file, os.PathSeparator) {
		return exec.LookPath(file)
	}
	for _, dir := range filepath.SplitList(searchPath) {
		if dir == "" {
			dir = "."
		}
		candidate := filepath.Join(dir, file)
		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if info.IsDir() || info.Mode().Perm()&0o111 == 0 {
			continue
		}
		return candidate, nil
	}
	return "", exec.ErrNotFound
}

func prepareLogWriter(path string, fallback io.Writer) (io.Closer, io.Writer, error) {
	if path == "" {
		return nil, fallback, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, fmt.Errorf("create log directory for %q: %w", path, err)
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file %q: %w", path, err)
	}
	return file, io.MultiWriter(fallback, file), nil
}

func closeQuietly(closer io.Closer) {
	if closer != nil {
		_ = closer.Close()
	}
}

func PlanNotes(cfg spec.Config) []string {
	var notes []string
	notes = append(notes, "execution backend: linux namespace runner")
	notes = append(notes, "execution mode: direct workload command becomes sandbox PID 1")
	notes = append(notes, "one sandbox = one isolated process tree")
	if cfg.NetworkPolicy != nil {
		if plan, err := planNetworkPolicyBackend(cfg); err == nil {
			switch plan.BackendMode {
			case backendNetworkPolicyHost:
				notes = append(notes, "network backend: allow-all policy via host namespace passthrough")
			case backendNetworkPolicyIsolated:
				notes = append(notes, fmt.Sprintf("network backend: isolated policy namespace (%s loopback)", plan.LoopbackAction))
			}
		} else {
			notes = append(notes, fmt.Sprintf("network backend: networkPolicy unsupported by current backend (%v)", err))
		}
	}
	if cfg.StdoutLog != "" || cfg.StderrLog != "" {
		var exports []string
		if cfg.StdoutLog != "" {
			exports = append(exports, "stdout")
		}
		if cfg.StderrLog != "" {
			exports = append(exports, "stderr")
		}
		notes = append(notes, fmt.Sprintf("host log export: %s", strings.Join(exports, "+")))
	}
	if len(cfg.ROBind) > 0 || len(cfg.RWBind) > 0 {
		notes = append(notes, "bind mounts: enforced read-only/read-write host path exposure")
	}
	if cfg.Memory != "" || cfg.Pids > 0 {
		var limits []string
		if cfg.Memory != "" {
			limits = append(limits, "memory="+cfg.Memory)
		}
		if cfg.Pids > 0 {
			limits = append(limits, fmt.Sprintf("pids=%d", cfg.Pids))
		}
		notes = append(notes, fmt.Sprintf("cgroup v2: enforced via delegated systemd user-scope leaf cgroup (%s)", strings.Join(limits, ", ")))
	}
	if cfg.ScopeName != "" {
		notes = append(notes, fmt.Sprintf("systemd user scope: %s", cfg.ScopeName))
	}
	if cfg.RootFS == "/" {
		notes = append(notes, "rootfs backend: host root")
	} else {
		notes = append(notes, "rootfs backend: mounted runtime layout plus chroot handoff")
	}
	return notes
}

func runtimeUnsupported() bool {
	return runtime.GOOS != "linux"
}

type stringSliceValue struct {
	target *[]string
}

func (s stringSliceValue) String() string {
	if s.target == nil || len(*s.target) == 0 {
		return ""
	}
	return strings.Join(*s.target, ",")
}

func (s stringSliceValue) Set(value string) error {
	*s.target = append(*s.target, value)
	return nil
}
