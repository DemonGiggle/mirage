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

	"github.com/DemonGiggle/mirage/internal/spec"
)

func Execute(cfg spec.Config, stdout, stderr io.Writer) error {
	if runtimeUnsupported() {
		return errors.New("sandbox backend currently supports Linux only")
	}
	if len(cfg.ROBind) > 0 || len(cfg.RWBind) > 0 {
		return errors.New("bind mounts are not implemented in the backend yet")
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
	backendArgs = append(backendArgs, "--")
	backendArgs = append(backendArgs, cfg.Command...)

	cmd := exec.Command("unshare", append(unshareArgs, backendArgs...)...)

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

	fs.StringVar(&rootfs, "rootfs", "", "backend rootfs")
	fs.StringVar(&cwd, "cwd", "", "backend cwd")
	fs.StringVar(&hostname, "hostname", "", "backend hostname")
	fs.StringVar(&netMode, "net", "", "backend network mode")
	fs.Var(stringSliceValue{target: &warnModes}, "warn", "backend warn mode")
	fs.Var(stringSliceValue{target: &allowCIDRs}, "allow-cidr", "backend allowed cidr")
	fs.Var(stringSliceValue{target: &allowPorts}, "allow-port", "backend allowed port")
	fs.Var(stringSliceValue{target: &resolvedAllowHosts}, "resolved-allow-host", "backend resolved allow-host")

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

	if rootfs != "" && rootfs != "/" {
		if err := prepareRootfsMountLayout(rootfs); err != nil {
			return err
		}
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
		return runObservedCommand(command, policy, stdout, stderr)
	}

	binary, err := exec.LookPath(command[0])
	if err != nil {
		return fmt.Errorf("resolve command %q: %w", command[0], err)
	}
	return syscall.Exec(binary, command, os.Environ())
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
	case spec.NetworkNone, spec.NetworkIsolated:
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
		notes = append(notes, "network backend: dedicated net namespace with observed policy enforcement")
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
		notes = append(notes, "bind mounts: parsed but not enforced yet")
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
