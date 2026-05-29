package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/DemonGiggle/mirage/internal/rootfs"
	"github.com/DemonGiggle/mirage/internal/spec"
)

var (
	errSandboxUnitNotFound = errors.New("sandbox unit not found")

	sandboxValidateInitRootfs = rootfs.ValidateInitRootfs
	sandboxLaunchProcess      = launchSandboxProcess
	sandboxShowUserUnit       = showSandboxUserUnit
	sandboxStopUserUnit       = stopSandboxUserUnit
	sandboxKillUserUnit       = killSandboxUserUnit
	sandboxStateRootDir       = defaultSandboxStateRootDir
)

var sandboxNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

type sandboxState struct {
	Name        string    `json:"name"`
	Unit        string    `json:"unit"`
	RootFS      string    `json:"rootfs"`
	ServiceUnit string    `json:"service_unit,omitempty"`
	StdoutLog   string    `json:"stdout_log"`
	StderrLog   string    `json:"stderr_log"`
	LaunchLog   string    `json:"launch_log"`
	Command     []string  `json:"command"`
	StartedAt   time.Time `json:"started_at"`
	StoppedAt   time.Time `json:"stopped_at,omitempty"`
}

type sandboxUnitStatus struct {
	LoadState   string
	ActiveState string
	SubState    string
	Result      string
}

type sandboxLaunchRequest struct {
	RunArgs   []string
	LaunchLog string
}

func runSandboxCommand(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("sandbox requires a subcommand")
	}
	switch args[0] {
	case "start":
		return runSandboxStart(args[1:], stdout, stderr)
	case "status":
		return runSandboxStatus(args[1:], stdout, stderr)
	case "stop":
		return runSandboxStop(args[1:], stdout, stderr)
	case "logs":
		return runSandboxLogs(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown sandbox subcommand %q", args[0])
	}
}

