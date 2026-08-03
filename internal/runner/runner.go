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

	"github.com/DemonGiggle/mirage/internal/spec"
)

const (
	mountAttrReadOnly  = 0x00000001
	atRecursive        = 0x00008000
	atFDCWDUintptr     = ^uintptr(99)
	sysMountSetattr    = 442
	defaultSandboxPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	defaultSandboxUser = "mirage"
	defaultSandboxHome = "/home/mirage"
	defaultSandboxHost = "oasis"
	defaultRootUser    = "root"
	defaultRootHome    = "/root"
	sandboxUID         = 1000
	sandboxGID         = 1000
	maxMappedGuestID   = 65535
)

func Execute(cfg spec.Config, stdout, stderr io.Writer) error {
	return execute(cfg, stdout, stderr)
}

func execute(cfg spec.Config, stdout, stderr io.Writer) error {
	if runtimeUnsupported() {
		return errors.New("sandbox backend currently supports Linux only")
	}
	cfg = applyConfigDefaults(cfg)

	if cfg.NetworkPolicy == nil {
		return errors.New("network policy backend plan is missing")
	}
	policyPlan, err := planNetworkPolicyBackend(cfg)
	if err != nil {
		return err
	}
	if policyPlan.BackendMode == backendNetworkPolicyRouted && requiresCgroupScope(cfg) {
		return errors.New("routed network policy backend does not yet support delegated cgroup execution")
	}
	self, cleanupReexecPath, err := prepareExternalReexecPath()
	if err != nil {
		return err
	}
	defer cleanupReexecPath()

	launchConfig := backendLaunchConfig{
		Self:             self,
		RootFS:           cfg.RootFS,
		Cwd:              cfg.Cwd,
		Hostname:         cfg.Hostname,
		NetworkBackend:   policyPlan.BackendMode,
		SerializedPolicy: policyPlan.SerializedPolicy,
		ROBind:           cfg.ROBind,
		RWBind:           cfg.RWBind,
		Env:              cfg.Env,
		RunAsRoot:        cfg.RunAsRoot,
		EnableSudo:       cfg.EnableSudo,
		Command:          cfg.Command,
	}
	var routedConfig routedNetworkConfig
	if policyPlan.BackendMode == backendNetworkPolicyRouted {
		if err := requireRoutedNetworkHostPrerequisites(); err != nil {
			return err
		}
		routedConfig, err = newRoutedNetworkConfig()
		if err != nil {
			return err
		}
		launchConfig.RoutedInterface = routedConfig.GuestIfName
		launchConfig.RoutedAddress = routedConfig.GuestCIDR
		launchConfig.RoutedGateway = routedConfig.HostAddress
		launchConfig.NetworkReadyFD = 3
	}
	backendArgs := launchConfig.args()

	launchSync, err := prepareSandboxLaunchSync(cfg.RunAsRoot, policyPlan.BackendMode)
	if err != nil {
		return err
	}
	defer launchSync.cleanup()
	unshareArgs, err := buildUnshareArgs(cfg.RunAsRoot, cfg.EnableSudo, policyPlan.BackendMode)
	if err != nil {
		return err
	}
	if !cfg.RunAsRoot && currentUID() == 0 {
		if err := clearInheritedSupplementaryGroups(); err != nil {
			return err
		}
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

	buildSandboxCommand := func(extraFiles []*os.File) (*exec.Cmd, error) {
		helperFiles := append([]*os.File{}, extraFiles...)
		targetPIDFD := -1
		if launchSync.targetPIDWriter != nil {
			targetPIDFD = 3 + len(helperFiles)
			helperFiles = append(helperFiles, launchSync.targetPIDWriter)
		}
		localBackendArgs := buildBackendLaunchArgs(backendArgs, launchSync.uidMapReadyFile, targetPIDFD)
		localCommandArgs := append(append([]string{}, unshareArgs...), localBackendArgs...)
		if requiresCgroupScope(cfg) {
			cgroupArgs := []string{self, "__cgroup-exec"}
			if cfg.Memory != "" {
				cgroupArgs = append(cgroupArgs, "--memory", cfg.Memory)
			}
			if cfg.Pids > 0 {
				cgroupArgs = append(cgroupArgs, "--pids", strconv.Itoa(cfg.Pids))
			}
			cgroupArgs = append(cgroupArgs, "--", "unshare")
			cgroupArgs = append(cgroupArgs, localCommandArgs...)
			cmd, err := buildDelegatedScopeCommand(cfg.ScopeName, cgroupArgs...)
			if err != nil {
				return nil, err
			}
			cmd.ExtraFiles = helperFiles
			return cmd, nil
		}

		cmd := exec.Command("unshare", localCommandArgs...)
		cmd.ExtraFiles = helperFiles
		return cmd, nil
	}

	if policyPlan.BackendMode != backendNetworkPolicyRouted {
		cmd, err := buildSandboxCommand(nil)
		if err != nil {
			return err
		}
		cmd.Stdout = stdoutTarget
		cmd.Stderr = stderrTarget
		cmd.Stdin = os.Stdin
		cmd.Env = os.Environ()
		if launchSync.uidMapReadyFile == "" {
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("backend command failed: %w", err)
			}
			return nil
		}
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start backend command: %w", err)
		}
		launchSync.closeTargetPIDWriter()
		targetPID, err := waitForSandboxTargetPID(launchSync.targetPIDReader)
		if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return err
		}
		if err := configureSandboxUIDMappings(targetPID, cfg.RunAsRoot, cfg.EnableSudo); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return err
		}
		if err := launchSync.signalUIDMapReady(); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return err
		}
		if err := cmd.Wait(); err != nil {
			return fmt.Errorf("backend command failed: %w", err)
		}
		return nil
	}

	syncRead, syncWrite, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("create routed network sync pipe: %w", err)
	}
	defer func() {
		closeQuietly(syncRead)
	}()
	defer func() {
		closeQuietly(syncWrite)
	}()

	cmd, err := buildSandboxCommand([]*os.File{syncRead})
	if err != nil {
		return err
	}
	cmd.Stdout = stdoutTarget
	cmd.Stderr = stderrTarget
	cmd.Stdin = os.Stdin
	cmd.Env = os.Environ()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start backend command: %w", err)
	}
	launchSync.closeTargetPIDWriter()
	closeQuietly(syncRead)
	syncRead = nil

	var targetPID int
	if launchSync.uidMapReadyFile != "" || policyPlan.BackendMode == backendNetworkPolicyRouted {
		targetPID, err = waitForSandboxTargetPID(launchSync.targetPIDReader)
		if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return err
		}
		if launchSync.uidMapReadyFile != "" {
			if err := configureSandboxUIDMappings(targetPID, cfg.RunAsRoot, cfg.EnableSudo); err != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				return err
			}
			if err := launchSync.signalUIDMapReady(); err != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				return err
			}
		}
	}

	cleanupNetwork, err := setupRoutedNetworkHost(targetPID, routedConfig)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	defer cleanupNetwork()

	if _, err := syncWrite.Write([]byte{1}); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("signal routed network readiness: %w", err)
	}
	closeQuietly(syncWrite)
	syncWrite = nil

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("backend command failed: %w", err)
	}
	return nil
}

