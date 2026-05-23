package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

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
	case "preset":
		return runPreset(args[1:], stdout)
	case "doctor":
		return runDoctor(stdout)
	case "run":
		return runSandbox(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func printRootHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `mirage is a lightweight Linux sandbox launcher.

Usage:
  mirage run [flags] -- <command> [args...]
  mirage doctor
  mirage preset list
  mirage version

Examples:
  mirage run --rootfs /srv/rootfs --net none -- echo hello
  mirage run --rootfs /srv/rootfs --preset openai --warn net -- app
`)
}

func runPreset(args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] == "list" {
		for _, name := range spec.PresetNames() {
			preset := spec.KnownPresets[name]
			_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\n", preset.Name, preset.NetworkMode, preset.Description)
		}
		return nil
	}
	return fmt.Errorf("unknown preset subcommand %q", args[0])
}

func runDoctor(stdout io.Writer) error {
	_, _ = fmt.Fprintln(stdout, "mirage doctor")
	_, _ = fmt.Fprintln(stdout, "- namespace backend: planned")
	_, _ = fmt.Fprintln(stdout, "- rootfs isolation: planned")
	_, _ = fmt.Fprintln(stdout, "- network presets: planned")
	_, _ = fmt.Fprintln(stdout, "- warn mode recorder: planned")
	return nil
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
	fs.StringVar(&cfg.Preset, "preset", "", "Named preset to apply before inline overrides")
	fs.StringVar(&warnCSV, "warn", "", "Warn modes, currently supports: net")
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
	if err := spec.Validate(resolved); err != nil {
		return err
	}

	_, _ = fmt.Fprintln(stdout, "mirage run preview")
	_, _ = fmt.Fprint(stdout, spec.Summary(resolved))
	if resolved.DryRun {
		_, _ = fmt.Fprintln(stdout, "execution: skipped (--dry-run)")
		return nil
	}
	return errors.New("execution backend not implemented yet")
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
