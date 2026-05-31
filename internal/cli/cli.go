package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/DemonGiggle/mirage/internal/rootfs"
	"github.com/DemonGiggle/mirage/internal/runner"
	"github.com/DemonGiggle/mirage/internal/spec"
)

const version = "0.1.0"

type bundledNetworkPolicyFile struct {
	Path        string
	Description string
}

var bundledNetworkPolicyFiles = []bundledNetworkPolicyFile{
	{
		Path:        "examples/network-policies/allow-all.yaml",
		Description: "Allow all ingress and egress; uses host network namespace passthrough.",
	},
	{
		Path:        "examples/network-policies/offline.yaml",
		Description: "Deny ingress and egress except loopback; uses the isolated namespace backend.",
	},
	{
		Path:        "examples/network-policies/block-local-egress.yaml",
		Description: "Allow internet egress while denying common local/private ranges; uses the routed namespace backend.",
	},
}

func Run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printRootHelp(stdout)
		return nil
	}
	switch args[0] {
	case "help":
		return runHelpTopic(args[1:], stdout)
	case "--help", "-h":
		printRootHelp(stdout)
		return nil
	case "version":
		_, _ = fmt.Fprintf(stdout, "mirage %s\n", version)
		return nil
	case "__backend-exec":
		return runner.RunBackendHelper(args[1:], stdout, stderr)
	case "__cgroup-exec":
		return runner.RunCgroupHelper(args[1:], stdout, stderr)
	case "rootfs":
		return runRootfs(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "run":
		return runSandbox(args[1:], stdout, stderr)
	case "network-policy":
		return runNetworkPolicy(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runHelpTopic(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		printRootHelp(stdout)
		return nil
	}
	switch args[0] {
	case "rootfs":
		return runRootfsHelp(args[1:], stdout)
	case "doctor":
		printDoctorHelp(stdout)
		return nil
	case "run":
		printRunHelp(stdout)
		return nil
	case "network-policy":
		return runNetworkPolicyHelp(args[1:], stdout)
	default:
		return fmt.Errorf("unknown help topic %q", args[0])
	}
}

func printRootHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `mirage is a lightweight Linux sandbox launcher.

Usage:
  mirage <command> [flags]

Commands:
  run             launch a sandboxed workload
  doctor          inspect host capabilities and optionally validate a rootfs
  rootfs          generate or inspect built-in rootfs templates
  network-policy  list bundled example network policy files
  version         print version

Help:
  mirage <command> --help
  mirage help rootfs
  mirage help network-policy

Examples:
  mirage rootfs list-template
  mirage rootfs init --template basic --output /srv/mirage/basic-rootfs
  mirage network-policy list-files
  mirage doctor --rootfs /srv/mirage/basic-rootfs --command /bin/ls
  mirage run --rootfs /srv/rootfs --network-policy-file ./examples/network-policies/offline.yaml -- app
  mirage run --preset-file ./examples/presets/openclaw-offline.yaml -- app
`)
}

func runRootfs(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		printRootfsHelp(stdout)
		return nil
	}
	if args[0] == "help" {
		return runRootfsHelp(args[1:], stdout)
	}
	switch args[0] {
	case "init":
		return runRootfsInit(args[1:], stdout, stderr)
	case "list-template", "list-templates":
		return runRootfsListTemplates(args[1:], stdout)
	default:
		return fmt.Errorf("unknown rootfs subcommand %q", args[0])
	}
}

func runRootfsHelp(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		printRootfsHelp(stdout)
		return nil
	}
	switch args[0] {
	case "init":
		printRootfsInitHelp(stdout)
		return nil
	case "list-template", "list-templates":
		printRootfsListTemplatesHelp(stdout)
		return nil
	default:
		return fmt.Errorf("unknown rootfs help topic %q", args[0])
	}
}

func printRootfsHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `Manage Mirage root filesystems.

Usage:
  mirage rootfs <subcommand> [flags]

Subcommands:
  init            generate a rootfs from a built-in template
  list-template   list built-in template names and descriptions

Help:
  mirage rootfs init --help
  mirage rootfs list-template --help

Examples:
  mirage rootfs list-template
  mirage rootfs init --template basic --output /srv/mirage/basic-rootfs
`)
}

