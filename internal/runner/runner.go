package runner

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/DemonGiggle/mirage/internal/spec"
)

const (
	mountAttrReadOnly = 0x00000001
	atRecursive       = 0x00008000
	atFDCWDUintptr    = ^uintptr(99)
	sysMountSetattr   = 442
)

func Execute(cfg spec.Config, stdout, stderr io.Writer) error {
	if runtimeUnsupported() {
		return errors.New("sandbox backend currently supports Linux only")
	}

	unshareArgs, err := buildUnshareArgs(cfg)
	if err != nil {
		return err
	}

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve mirage executable: %w", err)
	}

	backendArgs := []string{self, "__backend-exec", "--rootfs", cfg.RootFS, "--net", string(cfg.NetworkMode)}
	if cfg.Cwd != "" {
		backendArgs = append(backendArgs, "--cwd", cfg.Cwd)
	}
	if cfg.Hostname != "" {
		backendArgs = append(backendArgs, "--hostname", cfg.Hostname)
	}
	for _, warn := range cfg.Warn {
		backendArgs = append(backendArgs, "--warn", warn)
	}
	for _, item := range cfg.AllowCIDRs {
		backendArgs = append(backendArgs, "--allow-cidr", item)
	}
	for _, item := range cfg.AllowPorts {
		backendArgs = append(backendArgs, "--allow-port", item)
	}
	resolvedAllowHosts, err := resolveAllowHosts(cfg.AllowHosts)
	if err != nil {
		return err
	}
	for _, item := range resolvedAllowHosts {
		backendArgs = append(backendArgs, "--resolved-allow-host", item)
	}
	for _, item := range cfg.ROBind {
		backendArgs = append(backendArgs, "--ro-bind", item)
	}
	for _, item := range cfg.RWBind {
		backendArgs = append(backendArgs, "--rw-bind", item)
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
		cmd, err = buildDelegatedScopeCommand(cgroupArgs...)
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
	cmd.Env = append(os.Environ(), cfg.Env...)

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
	var netMode string
	var warnModes []string
	var allowCIDRs []string
	var allowPorts []string
	var resolvedAllowHosts []string
	var roBind []string
	var rwBind []string

	fs.StringVar(&rootfs, "rootfs", "", "backend rootfs")
	fs.StringVar(&cwd, "cwd", "", "backend cwd")
	fs.StringVar(&hostname, "hostname", "", "backend hostname")
	fs.StringVar(&netMode, "net", "", "backend network mode")
	fs.Var(stringSliceValue{target: &warnModes}, "warn", "backend warn mode")
	fs.Var(stringSliceValue{target: &allowCIDRs}, "allow-cidr", "backend allowed cidr")
	fs.Var(stringSliceValue{target: &allowPorts}, "allow-port", "backend allowed port")
	fs.Var(stringSliceValue{target: &resolvedAllowHosts}, "resolved-allow-host", "backend resolved allow-host")
	fs.Var(stringSliceValue{target: &roBind}, "ro-bind", "backend read-only bind mount")
	fs.Var(stringSliceValue{target: &rwBind}, "rw-bind", "backend read-write bind mount")

	if err := fs.Parse(args); err != nil {
		return err
	}
	command := fs.Args()
	if len(command) == 0 {
		return errors.New("backend helper requires a command")
	}
	if netMode != string(spec.NetworkHost) && netMode != string(spec.NetworkNone) && netMode != string(spec.NetworkIsolated) {
		return fmt.Errorf("unsupported backend network mode %q", netMode)
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

	policy, err := buildNetworkPolicy(netMode, warnModes, resolvedAllowHosts, allowCIDRs, allowPorts)
	if err != nil {
		return err
	}

	if shouldObserveNetwork(policy) {
		if err := EnsureObservedNetworkToolAvailable(); err == nil {
			return runObservedCommand(command, policy, stdout, stderr)
		}
	}

	binary, err := resolveCommandBinary(command[0], rootfs)
	if err != nil {
		return err
	}
	return syscall.Exec(binary, command, os.Environ())
}

func requiresCgroupScope(cfg spec.Config) bool {
	return cfg.Memory != "" || cfg.Pids > 0
}

func buildDelegatedScopeCommand(args ...string) (*exec.Cmd, error) {
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return nil, fmt.Errorf("cgroup limits require systemd-run on PATH: %w", err)
	}
	return exec.Command("systemd-run", delegatedScopeArgs(args...)...), nil
}