func applyConfigDefaults(cfg spec.Config) spec.Config {
	if strings.TrimSpace(cfg.Hostname) == "" {
		cfg.Hostname = defaultSandboxHost
	}
	return cfg
}

func prepareExternalReexecPath() (string, func(), error) {
	self, err := os.Executable()
	if err != nil {
		return "", nil, fmt.Errorf("resolve mirage executable: %w", err)
	}

	source, err := os.Open(self)
	if err != nil {
		return "", nil, fmt.Errorf("open mirage executable %q: %w", self, err)
	}
	defer closeQuietly(source)

	temp, err := os.CreateTemp("", "mirage-self-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temporary mirage executable: %w", err)
	}
	cleanup := func() {
		closeQuietly(temp)
		_ = os.Remove(temp.Name())
	}
	if _, err := io.Copy(temp, source); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("copy mirage executable into temporary launch path: %w", err)
	}
	if err := temp.Chmod(0o755); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("mark temporary mirage executable %q executable: %w", temp.Name(), err)
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(temp.Name())
		return "", nil, fmt.Errorf("close temporary mirage executable %q: %w", temp.Name(), err)
	}

	return temp.Name(), func() {
		_ = os.Remove(temp.Name())
	}, nil
}

func selfReexecPath() string {
	return "/proc/self/exe"
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
	var routedInterface string
	var routedAddress string
	var routedGateway string
	var networkReadyFD int
	var roBind []string
	var rwBind []string
	var envItems []string
	var runAsRoot bool
	var enableSudo bool
	var targetPIDFD int
	var uidMapReadyFile string
	var mappedRootReady bool

	fs.StringVar(&rootfs, "rootfs", "", "backend rootfs")
	fs.StringVar(&cwd, "cwd", "", "backend cwd")
	fs.StringVar(&hostname, "hostname", "", "backend hostname")
	fs.StringVar(&networkBackend, "network-backend", "", "backend network mode")
	fs.StringVar(&policyConfig, "policy-config", "", "backend policy configuration")
	fs.StringVar(&routedInterface, "routed-interface", "", "backend routed interface name")
	fs.StringVar(&routedAddress, "routed-address", "", "backend routed interface address")
	fs.StringVar(&routedGateway, "routed-gateway", "", "backend routed default gateway")
	fs.IntVar(&networkReadyFD, "network-ready-fd", -1, "backend sync fd for host-side routed network setup")
	fs.Var(stringSliceValue{target: &roBind}, "ro-bind", "backend read-only bind mount")
	fs.Var(stringSliceValue{target: &rwBind}, "rw-bind", "backend read-write bind mount")
	fs.Var(stringSliceValue{target: &envItems}, "env", "backend environment variable")
	fs.BoolVar(&runAsRoot, "run-as-root", false, "backend workload identity")
	fs.BoolVar(&enableSudo, "sudo", false, "backend guest sudo capability")
	fs.IntVar(&targetPIDFD, "target-pid-fd", -1, "backend target pid publication fd")
	fs.StringVar(&uidMapReadyFile, "uid-map-ready-file", "", "backend uid/gid mapping readiness file")
	fs.BoolVar(&mappedRootReady, "mapped-root-ready", false, "backend privilege handoff completion marker")

	if err := fs.Parse(args); err != nil {
		return err
	}
	command := fs.Args()
	if len(command) == 0 {
		return errors.New("backend helper requires a command")
	}
	if err := writeTargetPIDFD(targetPIDFD); err != nil {
		return err
	}
	if networkBackend != backendNetworkPolicyHost && networkBackend != backendNetworkPolicyIsolated && networkBackend != backendNetworkPolicyRouted {
		return fmt.Errorf("unsupported backend network mode %q", networkBackend)
	}
	if uidMapReadyFile != "" && !mappedRootReady {
		if err := waitForUIDMapReady(uidMapReadyFile); err != nil {
			return err
		}
		if err := reexecBackendWithMappedRoot(rootfs, cwd, hostname, networkBackend, policyConfig, routedInterface, routedAddress, routedGateway, networkReadyFD, roBind, rwBind, envItems, runAsRoot, enableSudo, command); err != nil {
			return err
		}
		return nil
	}
	if rootfs != "/" || len(roBind) > 0 || len(rwBind) > 0 || !runAsRoot {
		if err := makeMountNamespacePrivate(); err != nil {
			return err
		}
	}
	if !runAsRoot {
		if err := prepareHostRootRuntimeLayout(); err != nil {
			return err
		}
	}
	if networkBackend == backendNetworkPolicyIsolated {
		if err := configurePolicyNetworkBackend(policyConfig); err != nil {
			return err
		}
	}
	if networkBackend == backendNetworkPolicyRouted {
		if err := waitForRoutedNetworkReady(networkReadyFD); err != nil {
			if errors.Is(err, errRoutedNetworkSetupAborted) {
				return nil
			}
			return err
		}
		if err := configureRoutedPolicyNetworkBackend(policyConfig, routedInterface, routedAddress, routedGateway); err != nil {
			return err
		}
	}
	if hostname != "" {
		if err := syscall.Sethostname([]byte(hostname)); err != nil {
			return fmt.Errorf("set hostname: %w", err)
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
	if networkBackend == backendNetworkPolicyRouted {
		if err := prepareRoutedResolverOverride(rootfs); err != nil {
			return err
		}
	}
	identity, err := prepareSandboxIdentity(rootfs, runAsRoot, enableSudo, hostname)
	if err != nil {
		return err
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

	sandboxEnv, err := buildSandboxEnv(envItems, identity)
	if err != nil {
		return err
	}
	command, err = stageSandboxCommand(command, rootfs, sandboxEnv, identity)
	if err != nil {
		return err
	}
	if err := applySandboxIdentity(identity); err != nil {
		return err
	}
	return runDirectCommand(command, rootfs, sandboxEnv, identity)
}

func runDirectCommand(command []string, rootfs string, sandboxEnv []string, identity sandboxIdentity) error {
	return execCommandInSandbox(command, rootfs, sandboxEnv, identity)
}

func execCommandInSandbox(command []string, rootfs string, sandboxEnv []string, identity sandboxIdentity) error {
	binary, err := resolveCommandBinary(command[0], rootfs, sandboxEnv)
	if err != nil {
		return err
	}
	if err := preflightUnsupportedPing(binary, identity); err != nil {
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
	scopeArgs := []string{"--scope", "--quiet", "--collect", "-p", "Delegate=yes", "--"}
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

	selfInLeaf := false
	selfPID := strconv.Itoa(os.Getpid())
	cleanup = cgroupLeafCleanup(leafPath, &selfInLeaf)
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

func cgroupLeafCleanup(leafPath string, selfInLeaf *bool) func() {
	return func() {
		if *selfInLeaf {
			// Once the helper has entered the leaf and enabled controllers on the
			// parent, cgroup v2's no-internal-process rule prevents moving the
			// helper back to the parent for in-process cleanup. The surrounding
			// systemd scope is responsible for tearing down the delegated subtree.
			return
		}
		_ = os.Remove(leafPath)
	}
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

func preflightUnsupportedPing(binary string, identity sandboxIdentity) error {
	if !isLikelyPingBinary(binary) {
		return nil
	}
	if identity.UID != 0 {
		return errors.New("ping is not supported in this Mirage sandbox on the current host/kernel because ICMP sockets are not available; use a TCP/HTTP probe instead")
	}

	for _, probe := range pingSocketProbes(binary) {
		fd, err := syscall.Socket(probe.domain, probe.typ, probe.protocol)
		if err == nil {
			_ = syscall.Close(fd)
			return nil
		}
		if !errors.Is(err, syscall.EPERM) && !errors.Is(err, syscall.EACCES) && !errors.Is(err, syscall.EAFNOSUPPORT) && !errors.Is(err, syscall.EPROTONOSUPPORT) {
			return nil
		}
	}

	return errors.New("ping is not supported in this Mirage sandbox on the current host/kernel because ICMP sockets are not available; use a TCP/HTTP probe instead")
}

type pingSocketProbe struct {
	domain   int
	typ      int
	protocol int
}

func pingSocketProbes(binary string) []pingSocketProbe {
	name := strings.ToLower(filepath.Base(binary))
	if name == "ping6" {
		return []pingSocketProbe{
			{domain: syscall.AF_INET6, typ: syscall.SOCK_DGRAM, protocol: syscall.IPPROTO_ICMPV6},
			{domain: syscall.AF_INET6, typ: syscall.SOCK_RAW, protocol: syscall.IPPROTO_ICMPV6},
		}
	}
	if name == "ping4" {
		return []pingSocketProbe{
			{domain: syscall.AF_INET, typ: syscall.SOCK_DGRAM, protocol: syscall.IPPROTO_ICMP},
			{domain: syscall.AF_INET, typ: syscall.SOCK_RAW, protocol: syscall.IPPROTO_ICMP},
		}
	}
	return []pingSocketProbe{
		{domain: syscall.AF_INET, typ: syscall.SOCK_DGRAM, protocol: syscall.IPPROTO_ICMP},
		{domain: syscall.AF_INET, typ: syscall.SOCK_RAW, protocol: syscall.IPPROTO_ICMP},
		{domain: syscall.AF_INET6, typ: syscall.SOCK_DGRAM, protocol: syscall.IPPROTO_ICMPV6},
		{domain: syscall.AF_INET6, typ: syscall.SOCK_RAW, protocol: syscall.IPPROTO_ICMPV6},
	}
}

func isLikelyPingBinary(binary string) bool {
	name := strings.ToLower(filepath.Base(binary))
	return name == "ping" || name == "ping4" || name == "ping6"
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

func formatCommandOutput(output []byte) string {
	text := strings.TrimSpace(string(output))
	if text == "" {
		return ""
	}
	return ": " + text
}

func closeQuietly(closer io.Closer) {
	if closer != nil {
		_ = closer.Close()
	}
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