func runRootfsInit(args []string, stdout, stderr io.Writer) error {
	if containsHelpFlag(args) {
		printRootfsInitHelp(stdout)
		return nil
	}

	fs := flag.NewFlagSet("rootfs init", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var templateName string
	var outputRoot string
	var allowOverwrite bool

	fs.StringVar(&templateName, "template", "", "Built-in rootfs template name. Use `mirage rootfs list-template` to inspect choices.")
	fs.StringVar(&outputRoot, "output", "", "Path to the generated rootfs directory.")
	fs.BoolVar(&allowOverwrite, "allow-overwrite", false, "Allow writing into an existing non-empty output directory.")

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
	report, err := rootfs.GenerateWithReportWithOptions(outputRoot, template, rootfs.GenerateOptions{
		AllowOverwrite: allowOverwrite,
	})
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintln(stdout, "mirage rootfs init")
	_, _ = fmt.Fprintf(stdout, "template: %s\n", template.Name)
	_, _ = fmt.Fprintf(stdout, "description: %s\n", template.Description)
	_, _ = fmt.Fprintf(stdout, "output: %s\n", outputRoot)
	_, _ = fmt.Fprintf(stdout, "directories: %d\n", len(template.Directories))
	_, _ = fmt.Fprintf(stdout, "binaries: %d\n", len(template.Binaries))
	_, _ = fmt.Fprintf(stdout, "runtime-files: %d\n", len(template.RuntimeFiles))
	_, _ = fmt.Fprintf(stdout, "generated-files: %d\n", len(template.GeneratedFiles))
	printGenerateWarnings(stdout, report, "")
	return nil
}

func printRootfsInitHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `Generate a rootfs from a built-in template.

Usage:
  mirage rootfs init --template <name> --output <path> [--allow-overwrite]

Notes:
  - Use mirage rootfs list-template to inspect available templates first.
  - Generated rootfs trees can be validated later with mirage doctor --rootfs ....

Examples:
  mirage rootfs init --template basic --output /srv/mirage/basic-rootfs
  mirage rootfs init --template openclaw-work --output /srv/mirage/work --allow-overwrite
`)
}

func runRootfsListTemplates(args []string, stdout io.Writer) error {
	if containsHelpFlag(args) {
		printRootfsListTemplatesHelp(stdout)
		return nil
	}
	if len(args) > 0 {
		return fmt.Errorf("rootfs list-template does not accept positional arguments: %s", strings.Join(args, " "))
	}

	_, _ = fmt.Fprintln(stdout, "mirage rootfs list-template")
	for _, name := range rootfs.TemplateNames() {
		template, ok := rootfs.LookupTemplate(name)
		if !ok {
			continue
		}
		_, _ = fmt.Fprintf(stdout, "- %s: %s\n", template.Name, template.Description)
	}
	return nil
}

func printRootfsListTemplatesHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `List built-in rootfs template names and descriptions.

Usage:
  mirage rootfs list-template

Examples:
  mirage rootfs list-template
`)
}

