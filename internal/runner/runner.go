package runner

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
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
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
	defaultSandboxHome = "/home/mirage"
	defaultSandboxHost = "oasis"
	defaultRootUser    = "root"
	defaultRootHome    = "/root"
	sandboxUID         = 1000
	sandboxGID         = 1000
	maxMappedGuestID   = 65535
)

var (
	currentUID         = os.Getuid
	currentGID         = os.Getgid
	currentGroups      = os.Getgroups
	chownFunc          = os.Lchown
	idMapCommandRunner = runIDMapCommand
	procfsRoot         = "/proc"
	setgroupsFunc      = syscall.Setgroups
	setgidFunc         = syscall.Setgid
	setuidFunc         = syscall.Setuid
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

	backendArgs := []string{self, "__backend-exec", "--rootfs", cfg.RootFS, "--network-backend", policyPlan.BackendMode}
	if policyPlan.SerializedPolicy != "" {
		backendArgs = append(backendArgs, "--policy-config", policyPlan.SerializedPolicy)
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
		backendArgs = append(backendArgs,
			"--routed-interface", routedConfig.GuestIfName,
			"--routed-address", routedConfig.GuestCIDR,
			"--routed-gateway", routedConfig.HostAddress,
			"--network-ready-fd", "3",
		)
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
	if cfg.RunAsRoot {
		backendArgs = append(backendArgs, "--run-as-root")
	}
	backendArgs = append(backendArgs, "--")
	backendArgs = append(backendArgs, cfg.Command...)

	launchSync, err := prepareSandboxLaunchSync(cfg.RunAsRoot, policyPlan.BackendMode)
	if err != nil {
		return err
	}
	defer launchSync.cleanup()
	unshareArgs, err := buildUnshareArgs(cfg.RunAsRoot, policyPlan.BackendMode)
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
			targetPID, err = waitForSandboxLeafPID(cmd.Process.Pid)
			if err != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				return err
			}
		}
		if err := configureSandboxUIDMappings(targetPID, cfg.RunAsRoot); err != nil {
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
			targetPID, err = waitForSandboxLeafPID(cmd.Process.Pid)
			if err != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				return err
			}
		}
		if launchSync.uidMapReadyFile != "" {
			if err := configureSandboxUIDMappings(targetPID, cfg.RunAsRoot); err != nil {
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
		if err := reexecBackendWithMappedRoot(rootfs, cwd, hostname, networkBackend, policyConfig, routedInterface, routedAddress, routedGateway, networkReadyFD, roBind, rwBind, envItems, runAsRoot, command); err != nil {
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
	identity, err := prepareSandboxIdentity(rootfs, runAsRoot)
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
	if !runAsRoot {
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

func waitForSandboxLeafPID(parentPID int) (int, error) {
	childrenPath := fmt.Sprintf("/proc/%d/task/%d/children", parentPID, parentPID)
	if _, err := os.Stat(childrenPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if _, statErr := os.Stat(fmt.Sprintf("/proc/%d", parentPID)); statErr == nil {
				return 0, errors.New("sandbox tracking requires /proc/<pid>/task/<tid>/children support on the host kernel")
			}
		}
		return 0, fmt.Errorf("check sandbox child pid support for %d: %w", parentPID, err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		pid, err := findLeafDescendantPID(parentPID)
		if err == nil && pid > 0 && pid != parentPID {
			return pid, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return 0, err
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("timed out waiting for sandbox descendant pid from parent %d", parentPID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func findLeafDescendantPID(pid int) (int, error) {
	childrenPath := fmt.Sprintf("/proc/%d/task/%d/children", pid, pid)
	content, err := os.ReadFile(childrenPath)
	if err != nil {
		return 0, fmt.Errorf("read sandbox child pid list for %d: %w", pid, err)
	}
	fields := strings.Fields(string(content))
	if len(fields) == 0 {
		return pid, nil
	}
	lastField := fields[len(fields)-1]
	childPID, err := strconv.Atoi(lastField)
	if err != nil {
		return 0, fmt.Errorf("parse sandbox child pid %q: %w", lastField, err)
	}
	if childPID <= 0 {
		return 0, fmt.Errorf("sandbox child pid %d is invalid", childPID)
	}
	return findLeafDescendantPID(childPID)
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
	if _, err := fmt.Fprintf(file, "%d\n", os.Getpid()); err != nil {
		return fmt.Errorf("write sandbox target pid to fd %d: %w", fd, err)
	}
	return nil
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

type sandboxIdentity struct {
	UID  int
	GID  int
	Home string
	User string
}

func buildSandboxEnv(items []string, identity sandboxIdentity) ([]string, error) {
	env := []string{
		"PATH=" + defaultSandboxPath,
		"HOME=" + identity.Home,
		"USER=" + identity.User,
		"LOGNAME=" + identity.User,
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

func prepareSandboxIdentity(rootfs string, runAsRoot bool) (sandboxIdentity, error) {
	identity := defaultSandboxIdentity(rootfs, runAsRoot)
	if runAsRoot {
		if rootfs != "" && rootfs != "/" {
			if err := ensureDir(bindMountTargetPath(rootfs, identity.Home), 0o755); err != nil {
				return sandboxIdentity{}, fmt.Errorf("prepare root home %q: %w", identity.Home, err)
			}
		}
		return identity, nil
	}
	if err := ensureOwnedDir(bindMountTargetPath(rootfs, identity.Home), 0o755, identity.UID, identity.GID); err != nil {
		return sandboxIdentity{}, fmt.Errorf("prepare sandbox home %q: %w", identity.Home, err)
	}
	if err := applySandboxIdentityFiles(rootfs, identity); err != nil {
		return sandboxIdentity{}, err
	}
	return identity, nil
}

func defaultSandboxIdentity(rootfs string, runAsRoot bool) sandboxIdentity {
	if runAsRoot {
		return sandboxIdentity{
			UID:  0,
			GID:  0,
			Home: defaultRootHome,
			User: defaultRootUser,
		}
	}
	home := defaultSandboxHome
	if rootfs == "/" {
		home = hostSandboxHome()
	}
	return sandboxIdentity{
		UID:  sandboxUID,
		GID:  sandboxGID,
		Home: home,
		User: defaultSandboxUser,
	}
}

func hostSandboxHome() string {
	suffix := strconv.Itoa(os.Getuid())
	if currentUser, err := user.Current(); err == nil {
		if username := sanitizePathComponent(currentUser.Username); username != "" {
			suffix = username
		}
	}
	return filepath.Join("/tmp", "mirage-home-"+suffix)
}

func sanitizePathComponent(value string) string {
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-', r == '_', r == '.':
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func applySandboxIdentityFiles(rootfs string, identity sandboxIdentity) error {
	contents := sandboxIdentityFileContents(identity)
	for target, content := range contents {
		sourcePath, err := writeRuntimeIdentityFile(content, sandboxIdentityFileMode(target))
		if err != nil {
			return fmt.Errorf("prepare sandbox identity file %q: %w", target, err)
		}
		defer func(path string) {
			_ = os.Remove(path)
		}(sourcePath)
		if err := applyBindMount(rootfs, bindMount{Source: sourcePath, Target: target, ReadOnly: true}); err != nil {
			return fmt.Errorf("install sandbox identity file %q: %w", target, err)
		}
	}
	return nil
}

func sandboxIdentityFileMode(target string) os.FileMode {
	switch target {
	case "/etc/shadow", "/etc/gshadow":
		return 0o640
	default:
		return 0o644
	}
}

func sandboxIdentityFileContents(identity sandboxIdentity) map[string]string {
	return map[string]string{
		"/etc/passwd":  fmt.Sprintf("root:x:0:0:root:%s:/bin/sh\n%s:x:%d:%d:%s:%s:/bin/sh\n", defaultRootHome, identity.User, identity.UID, identity.GID, identity.User, identity.Home),
		"/etc/group":   fmt.Sprintf("root:x:0:\n%s:x:%d:\n", identity.User, identity.GID),
		"/etc/shadow":  fmt.Sprintf("root:!:1:0:99999:7:::\n%s:!:1:0:99999:7:::\n", identity.User),
		"/etc/gshadow": fmt.Sprintf("root:!::\n%s:!::\n", identity.User),
		"/etc/nsswitch.conf": strings.Join([]string{
			"passwd: files",
			"group: files",
			"shadow: files",
			"gshadow: files",
			"hosts: files dns",
			"networks: files",
			"protocols: files",
			"services: files",
			"ethers: files",
			"rpc: files",
			"netgroup: files",
			"",
		}, "\n"),
	}
}

func writeRuntimeIdentityFile(content string, mode os.FileMode) (string, error) {
	file, err := os.CreateTemp("", "mirage-identity-*")
	if err != nil {
		return "", err
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return "", err
	}
	if err := os.Chmod(file.Name(), mode); err != nil {
		_ = os.Remove(file.Name())
		return "", err
	}
	return file.Name(), nil
}

func prepareHostRootRuntimeLayout() error {
	if err := mountTmpfs("/run", "mode=0755"); err != nil {
		return fmt.Errorf("mount runtime /run tmpfs: %w", err)
	}
	return nil
}

func stageSandboxCommand(command []string, rootfs string, sandboxEnv []string, identity sandboxIdentity) ([]string, error) {
	if rootfs != "/" || identity.UID == 0 || len(command) == 0 {
		return command, nil
	}

	binary, err := resolveCommandBinary(command[0], rootfs, sandboxEnv)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(binary, "/tmp/") {
		return command, nil
	}

	stageDir := "/run/mirage-bin"
	if err := ensureDir(stageDir, 0o755); err != nil {
		return nil, fmt.Errorf("prepare staged command dir: %w", err)
	}
	source, err := os.Open(binary)
	if err != nil {
		return nil, fmt.Errorf("open staged command source %q: %w", binary, err)
	}
	defer source.Close()

	stagedPath := filepath.Join(stageDir, filepath.Base(binary))
	target, err := os.OpenFile(stagedPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return nil, fmt.Errorf("create staged command %q: %w", stagedPath, err)
	}
	if _, err := io.Copy(target, source); err != nil {
		_ = target.Close()
		return nil, fmt.Errorf("copy staged command %q: %w", stagedPath, err)
	}
	if err := target.Close(); err != nil {
		return nil, fmt.Errorf("close staged command %q: %w", stagedPath, err)
	}
	if err := os.Chown(stageDir, identity.UID, identity.GID); err != nil {
		return nil, fmt.Errorf("chown staged command dir %q: %w", stageDir, err)
	}
	if err := os.Chown(stagedPath, identity.UID, identity.GID); err != nil {
		return nil, fmt.Errorf("chown staged command %q: %w", stagedPath, err)
	}

	stagedCommand := append([]string(nil), command...)
	stagedCommand[0] = stagedPath
	return stagedCommand, nil
}

func applySandboxIdentity(identity sandboxIdentity) error {
	if identity.UID == 0 && identity.GID == 0 {
		return nil
	}
	if err := clearInheritedSupplementaryGroups(); err != nil {
		return fmt.Errorf("clear supplementary groups: %w", err)
	}
	if err := setgidFunc(identity.GID); err != nil {
		return fmt.Errorf("drop sandbox gid to %d: %w", identity.GID, err)
	}
	if err := setuidFunc(identity.UID); err != nil {
		return fmt.Errorf("drop sandbox uid to %d: %w", identity.UID, err)
	}
	return nil
}

func clearInheritedSupplementaryGroups() error {
	if err := setgroupsFunc(nil); err != nil {
		if errors.Is(err, syscall.EPERM) {
			groups, groupsErr := currentGroups()
			if groupsErr == nil && len(groups) == 0 {
				return nil
			}
		}
		return err
	}
	return nil
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
			case backendNetworkPolicyRouted:
				notes = append(notes, fmt.Sprintf("network backend: routed policy namespace (%s loopback, host NAT uplink)", plan.LoopbackAction))
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
		notes = append(notes, fmt.Sprintf("cgroup v2: enforced via delegated systemd scope leaf cgroup (%s)", strings.Join(limits, ", ")))
	}
	if cfg.ScopeName != "" {
		notes = append(notes, fmt.Sprintf("systemd scope: %s", cfg.ScopeName))
	}
	if cfg.RootFS == "/" {
		notes = append(notes, "rootfs backend: host root")
	} else {
		notes = append(notes, "rootfs backend: mounted runtime layout plus chroot handoff")
	}
	if cfg.RunAsRoot {
		notes = append(notes, "workload identity: root (explicit via --run-as-root)")
	} else {
		notes = append(notes, fmt.Sprintf("workload identity: non-root %s (%d:%d)", defaultSandboxUser, sandboxUID, sandboxGID))
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
