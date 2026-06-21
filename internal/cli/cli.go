package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/DemonGiggle/mirage/examples"
	"github.com/DemonGiggle/mirage/internal/release"
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
	case "package":
		return runPackage(args[1:], stdout, stderr)
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
	case "package":
		printPackageHelp(stdout)
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
  rootfs          bootstrap a Debian rootfs
  network-policy  list bundled example network policy files
  package         assemble a standalone release bundle
  version         print version

Help:
  mirage <command> --help
  mirage help rootfs
  mirage help network-policy
  mirage help package

Examples:
  mirage rootfs init --output /tmp/mirage/basic-rootfs
  mirage network-policy list
  mirage package --output ./dist/mirage-linux-x86_64.tar.gz --binary ./bin/mirage
  mirage doctor --rootfs /tmp/mirage/basic-rootfs --command /bin/ls
  mirage run --rootfs /tmp/mirage/basic-rootfs --network-policy-file ./examples/network-policies/offline.yaml -- app
  mirage run --preset-file ./examples/presets/openclaw-offline.yaml -- app
`)
}

func runRootfs(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		printRootfsHelp(stdout)
		return nil
	}
	if args[0] == "help" {
		return runRootfsHelp(args[1:], stdout)
	}
	switch args[0] {
	case "init":
		return runRootfsInit(args[1:], stdout, stderr)
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
	default:
		return fmt.Errorf("unknown rootfs help topic %q", args[0])
	}
}

func printRootfsHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `Manage Mirage root filesystems.

Usage:
  mirage rootfs <subcommand> [flags]

Subcommands:
  init            bootstrap a Debian rootfs

Help:
  mirage rootfs init --help

Examples:
  mirage rootfs init --output /tmp/mirage/basic-rootfs
`)
}

func runRootfsInit(args []string, stdout, stderr io.Writer) error {
	if containsHelpFlag(args) {
		printRootfsInitHelp(stdout)
		return nil
	}

	fs := flag.NewFlagSet("rootfs init", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var outputRoot string
	var allowOverwrite bool
	var architecture string
	var debianRelease string
	var extraPackages string

	fs.StringVar(&outputRoot, "output", "", "Path to the generated rootfs directory.")
	fs.BoolVar(&allowOverwrite, "allow-overwrite", false, "Allow writing into an existing non-empty output directory.")
	fs.StringVar(&architecture, "arch", "", "Target rootfs architecture. Supported: x86_64, arm64, arm32, riscv64. Defaults to the host architecture.")
	fs.StringVar(&debianRelease, "debian-release", "", "Debian codename to bootstrap. Defaults to the built-in release used by Mirage.")
	fs.StringVar(&extraPackages, "extra-pkg", "", "Comma-separated Debian package names to install in addition to the default rootfs package set.")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if outputRoot == "" {
		return errors.New("rootfs init requires --output")
	}
	if len(fs.Args()) > 0 {
		return fmt.Errorf("rootfs init does not accept positional arguments: %s", strings.Join(fs.Args(), " "))
	}

	_, _ = fmt.Fprintln(stdout, "mirage rootfs init")
	_, _ = fmt.Fprintf(stdout, "output: %s\n", outputRoot)
	report, err := rootfs.BootstrapWithReportWithOptions(outputRoot, rootfs.GenerateOptions{
		AllowOverwrite: allowOverwrite,
		LogOutput:      stdout,
		Architecture:   architecture,
		DebianRelease:  debianRelease,
		ExtraPackages:  splitCommaSeparatedList(extraPackages),
	})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "architecture: %s\n", report.Architecture)
	printGenerateWarnings(stdout, report, "")
	return nil
}

func printRootfsInitHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `Bootstrap a Debian minbase rootfs.

Usage:
  mirage rootfs init --output <path> [--allow-overwrite] [--arch <arch>] [--debian-release <codename>] [--extra-pkg <pkg1,pkg2>]

Notes:
  - The rootfs is created with mmdebstrap.
  - The default Debian release is `+rootfs.DefaultDebianRelease()+`.
  - Supported --arch values: x86_64, arm64, arm32, riscv64.
  - If --arch is omitted, Mirage detects the host architecture and uses that.
  - --debian-release overrides the Debian codename passed to mmdebstrap.
  - --extra-pkg appends Debian packages to the default bootstrap package set.
  - --allow-overwrite clears the existing output directory before rebuilding it.
  - Generated rootfs trees can be validated later with mirage doctor --rootfs ....