func runSandboxStart(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("sandbox start", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var cfg spec.Config
	var name string
	var serviceUnit string
	var stateDir string

	fs.StringVar(&name, "name", "", "Tracked sandbox name")
	fs.StringVar(&serviceUnit, "service-unit", "", "Systemd unit to validate and describe for this sandbox")
	fs.StringVar(&stateDir, "state-dir", "", "Override the sandbox state root directory")
	fs.StringVar(&cfg.RootFS, "rootfs", "", "Path to the sandbox root filesystem")
	fs.Var(stringSliceValue{target: &cfg.ROBind}, "ro-bind", "Read-only bind mount host:guest")
	fs.Var(stringSliceValue{target: &cfg.RWBind}, "rw-bind", "Writable bind mount host:guest")
	fs.Var(stringSliceValue{target: &cfg.Env}, "env", "Environment variable in KEY=VALUE form")
	fs.StringVar(&cfg.NetworkPolicyFile, "network-policy-file", "", "Path to a standalone networkPolicy YAML file")
	fs.StringVar(&cfg.Preset, "preset", "", "Named preset to apply before inline overrides")
	fs.StringVar(&cfg.PresetFile, "preset-file", "", "Path to a local preset YAML file")
	fs.StringVar(&cfg.StdoutLog, "stdout-log", "", "Write guest init stdout to a host-side log file")
	fs.StringVar(&cfg.StderrLog, "stderr-log", "", "Write guest init stderr to a host-side log file")
	fs.StringVar(&cfg.Cwd, "cwd", "", "Working directory inside the sandbox")
	fs.StringVar(&cfg.Hostname, "hostname", "", "Hostname inside the sandbox")
	fs.StringVar(&cfg.Memory, "memory", "", "Memory limit, for example 512M")
	fs.IntVar(&cfg.Pids, "pids", 0, "PID limit")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if !sandboxNamePattern.MatchString(name) {
		return errors.New("sandbox start requires --name matching [A-Za-z0-9][A-Za-z0-9_-]*")
	}
	cfg.RuntimeMode = spec.RuntimeModeInit
	cfg.Command = fs.Args()
	cfg.ScopeName = sandboxScopeUnit(name)
	if err := loadConfigNetworkPolicy(&cfg); err != nil {
		return err
	}

	rootDir, err := sandboxStateRootDir(stateDir)
	if err != nil {
		return err
	}
	sandboxDir := filepath.Join(rootDir, name)
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		return fmt.Errorf("create sandbox state directory %q: %w", sandboxDir, err)
	}

	if cfg.StdoutLog == "" {
		cfg.StdoutLog = filepath.Join(sandboxDir, "stdout.log")
	}
	if cfg.StderrLog == "" {
		cfg.StderrLog = filepath.Join(sandboxDir, "stderr.log")
	}
	launchLog := filepath.Join(sandboxDir, "launch.log")
	statePath := filepath.Join(sandboxDir, "state.json")

	for _, path := range []string{cfg.StdoutLog, cfg.StderrLog, launchLog} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("reset sandbox log %q: %w", path, err)
		}
	}

	if err := prepareSandboxStatePath(statePath); err != nil {
		return err
	}

	resolved, err := spec.ApplyPreset(cfg)
	if err != nil {
		_ = os.Remove(statePath)
		return err
	}
	if resolved.NetworkPolicy == nil {
		resolved.Preset = "allow-all"
		resolved.PresetFile = ""
		policy := spec.AllowAllNetworkPolicy()
		resolved.NetworkPolicy = &policy
	}
	if err := ensurePresetRootfs(resolved, stderr); err != nil {
		_ = os.Remove(statePath)
		return err
	}
	report, err := sandboxValidateInitRootfs(resolved.RootFS, firstCommand(resolved.Command), serviceUnit)
	if err != nil {
		_ = os.Remove(statePath)
		return err
	}
	if len(resolved.Command) == 0 {
		resolved.Command = []string{report.ResolvedInit}
	}
	if err := spec.Validate(resolved); err != nil {
		_ = os.Remove(statePath)
		return err
	}

	state := sandboxState{
		Name:        name,
		Unit:        resolved.ScopeName,
		RootFS:      resolved.RootFS,
		ServiceUnit: serviceUnit,
		StdoutLog:   resolved.StdoutLog,
		StderrLog:   resolved.StderrLog,
		LaunchLog:   launchLog,
		Command:     append([]string{}, resolved.Command...),
		StartedAt:   time.Now().UTC(),
	}
	if err := writeSandboxState(statePath, state); err != nil {
		_ = os.Remove(statePath)
		return err
	}

	runArgs := buildSandboxRunArgs(resolved)
	if err := sandboxLaunchProcess(sandboxLaunchRequest{RunArgs: runArgs, LaunchLog: launchLog}); err != nil {
		_ = os.Remove(statePath)
		return err
	}

	_, _ = fmt.Fprintln(stdout, "mirage sandbox start")
	_, _ = fmt.Fprintf(stdout, "name: %s\n", state.Name)
	_, _ = fmt.Fprintf(stdout, "unit: %s\n", state.Unit)
	_, _ = fmt.Fprintf(stdout, "rootfs: %s\n", state.RootFS)
	if state.ServiceUnit != "" {
		_, _ = fmt.Fprintf(stdout, "service-unit: %s\n", state.ServiceUnit)
	}
	_, _ = fmt.Fprintf(stdout, "stdout-log: %s\n", state.StdoutLog)
	_, _ = fmt.Fprintf(stdout, "stderr-log: %s\n", state.StderrLog)
	_, _ = fmt.Fprintf(stdout, "launch-log: %s\n", state.LaunchLog)
	_, _ = fmt.Fprintln(stdout, "status: starting")
	return nil
}

