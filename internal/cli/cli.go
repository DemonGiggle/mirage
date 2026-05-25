package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/DemonGiggle/mirage/internal/rootfs"
	"github.com/DemonGiggle/mirage/internal/runner"
	"github.com/DemonGiggle/mirage/internal/spec"
)

const version = "0.1.0"

func Run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printRootHelp(stdout)
		return nil
	}
	switch args[0] {
	case "help", "--help", "-h":
		printRootHelp(stdout)
		return nil
	case "version":
		_, _ = fmt.Fprintf(stdout, "mirage %s\n", version)
		return nil
	case "__backend-exec":
		return runner.RunBackendHelper(args[1:], stdout, stderr)
	case "__cgroup-exec":
		return runner.RunCgroupHelper(args[1:], stdout, stderr)
	case "preset":
		return runPreset(args[1:], stdout)
	case "rootfs":
		return runRootfs(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "sandbox":
		return runSandboxCommand(args[1:], stdout, stderr)
	case "run":
		return runSandbox(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func printRootHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `mirage is a lightweight Linux sandbox launcher.

Usage:
  mirage rootfs init --template <name> --output <path>
  mirage doctor [flags]
  mirage sandbox <start|status|stop|logs> [flags]
  mirage run [flags] -- <command> [args...]
  mirage preset list
  mirage version

Examples:
  mirage rootfs init --template basic --output /srv/mirage/basic-rootfs
  mirage doctor --rootfs /srv/mirage/basic-rootfs --command /bin/ls
  mirage sandbox start --name openclaw --rootfs /srv/systemd-rootfs --service-unit openclaw.service
  mirage run --rootfs / --net none -- echo hello
  mirage run --rootfs /srv/systemd-rootfs --net host --runtime-mode init -- /usr/lib/systemd/systemd
  mirage run --rootfs /srv/rootfs --preset openai --warn net -- app
  mirage run --rootfs /srv/rootfs --preset-file ./presets.json --preset team-openai -- app
`)
}

func runPreset(args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] == "list" {
		fs := flag.NewFlagSet("preset list", flag.ContinueOnError)
		fs.SetOutput(io.Discard)

		var presetFile string
		fs.StringVar(&presetFile, "preset-file", "", "Path to a local preset JSON file")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}

		presets, err := spec.AvailablePresets(presetFile)
		if err != nil {
			return err
		}
		for _, name := range presetNames(presets) {
			preset := presets[name]
			_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\n", preset.Name, preset.NetworkMode, preset.Description)
		}
		return nil
	}
	return fmt.Errorf("unknown preset subcommand %q", args[0])
}

func runRootfs(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("rootfs requires a subcommand")
	}
	switch args[0] {
	case "init":
		return runRootfsInit(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown rootfs subcommand %q", args[0])
	}
}