Examples:
  mirage rootfs init --output /tmp/mirage/basic-rootfs
  mirage rootfs init --output /tmp/mirage/bookworm-rootfs --debian-release bookworm
  mirage rootfs init --output /tmp/mirage/arm64-rootfs --arch arm64
  mirage rootfs init --output /tmp/mirage/dev-rootfs --extra-pkg vim,curl,jq
  mirage rootfs init --output /tmp/mirage/work --allow-overwrite
`)
}

func runPackage(args []string, stdout, stderr io.Writer) error {
	if containsHelpFlag(args) {
		printPackageHelp(stdout)
		return nil
	}

	fs := flag.NewFlagSet("package", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var outputPath string
	var binaryPath string
	var architecture string
	var allowOverwrite bool

	fs.StringVar(&outputPath, "output", "", "Package output path. Use a directory path for an unpacked bundle or a .tar.gz/.tgz path for an archive.")
	fs.StringVar(&binaryPath, "binary", "", "Path to the mirage executable to include. Defaults to the current executable unless --arch builds one from source.")
	fs.StringVar(&architecture, "arch", "", "Target package architecture. Supported: x86_64, arm64, arm32, riscv64. When set without --binary, Mirage builds a Linux binary for that architecture from ./cmd/mirage.")
	fs.BoolVar(&allowOverwrite, "allow-overwrite", false, "Allow replacing existing package-managed output paths.")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if outputPath == "" {
		return errors.New("package requires --output")
	}
	if len(fs.Args()) > 0 {
		return fmt.Errorf("package does not accept positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	report, err := release.CreatePackage(release.PackageOptions{
		OutputPath:     outputPath,
		BinaryPath:     binaryPath,
		Architecture:   architecture,
		AllowOverwrite: allowOverwrite,
	})
	if err != nil {
		return err
	}

	networkPolicies, err := examples.NetworkPolicyNames()
	if err != nil {
		return err
	}
	presets, err := examples.PresetNames()
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintln(stdout, "mirage package")
	_, _ = fmt.Fprintf(stdout, "format: %s\n", report.Format)
	_, _ = fmt.Fprintf(stdout, "output: %s\n", report.OutputPath)
	_, _ = fmt.Fprintf(stdout, "binary: %s\n", report.BinaryPath)
	if report.Architecture != "" {
		_, _ = fmt.Fprintf(stdout, "architecture: %s\n", report.Architecture)
	}
	_, _ = fmt.Fprintf(stdout, "package-root: %s\n", report.PackageRoot)
	_, _ = fmt.Fprintf(stdout, "network-policies: %d\n", len(networkPolicies))
	_, _ = fmt.Fprintf(stdout, "presets: %d\n", len(presets))
	return nil
}

func printPackageHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `Assemble a standalone Mirage release bundle.

Usage:
  mirage package --output <path> [--binary <path>] [--arch <arch>] [--allow-overwrite]

Notes:
  - If --output ends with .tar.gz or .tgz, Mirage writes a compressed release archive.
  - Otherwise Mirage writes an unpacked directory bundle.
  - Supported --arch values: x86_64, arm64, arm32, riscv64.
  - If --arch is set without --binary, Mirage cross-compiles a Linux mirage binary from ./cmd/mirage.
  - If --arch is set with --binary, Mirage verifies that the binary matches the requested architecture.
  - --allow-overwrite replaces existing package-managed files in a directory output or replaces an existing archive file.
  - The package includes bin/mirage plus share/mirage/network-policies and share/mirage/presets.