func delegatedScopeArgs(args ...string) []string {
	scopeArgs := []string{"--user", "--scope", "--quiet", "--collect", "-p", "Delegate=yes", "--"}
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

func resolveCommandBinary(commandName string, rootfs string) (string, error) {
	binary, err := exec.LookPath(commandName)
	if err == nil {
		return binary, nil
	}
	if rootfs != "" && rootfs != "/" && !strings.ContainsRune(commandName, os.PathSeparator) {
		pathHint := "using the current PATH"
		if os.Getenv("PATH") == "" {
			pathHint = "with an empty PATH"
		}
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

	switch cfg.NetworkMode {
	case spec.NetworkHost:
	case spec.NetworkIsolated:
	case spec.NetworkNone:
		args = append(args, "--net")
	default:
		return nil, fmt.Errorf("unsupported network mode %q", cfg.NetworkMode)
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

	if err := mountProc(filepath.Join(rootfs, "proc")); err != nil {
		return err
	}
	if err := mountTmpfs(filepath.Join(rootfs, "tmp"), "mode=1777"); err != nil {
		return err
	}
	if err := mountTmpfs(filepath.Join(rootfs, "run"), "mode=0755"); err != nil {
		return err
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

type networkPolicy struct {
	Mode               string
	WarnNet            bool
	ResolvedAllowHosts []hostPort
	AllowCIDRs         []netip.Prefix
	AllowPorts         []int
}

type hostPort struct {
	Host string
	Port int
}

type connectAttempt struct {
	Address string `json:"address"`
	Allowed bool   `json:"allowed"`
	Raw     string `json:"raw"`
}

type warnRecord struct {
	Timestamp   time.Time        `json:"timestamp"`
	NetworkMode string           `json:"network_mode"`
	Command     []string         `json:"command"`
	Attempts    []connectAttempt `json:"attempts"`
}

func buildNetworkPolicy(netMode string, warnModes []string, resolvedAllowHosts, allowCIDRs, allowPorts []string) (networkPolicy, error) {
	policy := networkPolicy{Mode: netMode}
	for _, item := range warnModes {
		if item == "net" {
			policy.WarnNet = true
		}
	}
	for _, item := range resolvedAllowHosts {
		host, port, err := net.SplitHostPort(item)
		if err != nil {
			return networkPolicy{}, fmt.Errorf("parse resolved allow-host %q: %w", item, err)
		}
		portNumber, err := strconv.Atoi(port)
		if err != nil {
			return networkPolicy{}, fmt.Errorf("parse resolved allow-host port %q: %w", item, err)
		}
		policy.ResolvedAllowHosts = append(policy.ResolvedAllowHosts, hostPort{Host: host, Port: portNumber})
	}
	for _, item := range allowCIDRs {
		prefix, err := netip.ParsePrefix(item)
		if err != nil {
			return networkPolicy{}, fmt.Errorf("parse allow-cidr %q: %w", item, err)
		}
		policy.AllowCIDRs = append(policy.AllowCIDRs, prefix)
	}
	for _, item := range allowPorts {
		portString := strings.TrimPrefix(item, "tcp/")
		portNumber, err := strconv.Atoi(portString)
		if err != nil {
			return networkPolicy{}, fmt.Errorf("parse allow-port %q: %w", item, err)
		}
		policy.AllowPorts = append(policy.AllowPorts, portNumber)
	}
	return policy, nil
}

func shouldObserveNetwork(policy networkPolicy) bool {
	return policy.Mode == string(spec.NetworkIsolated) || policy.WarnNet
}

func runObservedCommand(command []string, policy networkPolicy, stdout, stderr io.Writer) error {
	if err := EnsureObservedNetworkToolAvailable(); err != nil {
		return err
	}

	traceFile, err := os.CreateTemp("", "mirage-strace-*.log")
	if err != nil {
		return fmt.Errorf("create network trace file: %w", err)
	}
	tracePath := traceFile.Name()
	_ = traceFile.Close()
	defer os.Remove(tracePath)

	straceArgs := []string{"-f", "-e", "trace=connect", "-s", "0", "-o", tracePath, "--"}
	straceArgs = append(straceArgs, command...)
	cmd := exec.Command("strace", straceArgs...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = os.Environ()

	runErr := cmd.Run()

	attempts, parseErr := parseConnectAttempts(tracePath, policy)
	if parseErr != nil {
		return parseErr
	}
	if policy.WarnNet {
		if err := persistWarnRecord(policy, command, attempts); err != nil {
			return err
		}
	}
	if policy.Mode == string(spec.NetworkIsolated) {
		if err := enforceObservedPolicy(attempts); err != nil {
			return err
		}
	}
	if runErr != nil {
		return runErr
	}
	return nil
}

func EnsureObservedNetworkToolAvailable() error {
	if _, err := exec.LookPath("strace"); err != nil {
		return fmt.Errorf("observed isolated networking requires strace on PATH: %w", err)
	}
	return nil
}

func parseConnectAttempts(tracePath string, policy networkPolicy) ([]connectAttempt, error) {
	data, err := os.ReadFile(tracePath)
	if err != nil {
		return nil, fmt.Errorf("read network trace: %w", err)
	}

	var attempts []connectAttempt
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, "connect(") {
			continue
		}
		address, ok := extractConnectAddress(line)
		if !ok {
			attempts = append(attempts, connectAttempt{Address: "unknown", Allowed: false, Raw: line})
			continue
		}
		attempts = append(attempts, connectAttempt{
			Address: address,
			Allowed: isAllowedAddress(address, policy),
			Raw:     line,
		})
	}
	return attempts, nil
}

func extractConnectAddress(line string) (string, bool) {
	if idx := strings.Index(line, `sin_port=htons(`); idx >= 0 {
		portStart := idx + len(`sin_port=htons(`)
		portEnd := strings.Index(line[portStart:], ")")
		if portEnd < 0 {
			return "", false
		}
		port := line[portStart : portStart+portEnd]
		addrMarker := `inet_addr("`
		addrIdx := strings.Index(line, addrMarker)
		if addrIdx < 0 {
			return "", false
		}
		addrStart := addrIdx + len(addrMarker)
		addrEnd := strings.Index(line[addrStart:], `")`)
		if addrEnd < 0 {
			return "", false
		}
		host := line[addrStart : addrStart+addrEnd]
		return net.JoinHostPort(host, port), true
	}
	if idx := strings.Index(line, `sin6_port=htons(`); idx >= 0 {
		portStart := idx + len(`sin6_port=htons(`)
		portEnd := strings.Index(line[portStart:], ")")
		if portEnd < 0 {
			return "", false
		}
		port := line[portStart : portStart+portEnd]
		addrMarker := `inet_pton(AF_INET6, "`
		addrIdx := strings.Index(line, addrMarker)
		if addrIdx < 0 {
			return "", false
		}
		addrStart := addrIdx + len(addrMarker)
		addrEnd := strings.Index(line[addrStart:], `"`)
		if addrEnd < 0 {
			return "", false
		}
		host := line[addrStart : addrStart+addrEnd]
		return net.JoinHostPort(host, port), true
	}
	return "", false
}

func isAllowedAddress(address string, policy networkPolicy) bool {
	host, portString, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		return false
	}
	for _, allowed := range policy.ResolvedAllowHosts {
		if allowed.Host == host && allowed.Port == port {
			return true
		}
	}
	ip, err := netip.ParseAddr(host)
	if err == nil {
		for _, prefix := range policy.AllowCIDRs {
			if prefix.Contains(ip) {
				return true
			}
		}
	}
	for _, allowedPort := range policy.AllowPorts {
		if allowedPort == port {
			return true
		}
	}
	return false
}

func enforceObservedPolicy(attempts []connectAttempt) error {
	var disallowed []string
	for _, attempt := range attempts {
		if !attempt.Allowed {
			disallowed = append(disallowed, attempt.Address)
		}
	}
	if len(disallowed) == 0 {
		return nil
	}
	return fmt.Errorf("isolated network policy blocked attempted connections: %s", strings.Join(disallowed, ", "))
}

func persistWarnRecord(policy networkPolicy, command []string, attempts []connectAttempt) error {
	record := warnRecord{
		Timestamp:   time.Now().UTC(),
		NetworkMode: policy.Mode,
		Command:     append([]string{}, command...),
		Attempts:    attempts,
	}
	stateDir := os.Getenv("MIRAGE_STATE_DIR")
	if stateDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home for warn record: %w", err)
		}
		stateDir = filepath.Join(home, ".local", "state", "mirage")
	}
	warnDir := filepath.Join(stateDir, "warn")
	if err := os.MkdirAll(warnDir, 0o755); err != nil {
		return fmt.Errorf("create warn state directory: %w", err)
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal warn record: %w", err)
	}
	filename := filepath.Join(warnDir, fmt.Sprintf("net-%d.json", time.Now().UnixNano()))
	if err := os.WriteFile(filename, append(payload, '\n'), 0o644); err != nil {
		return fmt.Errorf("write warn record: %w", err)
	}
	return nil
}

func resolveAllowHosts(entries []string) ([]string, error) {
	var out []string
	for _, entry := range entries {
		host, port, err := net.SplitHostPort(entry)
		if err != nil {
			return nil, fmt.Errorf("parse allow-host %q: %w", entry, err)
		}
		if ip, err := netip.ParseAddr(host); err == nil {
			out = append(out, net.JoinHostPort(ip.String(), port))
			continue
		}
		ips, err := net.LookupIP(host)
		if err != nil {
			return nil, fmt.Errorf("resolve allow-host %q: %w", entry, err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("resolve allow-host %q: no addresses returned", entry)
		}
		for _, ip := range ips {
			out = append(out, net.JoinHostPort(ip.String(), port))
		}
	}
	return out, nil
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
	notes = append(notes, "one sandbox = one isolated process tree")
	switch cfg.NetworkMode {
	case spec.NetworkHost:
		notes = append(notes, "network backend: host namespace")
	case spec.NetworkNone:
		notes = append(notes, "network backend: dedicated net namespace without host network")
	case spec.NetworkIsolated:
		notes = append(notes, "network backend: host namespace with observed policy enforcement")
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