func runRootfsInit(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("rootfs init", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var templateName string
	var outputRoot string

	fs.StringVar(&templateName, "template", "", "Built-in rootfs template name")
	fs.StringVar(&outputRoot, "output", "", "Path to the generated rootfs directory")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if templateName == "" {
		return errors.New("rootfs init requires --template")
	}
	if outputRoot == "" {
		return errors.New("rootfs init requires --output")
	}
	if len(fs.Args()) > 0 {
		return fmt.Errorf("rootfs init does not accept positional arguments: %s", strings.Join(fs.Args(), " "))
	}

	template, ok := rootfs.LookupTemplate(templateName)
	if !ok {
		return fmt.Errorf("unknown rootfs template %q", templateName)
	}
	if err := rootfs.Generate(outputRoot, template); err != nil {
		return err
	}

	_, _ = fmt.Fprintln(stdout, "mirage rootfs init")
	_, _ = fmt.Fprintf(stdout, "template: %s\n", template.Name)
	_, _ = fmt.Fprintf(stdout, "output: %s\n", outputRoot)
	_, _ = fmt.Fprintf(stdout, "directories: %d\n", len(template.Directories))
	_, _ = fmt.Fprintf(stdout, "binaries: %d\n", len(template.Binaries))
	_, _ = fmt.Fprintf(stdout, "runtime-files: %d\n", len(template.RuntimeFiles))
	_, _ = fmt.Fprintf(stdout, "generated-files: %d\n", len(template.GeneratedFiles))
	return nil
}

func runDoctor(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var rootfsPath string
	var command string
	var cwd string
	var preset string
	var presetFile string
	var runtimeMode string
	var serviceUnit string

	fs.StringVar(&rootfsPath, "rootfs", "", "Path to the rootfs to validate")
	fs.StringVar(&command, "command", "", "Command to resolve and validate inside the rootfs")
	fs.StringVar(&cwd, "cwd", "", "Working directory to validate inside the rootfs")
	fs.StringVar(&preset, "preset", "", "Named preset to resolve while validating")
	fs.StringVar(&presetFile, "preset-file", "", "Path to a local preset JSON file")
	fs.StringVar(&runtimeMode, "runtime-mode", string(spec.RuntimeModeDirect), "Runtime mode to validate against: direct, init")
	fs.StringVar(&serviceUnit, "service-unit", "", "Systemd unit to validate inside the rootfs when runtime-mode=init")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) > 0 {
		return fmt.Errorf("doctor does not accept positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	if runtimeMode != string(spec.RuntimeModeDirect) && runtimeMode != string(spec.RuntimeModeInit) {
		return fmt.Errorf("invalid runtime-mode %q; must be %q or %q", runtimeMode, spec.RuntimeModeDirect, spec.RuntimeModeInit)
	}

	_, _ = fmt.Fprintln(stdout, "mirage doctor")
	_, _ = fmt.Fprintln(stdout, "- namespace backend: available (linux, initial)")
	_, _ = fmt.Fprintln(stdout, "- rootfs isolation: available via mounted runtime layout plus chroot handoff")
	if err := runner.EnsureObservedNetworkToolAvailable(); err != nil {
		_, _ = fmt.Fprintf(stdout, "- observed isolated networking: unavailable (%v)\n", err)
	} else {
		_, _ = fmt.Fprintln(stdout, "- observed isolated networking: available (strace found on PATH)")
	}
	_, _ = fmt.Fprintln(stdout, "- cgroup v2 resource controls: available via delegated systemd user scopes when systemd-run is present")
	_, _ = fmt.Fprintln(stdout, "- network presets: available")
	_, _ = fmt.Fprintln(stdout, "- warn mode recorder: available for network connect attempts")
	_, _ = fmt.Fprintln(stdout, "- host log export: available")

	if rootfsPath == "" && command == "" && cwd == "" && preset == "" && presetFile == "" && serviceUnit == "" {
		return nil
	}
	if rootfsPath == "" {
		return errors.New("rootfs-aware doctor checks require --rootfs")
	}

	if preset != "" {
		presets, err := spec.AvailablePresets(presetFile)
		if err != nil {
			return err
		}
		resolvedPreset, ok := presets[preset]
		if !ok {
			return fmt.Errorf("unknown preset %q", preset)
		}
		_, _ = fmt.Fprintf(stdout, "- preset: %s (%s)\n", resolvedPreset.Name, resolvedPreset.NetworkMode)
		if resolvedPreset.Rootfs.RecommendedTemplate != "" {
			_, _ = fmt.Fprintf(stdout, "- preset recommended rootfs template: %s\n", resolvedPreset.Rootfs.RecommendedTemplate)
		}
		if resolvedPreset.Rootfs.RecommendedCwd != "" {
			_, _ = fmt.Fprintf(stdout, "- preset recommended working directory: %s\n", resolvedPreset.Rootfs.RecommendedCwd)
		}
		presetRequiredCommands := uniqueStrings(resolvedPreset.Rootfs.RequiredCommands)
		if len(presetRequiredCommands) > 0 {
			_, _ = fmt.Fprintf(stdout, "- preset required rootfs commands: %s\n", strings.Join(presetRequiredCommands, ", "))
		}
		if runtimeMode == string(spec.RuntimeModeInit) {
			return runInitDoctor(stdout, rootfsPath, command, serviceUnit, presetRequiredCommands)
		}
		report, err := rootfs.ValidateRootfs(rootfsPath, "", cwd)
		_, _ = fmt.Fprintf(stdout, "- rootfs path: %s\n", report.Rootfs)
		for _, status := range report.RuntimePaths {
			_, _ = fmt.Fprintf(stdout, "- runtime path %s: %s\n", status.Path, status.Status)
		}
		if report.WorkingDir != "" {
			_, _ = fmt.Fprintf(stdout, "- working directory: %s\n", report.WorkingDir)
		}
		var problems []error
		if err != nil {
			problems = append(problems, err)
		}

		commandsToValidate := uniqueStrings(append([]string{command}, presetRequiredCommands...))
		for _, commandToValidate := range commandsToValidate {
			if commandToValidate == "" {
				continue
			}
			commandReport, err := rootfs.ValidateRootfs(rootfsPath, commandToValidate, "")
			if err != nil {
				problems = append(problems, err)
				continue
			}
			if commandToValidate == command {
				_, _ = fmt.Fprintf(stdout, "- resolved command: %s\n", commandReport.ResolvedCommand)
				if commandReport.Interpreter != "" {
					_, _ = fmt.Fprintf(stdout, "- ELF interpreter: %s\n", commandReport.Interpreter)
				}
				if commandReport.DependencyCount > 0 {
					_, _ = fmt.Fprintf(stdout, "- shared libraries: ok (%d resolved)\n", commandReport.DependencyCount)
				}
				continue
			}
			_, _ = fmt.Fprintf(stdout, "- preset required command %s: ok (%s)\n", commandToValidate, commandReport.ResolvedCommand)
		}
		if len(problems) > 0 {
			_, _ = fmt.Fprintln(stdout, "- rootfs validation: failed")
			return errors.Join(problems...)
		}
		_, _ = fmt.Fprintln(stdout, "- rootfs validation: ok")
		return nil
	}
	if runtimeMode == string(spec.RuntimeModeInit) {
		return runInitDoctor(stdout, rootfsPath, command, serviceUnit, nil)
	}

	report, err := rootfs.ValidateRootfs(rootfsPath, command, cwd)
	_, _ = fmt.Fprintf(stdout, "- rootfs path: %s\n", report.Rootfs)
	for _, status := range report.RuntimePaths {
		_, _ = fmt.Fprintf(stdout, "- runtime path %s: %s\n", status.Path, status.Status)
	}
	if report.WorkingDir != "" {
		_, _ = fmt.Fprintf(stdout, "- working directory: %s\n", report.WorkingDir)
	}
	if report.ResolvedCommand != "" {
		_, _ = fmt.Fprintf(stdout, "- resolved command: %s\n", report.ResolvedCommand)
	}
	if report.Interpreter != "" {
		_, _ = fmt.Fprintf(stdout, "- ELF interpreter: %s\n", report.Interpreter)
	}
	if report.DependencyCount > 0 {
		_, _ = fmt.Fprintf(stdout, "- shared libraries: ok (%d resolved)\n", report.DependencyCount)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stdout, "- rootfs validation: failed")
		return err
	}
	_, _ = fmt.Fprintln(stdout, "- rootfs validation: ok")
	return nil
}

