package spec

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

type NetworkMode string

const (
	NetworkNone     NetworkMode = "none"
	NetworkIsolated NetworkMode = "isolated"
	NetworkHost     NetworkMode = "host"
)

var KnownPresets = map[string]Preset{
	"offline": {
		Name:        "offline",
		NetworkMode: NetworkNone,
		Description: "No network access.",
	},
	"github": {
		Name:        "github",
		NetworkMode: NetworkIsolated,
		AllowHosts:  []string{"github.com:443"},
		Description: "Allow GitHub over HTTPS.",
	},
	"openai": {
		Name:        "openai",
		NetworkMode: NetworkIsolated,
		AllowHosts:  []string{"api.openai.com:443", "chatgpt.com:443"},
		Description: "Allow the minimum expected OpenAI endpoints over HTTPS.",
	},
}

type Preset struct {
	Name        string
	NetworkMode NetworkMode
	AllowHosts  []string
	Description string
}

type Config struct {
	RootFS      string
	NetworkMode NetworkMode
	Preset      string
	Warn        []string
	ROBind      []string
	RWBind      []string
	AllowHosts  []string
	AllowCIDRs  []string
	AllowPorts  []string
	Env         []string
	Cwd         string
	Hostname    string
	Memory      string
	Pids        int
	DryRun      bool
	Command     []string
}

func PresetNames() []string {
	names := make([]string, 0, len(KnownPresets))
	for name := range KnownPresets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func ApplyPreset(cfg Config) (Config, error) {
	if cfg.Preset == "" {
		return cfg, nil
	}
	preset, ok := KnownPresets[cfg.Preset]
	if !ok {
		return cfg, fmt.Errorf("unknown preset %q", cfg.Preset)
	}
	if cfg.NetworkMode == "" {
		cfg.NetworkMode = preset.NetworkMode
	}
	if len(cfg.AllowHosts) == 0 && len(preset.AllowHosts) > 0 {
		cfg.AllowHosts = append([]string{}, preset.AllowHosts...)
	}
	return cfg, nil
}

func Validate(cfg Config) error {
	var problems []error
	if cfg.RootFS == "" {
		problems = append(problems, errors.New("rootfs is required"))
	}
	switch cfg.NetworkMode {
	case NetworkNone, NetworkIsolated, NetworkHost:
	case "":
		problems = append(problems, errors.New("network mode is required"))
	default:
		problems = append(problems, fmt.Errorf("invalid network mode %q", cfg.NetworkMode))
	}
	if len(cfg.Command) == 0 {
		problems = append(problems, errors.New("command is required after --"))
	}
	if cfg.NetworkMode == NetworkNone && (len(cfg.AllowHosts) > 0 || len(cfg.AllowCIDRs) > 0 || len(cfg.AllowPorts) > 0) {
		problems = append(problems, errors.New("allow rules are incompatible with --net none"))
	}
	for _, warn := range cfg.Warn {
		if warn != "net" {
			problems = append(problems, fmt.Errorf("unsupported warn mode %q", warn))
		}
	}
	if cfg.Pids < 0 {
		problems = append(problems, errors.New("pids must be zero or positive"))
	}
	if len(problems) == 0 {
		return nil
	}
	return errors.Join(problems...)
}

func Summary(cfg Config) string {
	var b strings.Builder
	fmt.Fprintf(&b, "rootfs: %s\n", cfg.RootFS)
	fmt.Fprintf(&b, "net: %s\n", cfg.NetworkMode)
	if cfg.Preset != "" {
		fmt.Fprintf(&b, "preset: %s\n", cfg.Preset)
	}
	if len(cfg.Warn) > 0 {
		fmt.Fprintf(&b, "warn: %s\n", strings.Join(cfg.Warn, ", "))
	}
	if cfg.Cwd != "" {
		fmt.Fprintf(&b, "cwd: %s\n", cfg.Cwd)
	}
	if cfg.Hostname != "" {
		fmt.Fprintf(&b, "hostname: %s\n", cfg.Hostname)
	}
	if cfg.Memory != "" {
		fmt.Fprintf(&b, "memory: %s\n", cfg.Memory)
	}
	if cfg.Pids > 0 {
		fmt.Fprintf(&b, "pids: %d\n", cfg.Pids)
	}
	if len(cfg.ROBind) > 0 {
		fmt.Fprintf(&b, "ro-bind:\n")
		for _, item := range cfg.ROBind {
			fmt.Fprintf(&b, "  - %s\n", item)
		}
	}
	if len(cfg.RWBind) > 0 {
		fmt.Fprintf(&b, "rw-bind:\n")
		for _, item := range cfg.RWBind {
			fmt.Fprintf(&b, "  - %s\n", item)
		}
	}
	if len(cfg.AllowHosts) > 0 {
		fmt.Fprintf(&b, "allow-host:\n")
		for _, item := range cfg.AllowHosts {
			fmt.Fprintf(&b, "  - %s\n", item)
		}
	}
	if len(cfg.AllowCIDRs) > 0 {
		fmt.Fprintf(&b, "allow-cidr:\n")
		for _, item := range cfg.AllowCIDRs {
			fmt.Fprintf(&b, "  - %s\n", item)
		}
	}
	if len(cfg.AllowPorts) > 0 {
		fmt.Fprintf(&b, "allow-port:\n")
		for _, item := range cfg.AllowPorts {
			fmt.Fprintf(&b, "  - %s\n", item)
		}
	}
	if len(cfg.Env) > 0 {
		fmt.Fprintf(&b, "env:\n")
		for _, item := range cfg.Env {
			fmt.Fprintf(&b, "  - %s\n", item)
		}
	}
	fmt.Fprintf(&b, "command: %s\n", strings.Join(cfg.Command, " "))
	return b.String()
}