func runSandboxStatus(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("sandbox status", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var name string
	var stateDir string

	fs.StringVar(&name, "name", "", "Tracked sandbox name")
	fs.StringVar(&stateDir, "state-dir", "", "Override the sandbox state root directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if name == "" {
		return errors.New("sandbox status requires --name")
	}

	state, statePath, err := loadSandboxStateByName(stateDir, name)
	if err != nil {
		return err
	}
	status, err := sandboxShowUserUnit(state.Unit)
	if err != nil && !errors.Is(err, errSandboxUnitNotFound) {
		return err
	}

	_, _ = fmt.Fprintln(stdout, "mirage sandbox status")
	_, _ = fmt.Fprintf(stdout, "name: %s\n", state.Name)
	_, _ = fmt.Fprintf(stdout, "unit: %s\n", state.Unit)
	if err == nil {
		_, _ = fmt.Fprintf(stdout, "load-state: %s\n", status.LoadState)
		_, _ = fmt.Fprintf(stdout, "active-state: %s\n", status.ActiveState)
		_, _ = fmt.Fprintf(stdout, "sub-state: %s\n", status.SubState)
		if status.Result != "" {
			_, _ = fmt.Fprintf(stdout, "result: %s\n", status.Result)
		}
	} else {
		_, _ = fmt.Fprintln(stdout, "active-state: inactive")
		_, _ = fmt.Fprintln(stdout, "sub-state: not-found")
	}
	_, _ = fmt.Fprintf(stdout, "rootfs: %s\n", state.RootFS)
	if state.ServiceUnit != "" {
		_, _ = fmt.Fprintf(stdout, "service-unit: %s\n", state.ServiceUnit)
	}
	_, _ = fmt.Fprintf(stdout, "stdout-log: %s\n", state.StdoutLog)
	_, _ = fmt.Fprintf(stdout, "stderr-log: %s\n", state.StderrLog)
	_, _ = fmt.Fprintf(stdout, "launch-log: %s\n", state.LaunchLog)
	_, _ = fmt.Fprintf(stdout, "state-file: %s\n", statePath)
	_, _ = fmt.Fprintf(stdout, "started-at: %s\n", state.StartedAt.Format(time.RFC3339))
	if !state.StoppedAt.IsZero() {
		_, _ = fmt.Fprintf(stdout, "stopped-at: %s\n", state.StoppedAt.Format(time.RFC3339))
	}
	_, _ = fmt.Fprintf(stdout, "command: %s\n", strings.Join(state.Command, " "))
	return nil
}

func runSandboxStop(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("sandbox stop", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var name string
	var stateDir string
	var timeout time.Duration

	fs.StringVar(&name, "name", "", "Tracked sandbox name")
	fs.StringVar(&stateDir, "state-dir", "", "Override the sandbox state root directory")
	fs.DurationVar(&timeout, "timeout", 10*time.Second, "Grace period before escalating to SIGKILL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if name == "" {
		return errors.New("sandbox stop requires --name")
	}

	state, statePath, err := loadSandboxStateByName(stateDir, name)
	if err != nil {
		return err
	}
	status, err := sandboxShowUserUnit(state.Unit)
	switch {
	case err == nil && isActiveSandboxUnit(status):
		if err := sandboxStopUserUnit(state.Unit); err != nil {
			return err
		}
	case err == nil:
	case errors.Is(err, errSandboxUnitNotFound):
	default:
		return err
	}

	deadline := time.Now().Add(timeout)
	for {
		status, err = sandboxShowUserUnit(state.Unit)
		if errors.Is(err, errSandboxUnitNotFound) || (err == nil && !isActiveSandboxUnit(status)) {
			break
		}
		if err != nil {
			return err
		}
		if time.Now().After(deadline) {
			if err := sandboxKillUserUnit(state.Unit); err != nil {
				return err
			}
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	state.StoppedAt = time.Now().UTC()
	if err := writeSandboxState(statePath, state); err != nil {
		return err
	}

	_, _ = fmt.Fprintln(stdout, "mirage sandbox stop")
	_, _ = fmt.Fprintf(stdout, "name: %s\n", state.Name)
	_, _ = fmt.Fprintf(stdout, "unit: %s\n", state.Unit)
	_, _ = fmt.Fprintln(stdout, "active-state: inactive")
	return nil
}

func runSandboxLogs(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("sandbox logs", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var name string
	var stateDir string
	var lines int
	var stdoutOnly bool
	var stderrOnly bool
	var launchOnly bool

	fs.StringVar(&name, "name", "", "Tracked sandbox name")
	fs.StringVar(&stateDir, "state-dir", "", "Override the sandbox state root directory")
	fs.IntVar(&lines, "lines", 40, "Number of trailing lines to print")
	fs.BoolVar(&stdoutOnly, "stdout", false, "Show only the guest init stdout log")
	fs.BoolVar(&stderrOnly, "stderr", false, "Show only the guest init stderr log")
	fs.BoolVar(&launchOnly, "launch", false, "Show only the launch/control log")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if name == "" {
		return errors.New("sandbox logs requires --name")
	}
	if lines <= 0 {
		return errors.New("sandbox logs requires --lines to be positive")
	}
	if boolCount(stdoutOnly, stderrOnly, launchOnly) > 1 {
		return errors.New("sandbox logs accepts at most one of --stdout, --stderr, or --launch")
	}

	state, _, err := loadSandboxStateByName(stateDir, name)
	if err != nil {
		return err
	}

	switch {
	case stdoutOnly:
		return printSandboxLog(stdout, state.StdoutLog, lines)
	case stderrOnly:
		return printSandboxLog(stdout, state.StderrLog, lines)
	case launchOnly:
		return printSandboxLog(stdout, state.LaunchLog, lines)
	default:
		if err := printTitledSandboxLog(stdout, "stdout", state.StdoutLog, lines); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout)
		return printTitledSandboxLog(stdout, "stderr", state.StderrLog, lines)
	}
}

func prepareSandboxStatePath(statePath string) error {
	file, err := os.OpenFile(statePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err == nil {
		return file.Close()
	}
	if !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create sandbox state file %q: %w", statePath, err)
	}

	state, loadErr := readSandboxState(statePath)
	if loadErr != nil {
		return fmt.Errorf("sandbox state %q already exists and could not be read: %w", statePath, loadErr)
	}
	status, statusErr := sandboxShowUserUnit(state.Unit)
	if statusErr == nil && isActiveSandboxUnit(status) {
		return fmt.Errorf("sandbox %q is already running (%s)", state.Name, state.Unit)
	}
	if statusErr != nil && !errors.Is(statusErr, errSandboxUnitNotFound) {
		return statusErr
	}
	if err := os.Remove(statePath); err != nil {
		return fmt.Errorf("reset sandbox state %q: %w", statePath, err)
	}
	file, err = os.OpenFile(statePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("recreate sandbox state file %q: %w", statePath, err)
	}
	return file.Close()
}

func loadSandboxStateByName(stateDir string, name string) (sandboxState, string, error) {
	rootDir, err := sandboxStateRootDir(stateDir)
	if err != nil {
		return sandboxState{}, "", err
	}
	statePath := filepath.Join(rootDir, name, "state.json")
	state, err := readSandboxState(statePath)
	if err != nil {
		return sandboxState{}, "", err
	}
	return state, statePath, nil
}

func readSandboxState(path string) (sandboxState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return sandboxState{}, fmt.Errorf("read sandbox state %q: %w", path, err)
	}
	var state sandboxState
	if err := json.Unmarshal(data, &state); err != nil {
		return sandboxState{}, fmt.Errorf("parse sandbox state %q: %w", path, err)
	}
	return state, nil
}

func writeSandboxState(path string, state sandboxState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode sandbox state %q: %w", path, err)
	}
	data = append(data, '\n')
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, data, 0o644); err != nil {
		return fmt.Errorf("write sandbox state %q: %w", tempPath, err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace sandbox state %q: %w", path, err)
	}
	return nil
}

func buildSandboxRunArgs(cfg spec.Config) []string {
	args := []string{
		"run",
		"--rootfs", cfg.RootFS,
		"--runtime-mode", string(spec.NormalizeRuntimeMode(cfg.RuntimeMode)),
		"--scope-name", cfg.ScopeName,
	}
	if cfg.NetworkPolicyFile != "" {
		args = append(args, "--network-policy-file", cfg.NetworkPolicyFile)
	}
	if cfg.Preset != "" {
		args = append(args, "--preset", cfg.Preset)
	}
	if cfg.PresetFile != "" {
		args = append(args, "--preset-file", cfg.PresetFile)
	}
	for _, item := range cfg.ROBind {
		args = append(args, "--ro-bind", item)
	}
	for _, item := range cfg.RWBind {
		args = append(args, "--rw-bind", item)
	}
	for _, item := range cfg.Env {
		args = append(args, "--env", item)
	}
	if cfg.StdoutLog != "" {
		args = append(args, "--stdout-log", cfg.StdoutLog)
	}
	if cfg.StderrLog != "" {
		args = append(args, "--stderr-log", cfg.StderrLog)
	}
	if cfg.Cwd != "" {
		args = append(args, "--cwd", cfg.Cwd)
	}
	if cfg.Hostname != "" {
		args = append(args, "--hostname", cfg.Hostname)
	}
	if cfg.Memory != "" {
		args = append(args, "--memory", cfg.Memory)
	}
	if cfg.Pids > 0 {
		args = append(args, "--pids", fmt.Sprintf("%d", cfg.Pids))
	}
	args = append(args, "--")
	args = append(args, cfg.Command...)
	return args
}

func sandboxScopeUnit(name string) string {
	return "mirage-sandbox-" + strings.ToLower(name) + ".scope"
}

func defaultSandboxStateRootDir(override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		return filepath.Clean(override), nil
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); xdg != "" {
		return filepath.Join(xdg, "mirage", "sandboxes"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve sandbox state root: %w", err)
	}
	return filepath.Join(home, ".local", "state", "mirage", "sandboxes"), nil
}

func launchSandboxProcess(req sandboxLaunchRequest) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve mirage executable: %w", err)
	}
	logFile, err := os.OpenFile(req.LaunchLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open launch log %q: %w", req.LaunchLog, err)
	}
	defer logFile.Close()

	cmd := exec.Command(self, req.RunArgs...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start sandbox process: %w", err)
	}
	return cmd.Process.Release()
}