func runInitDoctor(stdout io.Writer, rootfsPath string, command string, serviceUnit string, presetRequiredCommands []string) error {
	report, err := rootfs.ValidateInitRootfs(rootfsPath, command, serviceUnit)
	_, _ = fmt.Fprintf(stdout, "- rootfs path: %s\n", report.Rootfs)
	for _, status := range report.RuntimePaths {
		_, _ = fmt.Fprintf(stdout, "- init runtime path %s: %s\n", status.Path, status.Status)
	}
	if report.ResolvedInit != "" {
		_, _ = fmt.Fprintf(stdout, "- resolved init command: %s\n", report.ResolvedInit)
	}
	if report.MachineIDPath != "" {
		_, _ = fmt.Fprintf(stdout, "- systemd machine-id: %s\n", report.MachineIDPath)
	}
	if serviceUnit != "" && report.ServiceUnitPath != "" {
		_, _ = fmt.Fprintf(stdout, "- systemd unit %s: ok (%s)\n", serviceUnit, report.ServiceUnitPath)
	}

	var problems []error
	if err != nil {
		problems = append(problems, err)
	}
	for _, commandToValidate := range presetRequiredCommands {
		commandReport, err := rootfs.ValidateRootfs(rootfsPath, commandToValidate, "")
		if err != nil {
			problems = append(problems, err)
			continue
		}
		_, _ = fmt.Fprintf(stdout, "- preset required command %s: ok (%s)\n", commandToValidate, commandReport.ResolvedCommand)
	}
	if len(problems) > 0 {
		_, _ = fmt.Fprintln(stdout, "- init rootfs validation: failed")
		return errors.Join(problems...)
	}
	_, _ = fmt.Fprintln(stdout, "- init rootfs validation: ok")
	return nil
}