Examples:
  mirage package --output ./dist/mirage-linux-x86_64.tar.gz --binary ./bin/mirage
  mirage package --output ./dist/mirage-linux-arm64.tar.gz --arch arm64
  mirage package --output ./dist/mirage-release --binary ./bin/mirage --allow-overwrite
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
	_, _ = fmt.Fprintln(stdout, "- network policy inputs: --preset-file, --network-policy-file, and mirage network-policy list")
	_, _ = fmt.Fprintln(stdout, "- policy backend coverage: allow-all host passthrough, isolated ordered allow/deny rules for IP/CIDR selectors, routed uplink for egress allow semantics, explicit errors for deferred selectors")
	_, _ = fmt.Fprintln(stdout, "- cgroup v2 resource controls: available via delegated systemd scopes when systemd-run is present")
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
  mirage doctor --rootfs /tmp/mirage/basic-rootfs
  mirage doctor --rootfs /tmp/mirage/basic-rootfs --command /bin/ls
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
	fs.BoolVar(&cfg.RunAsRoot, "run-as-root", false, "Run the workload as root inside the sandbox.")
	fs.StringVar(&cfg.NetworkPolicyFile, "network-policy-file", "", "Path to a standalone networkPolicy YAML file. Use `mirage network-policy list` for bundled examples.")
	fs.StringVar(&cfg.ScopeName, "scope-name", "", "Internal: explicit systemd scope unit name.")
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
		"rootfs", "ro-bind", "rw-bind", "env", "run-as-root", "network-policy-file", "cwd", "hostname", "memory", "pids",
	}); err != nil {
		return err
	}
	cfg.Command = fs.Args()
	if err := loadConfigNetworkPolicy(&cfg); err != nil {
		return err
	}

	resolved, _, err := spec.ApplyPresetFile(cfg)
	if err != nil {
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
  - --ro-bind and --rw-bind accept host:guest absolute path pairs.
  - --pids controls the maximum number of processes/threads in the sandbox process tree.

Examples:
  mirage run --rootfs /tmp/mirage/basic-rootfs --network-policy-file ./examples/network-policies/offline.yaml -- /bin/sh
  mirage run --rootfs /tmp/mirage/basic-rootfs --network-policy-file ./examples/network-policies/offline.yaml --ro-bind /home/user/project:/workspace/project --rw-bind /tmp/mirage-cache:/workspace/cache -- /bin/sh
  mirage run --rootfs /tmp/mirage/basic-rootfs --run-as-root -- /bin/sh
  mirage run --rootfs /tmp/mirage/basic-rootfs --memory 512M --pids 64 -- /usr/bin/node app.js
  mirage run --preset-file ./examples/presets/openclaw-offline.yaml -- app
`)
}

func runNetworkPolicy(args []string, stdout, stderr io.Writer) error {
	_ = stderr
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		printNetworkPolicyHelp(stdout)
		return nil
	}
	if args[0] == "help" {
		return runNetworkPolicyHelp(args[1:], stdout)
	}
	switch args[0] {
	case "list":
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
	case "list":
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
  list        list bundled example network policy files and their intent

Examples:
  mirage network-policy list
`)
}

func runNetworkPolicyListFiles(args []string, stdout io.Writer) error {
	if containsHelpFlag(args) {
		printNetworkPolicyListFilesHelp(stdout)
		return nil
	}
	if len(args) > 0 {
		return fmt.Errorf("network-policy list does not accept positional arguments: %s", strings.Join(args, " "))
	}

	_, _ = fmt.Fprintln(stdout, "mirage network-policy list")
	for _, entry := range bundledNetworkPolicyFiles {
		_, _ = fmt.Fprintf(stdout, "- %s: %s\n", entry.Path, entry.Description)
	}
	return nil
}

func printNetworkPolicyListFilesHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `List bundled example network policy files.

Usage:
  mirage network-policy list

Examples:
  mirage network-policy list
`)
}

func printGenerateWarnings(w io.Writer, report rootfs.GenerateReport, prefix string) {
	if w == nil {
		return
	}
	totalWarnings := len(report.MissingAssets) + len(report.Warnings)
	if totalWarnings == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "warnings: %d\n", totalWarnings)
	for _, asset := range report.MissingAssets {
		_, _ = fmt.Fprintf(w, "warning: %s%s\n", prefix, asset.Message())
	}
	for _, warning := range report.Warnings {
		_, _ = fmt.Fprintf(w, "warning: %s%s\n", prefix, warning)
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

func splitCommaSeparatedList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return strings.Split(raw, ",")
}

func hostToolStatus(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return "missing"
	}
	return "available (" + path + ")"
}