func showSandboxUserUnit(unit string) (sandboxUnitStatus, error) {
	cmd := exec.Command("systemctl", "--user", "show", unit, "--property=LoadState,ActiveState,SubState,Result")
	output, err := cmd.CombinedOutput()
	if err != nil {
		text := string(output)
		if strings.Contains(text, "not found") || strings.Contains(text, "No such file or directory") || strings.Contains(text, "not be found") {
			return sandboxUnitStatus{}, errSandboxUnitNotFound
		}
		return sandboxUnitStatus{}, fmt.Errorf("show sandbox unit %q: %w (%s)", unit, err, strings.TrimSpace(text))
	}

	status := sandboxUnitStatus{}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "LoadState":
			status.LoadState = value
		case "ActiveState":
			status.ActiveState = value
		case "SubState":
			status.SubState = value
		case "Result":
			status.Result = value
		}
	}
	if status.LoadState == "not-found" {
		return sandboxUnitStatus{}, errSandboxUnitNotFound
	}
	return status, nil
}

func stopSandboxUserUnit(unit string) error {
	cmd := exec.Command("systemctl", "--user", "stop", unit)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("stop sandbox unit %q: %w (%s)", unit, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func killSandboxUserUnit(unit string) error {
	cmd := exec.Command("systemctl", "--user", "kill", "--kill-whom=all", "--signal=KILL", unit)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kill sandbox unit %q: %w (%s)", unit, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func isActiveSandboxUnit(status sandboxUnitStatus) bool {
	switch status.ActiveState {
	case "active", "activating", "reloading", "deactivating":
		return true
	default:
		return false
	}
}

func printSandboxLog(w io.Writer, path string, lines int) error {
	content, err := tailFile(path, lines)
	if err != nil {
		return err
	}
	_, _ = io.WriteString(w, content)
	return nil
}

func printTitledSandboxLog(w io.Writer, title string, path string, lines int) error {
	_, _ = fmt.Fprintf(w, "== %s ==\n", title)
	return printSandboxLog(w, path, lines)
}

func tailFile(path string, lines int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Sprintf("log file %s does not exist\n", path), nil
		}
		return "", fmt.Errorf("read log file %q: %w", path, err)
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	parts := strings.Split(text, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	if len(parts) == 0 {
		return "", nil
	}
	return strings.Join(parts, "\n") + "\n", nil
}

func boolCount(values ...bool) int {
	var count int
	for _, value := range values {
		if value {
			count++
		}
	}
	return count
}

func firstCommand(command []string) string {
	if len(command) == 0 {
		return ""
	}
	return command[0]
}