func uniqueStrings(items []string) []string {
	var out []string
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if slicesContainsString(out, item) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func slicesContainsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func runSandbox(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var cfg spec.Config
	var warnCSV string

	fs.StringVar(&cfg.RootFS, "rootfs", "", "Path to the sandbox root filesystem")
	fs.Var(stringSliceValue{target: &cfg.ROBind}, "ro-bind", "Read-only bind mount host:guest")
	fs.Var(stringSliceValue{target: &cfg.RWBind}, "rw-bind", "Writable bind mount host:guest")
	fs.Var(stringSliceValue{target: &cfg.AllowHosts}, "allow-host", "Allow egress to host:port")
	fs.Var(stringSliceValue{target: &cfg.AllowCIDRs}, "allow-cidr", "Allow egress to CIDR")
	fs.Var(stringSliceValue{target: &cfg.AllowPorts}, "allow-port", "Allow egress to port or proto/port")
	fs.Var(stringSliceValue{target: &cfg.Env}, "env", "Environment variable in KEY=VALUE form")
	fs.StringVar((*string)(&cfg.NetworkMode), "net", "", "Network mode: none, isolated, host")
	fs.StringVar((*string)(&cfg.RuntimeMode), "runtime-mode", string(spec.RuntimeModeDirect), "Runtime mode: direct, init")
	fs.StringVar(&cfg.ScopeName, "scope-name", "", "Internal: explicit systemd user scope unit name")
	fs.StringVar(&cfg.Preset, "preset", "", "Named preset to apply before inline overrides")
	fs.StringVar(&cfg.PresetFile, "preset-file", "", "Path to a local preset JSON file")
	fs.StringVar(&warnCSV, "warn", "", "Warn modes, currently supports: net")
	fs.StringVar(&cfg.StdoutLog, "stdout-log", "", "Write workload stdout to a host-side log file")
	fs.StringVar(&cfg.StderrLog, "stderr-log", "", "Write workload stderr to a host-side log file")
	fs.StringVar(&cfg.Cwd, "cwd", "", "Working directory inside the sandbox")
	fs.StringVar(&cfg.Hostname, "hostname", "", "Hostname inside the sandbox")
	fs.StringVar(&cfg.Memory, "memory", "", "Memory limit, for example 512M")
	fs.IntVar(&cfg.Pids, "pids", 0, "PID limit")
	fs.BoolVar(&cfg.DryRun, "dry-run", false, "Print the planned sandbox config without running anything")

	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg.Command = fs.Args()
	cfg.Warn = splitCSV(warnCSV)

	resolved, err := spec.ApplyPreset(cfg)
	if err != nil {
		return err
	}
	if err := ensurePresetRootfs(resolved); err != nil {
		return err
	}
	if err := spec.Validate(resolved); err != nil {
		return err
	}

	_, _ = fmt.Fprintln(stdout, "mirage run preview")
	_, _ = fmt.Fprint(stdout, spec.Summary(resolved))
	for _, note := range runner.PlanNotes(resolved) {
		_, _ = fmt.Fprintf(stdout, "note: %s\n", note)
	}
	if resolved.DryRun {
		_, _ = fmt.Fprintln(stdout, "execution: skipped (--dry-run)")
		return nil
	}
	if resolved.RootFS == "" {
		return errors.New("execution backend requires rootfs")
	}
	return runner.Execute(resolved, stdout, stderr)
}

func ensurePresetRootfs(cfg spec.Config) error {
	if cfg.RootFS == "" || cfg.Preset == "" {
		return nil
	}

	presets, err := spec.AvailablePresets(cfg.PresetFile)
	if err != nil {
		return err
	}
	preset, ok := presets[cfg.Preset]
	if !ok {
		return fmt.Errorf("unknown preset %q", cfg.Preset)
	}
	templateName := preset.Rootfs.RecommendedTemplate
	if templateName == "" {
		return nil
	}

	info, err := os.Stat(cfg.RootFS)
	switch {
	case err == nil:
		if !info.IsDir() {
			return nil
		}
		entries, err := os.ReadDir(cfg.RootFS)
		if err != nil {
			return fmt.Errorf("read rootfs %q: %w", cfg.RootFS, err)
		}
		if len(entries) > 0 {
			return nil
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return fmt.Errorf("stat rootfs %q: %w", cfg.RootFS, err)
	}

	template, ok := rootfs.LookupTemplate(templateName)
	if !ok {
		return fmt.Errorf("unknown rootfs template %q", templateName)
	}
	if err := rootfs.Generate(cfg.RootFS, template); err != nil {
		return fmt.Errorf("prepare rootfs %q from preset %q template %q: %w", cfg.RootFS, cfg.Preset, templateName, err)
	}
	return nil
}

func splitCSV(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func presetNames(presets map[string]spec.Preset) []string {
	names := make([]string, 0, len(presets))
	for name := range presets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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