func runDoctor(args []string, stdout, stderr io.Writer) error {
	if err := rejectRemovedPresetFlag(args); err != nil {
		return err
	}
	if containsHelpFlag(args) {
		printDoctorHelp(stdout)
		return nil
	}

	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var rootfsPath string
	var command string
	var cwd string
	var presetFile string

	fs.StringVar(&rootfsPath, "rootfs", "", "Path to the rootfs to validate.")
	fs.StringVar(&command, "command", "", "Command to resolve and validate inside the rootfs.")
	fs.StringVar(&cwd, "cwd", "", "Working directory to validate inside the rootfs.")
	fs.StringVar(&presetFile, "preset-file", "", "Path to a preset YAML file whose rootfs settings should also be validated.")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) > 0 {
		return fmt.Errorf("doctor does not accept positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	setFlags := collectSetFlags(fs)
	if err := rejectPresetFileConflicts("doctor", presetFile, setFlags, []string{"rootfs", "cwd"}); err != nil {
		return err
	}

	_, _ = fmt.Fprintln(stdout, "mirage doctor")
	_, _ = fmt.Fprintln(stdout, "scope: host environment and optional rootfs validation")
	_, _ = fmt.Fprintln(stdout, "host analysis:")
	_, _ = fmt.Fprintln(stdout, "- namespace backend: available (linux, initial)")
	_, _ = fmt.Fprintf(stdout, "- systemd-run: %s\n", hostToolStatus("systemd-run"))
	_, _ = fmt.Fprintln(stdout, "- rootfs isolation: available via mounted runtime layout plus chroot handoff")
	_, _ = fmt.Fprintln(stdout, "- network policy inputs: --preset-file, --network-policy-file, and mirage network-policy list-files")
	_, _ = fmt.Fprintln(stdout, "- policy backend coverage: allow-all host passthrough, isolated ordered allow/deny rules for IP/CIDR selectors, routed uplink for egress allow semantics, explicit errors for deferred selectors")
	_, _ = fmt.Fprintln(stdout, "- cgroup v2 resource controls: available via delegated systemd user scopes when systemd-run is present")
	_, _ = fmt.Fprintln(stdout, "- preset-file loading: available")
	_, _ = fmt.Fprintln(stdout, "- host log export: available")

	cfg := spec.Config{
		RootFS:     rootfsPath,
		Cwd:        cwd,
		PresetFile: presetFile,
	}
	resolved, preset, err := spec.ApplyPresetFile(cfg)
	if err != nil {
		return err
	}
	rootfsPath = resolved.RootFS
	cwd = resolved.Cwd

	if rootfsPath == "" && command == "" && cwd == "" && presetFile == "" {
		return nil
	}
	if rootfsPath == "" {
		return errors.New("rootfs-aware doctor checks require --rootfs")
	}

	presetRequiredCommands := uniqueStrings(preset.Rootfs.RequiredCommands)
	_, _ = fmt.Fprintln(stdout, "rootfs analysis:")
	if presetFile != "" {
		_, _ = fmt.Fprintf(stdout, "- preset-file: %s\n", presetFile)
		if preset.Rootfs.Template != "" {
			_, _ = fmt.Fprintf(stdout, "- preset rootfs template: %s\n", preset.Rootfs.Template)
		}
		if len(presetRequiredCommands) > 0 {
			_, _ = fmt.Fprintf(stdout, "- preset required rootfs commands: %s\n", strings.Join(presetRequiredCommands, ", "))
		}
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

func printDoctorHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `Inspect Mirage host capabilities and optionally validate a rootfs.

Usage:
  mirage doctor [flags]

What it reports:
  - host-side capability summary for namespaces, cgroups, preset loading, and log export
  - optional rootfs path validation
  - optional command resolution and shared library checks inside the rootfs

Examples:
  mirage doctor
  mirage doctor --rootfs /srv/mirage/basic-rootfs
  mirage doctor --rootfs /srv/mirage/basic-rootfs --command /bin/ls
  mirage doctor --preset-file ./examples/presets/openclaw-offline.yaml
`)
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
	if err := rejectRemovedPresetFlag(args); err != nil {
		return err
	}
	if containsHelpFlag(args) {
		printRunHelp(stdout)
		return nil
	}

	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var cfg spec.Config

	fs.StringVar(&cfg.RootFS, "rootfs", "", "Path to the sandbox root filesystem mounted as / inside the guest.")
	fs.Var(stringSliceValue{target: &cfg.ROBind}, "ro-bind", "Read-only bind mount in host:guest form.")
	fs.Var(stringSliceValue{target: &cfg.RWBind}, "rw-bind", "Writable bind mount in host:guest form.")
	fs.Var(stringSliceValue{target: &cfg.Env}, "env", "Environment variable in KEY=VALUE form.")
	fs.StringVar(&cfg.NetworkPolicyFile, "network-policy-file", "", "Path to a standalone networkPolicy YAML file. Use `mirage network-policy list-files` for bundled examples.")
	fs.StringVar(&cfg.ScopeName, "scope-name", "", "Internal: explicit systemd user scope unit name.")
	fs.StringVar(&cfg.PresetFile, "preset-file", "", "Path to a preset YAML file.")
	fs.StringVar(&cfg.StdoutLog, "stdout-log", "", "Write workload stdout to a host-side log file.")
	fs.StringVar(&cfg.StderrLog, "stderr-log", "", "Write workload stderr to a host-side log file.")
	fs.StringVar(&cfg.Cwd, "cwd", "", "Working directory inside the sandbox.")
	fs.StringVar(&cfg.Hostname, "hostname", "", "Hostname inside the sandbox.")
	fs.StringVar(&cfg.Memory, "memory", "", "Memory limit for the sandbox cgroup, for example 512M.")
	fs.IntVar(&cfg.Pids, "pids", 0, "Maximum process/thread count for the sandbox process tree. Use 0 to leave it unlimited.")
	fs.BoolVar(&cfg.DryRun, "dry-run", false, "Print the planned sandbox config without running anything.")

	if err := fs.Parse(args); err != nil {
		return err
	}
	setFlags := collectSetFlags(fs)
	if err := rejectPresetFileConflicts("run", cfg.PresetFile, setFlags, []string{
		"rootfs", "ro-bind", "rw-bind", "env", "network-policy-file", "cwd", "hostname", "memory", "pids",
	}); err != nil {
		return err
	}
	cfg.Command = fs.Args()
	if err := loadConfigNetworkPolicy(&cfg); err != nil {
		return err
	}

	resolved, preset, err := spec.ApplyPresetFile(cfg)
	if err != nil {
		return err
	}
	if err := ensurePresetRootfs(resolved, preset, stderr); err != nil {
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

func printRunHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `Launch a sandboxed workload.

Usage:
  mirage run [flags] -- <command> [args...]

Notes:
  - Use -- to separate Mirage flags from the workload command.
  - --preset-file is exclusive with direct config flags such as --rootfs, --network-policy-file, --memory, and --pids.
  - --pids controls the maximum number of processes/threads in the sandbox process tree.

Examples:
  mirage run --rootfs /srv/rootfs --network-policy-file ./examples/network-policies/offline.yaml -- /bin/sh
  mirage run --rootfs /srv/rootfs --memory 512M --pids 64 -- /usr/bin/node app.js
  mirage run --preset-file ./examples/presets/openclaw-offline.yaml -- app
`)
}

func runNetworkPolicy(args []string, stdout, stderr io.Writer) error {
	_ = stderr
	if len(args) == 0 || isHelpToken(args[0]) {
		printNetworkPolicyHelp(stdout)
		return nil
	}
	if args[0] == "help" {
		return runNetworkPolicyHelp(args[1:], stdout)
	}
	switch args[0] {
	case "list-files":
		return runNetworkPolicyListFiles(args[1:], stdout)
	default:
		return fmt.Errorf("unknown network-policy subcommand %q", args[0])
	}
}

func runNetworkPolicyHelp(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		printNetworkPolicyHelp(stdout)
		return nil
	}
	switch args[0] {
	case "list-files":
		printNetworkPolicyListFilesHelp(stdout)
		return nil
	default:
		return fmt.Errorf("unknown network-policy help topic %q", args[0])
	}
}

func printNetworkPolicyHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `Inspect bundled network policy examples.

Usage:
  mirage network-policy <subcommand>

Subcommands:
  list-files   list bundled example network policy files and their intent

Examples:
  mirage network-policy list-files
`)
}

func runNetworkPolicyListFiles(args []string, stdout io.Writer) error {
	if containsHelpFlag(args) {
		printNetworkPolicyListFilesHelp(stdout)
		return nil
	}
	if len(args) > 0 {
		return fmt.Errorf("network-policy list-files does not accept positional arguments: %s", strings.Join(args, " "))
	}

	_, _ = fmt.Fprintln(stdout, "mirage network-policy list-files")
	for _, entry := range bundledNetworkPolicyFiles {
		_, _ = fmt.Fprintf(stdout, "- %s: %s\n", entry.Path, entry.Description)
	}
	return nil
}

func printNetworkPolicyListFilesHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `List bundled example network policy files.

Usage:
  mirage network-policy list-files

Examples:
  mirage network-policy list-files
`)
}

func ensurePresetRootfs(cfg spec.Config, preset spec.Preset, stderr io.Writer) error {
	if cfg.RootFS == "" {
		return nil
	}
	templateName := preset.Rootfs.Template
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
			report, err := rootfs.EnsureNSSRuntimeWithReport(cfg.RootFS)
			if err != nil {
				return err
			}
			printGenerateWarnings(stderr, report, "preset rootfs ")
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
	report, err := rootfs.GenerateWithReport(cfg.RootFS, template)
	if err != nil {
		return fmt.Errorf("prepare rootfs %q from preset file %q template %q: %w", cfg.RootFS, cfg.PresetFile, templateName, err)
	}
	printGenerateWarnings(stderr, report, "preset rootfs ")
	return nil
}

func printGenerateWarnings(w io.Writer, report rootfs.GenerateReport, prefix string) {
	if w == nil || len(report.MissingAssets) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "warnings: %d\n", len(report.MissingAssets))
	for _, asset := range report.MissingAssets {
		_, _ = fmt.Fprintf(w, "warning: %s%s\n", prefix, asset.Message())
	}
}

func loadConfigNetworkPolicy(cfg *spec.Config) error {
	if cfg == nil || cfg.NetworkPolicyFile == "" {
		return nil
	}
	policy, err := spec.LoadNetworkPolicyFile(cfg.NetworkPolicyFile)
	if err != nil {
		return err
	}
	cfg.NetworkPolicy = &policy
	return nil
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

func collectSetFlags(fs *flag.FlagSet) map[string]bool {
	setFlags := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) {
		setFlags[f.Name] = true
	})
	return setFlags
}

func rejectRemovedPresetFlag(args []string) error {
	for _, arg := range args {
		if arg == "--preset" || strings.HasPrefix(arg, "--preset=") {
			return errors.New("--preset has been removed; use --preset-file with a single preset YAML file instead")
		}
	}
	return nil
}

func rejectPresetFileConflicts(commandName string, presetFile string, setFlags map[string]bool, directFlags []string) error {
	if strings.TrimSpace(presetFile) == "" {
		return nil
	}
	for _, name := range directFlags {
		if setFlags[name] {
			return fmt.Errorf("%s does not allow --%s together with --preset-file", commandName, name)
		}
	}
	return nil
}

func isHelpToken(arg string) bool {
	return arg == "help" || arg == "--help" || arg == "-h"
}

func containsHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if isHelpToken(arg) {
			return true
		}
	}
	return false
}

func hostToolStatus(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return "missing"
	}
	return "available (" + path + ")"
}
