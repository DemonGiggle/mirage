package spec

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

type NetworkMode string

const (
	NetworkNone NetworkMode = "none"
	NetworkHost NetworkMode = "host"
)

type RuntimeMode string

const (
	RuntimeModeDirect RuntimeMode = "direct"
	RuntimeModeInit   RuntimeMode = "init"
)

type RootfsExpectations struct {
	RecommendedTemplate string   `json:"template,omitempty" yaml:"template,omitempty"`
	RequiredCommands    []string `json:"required_commands,omitempty" yaml:"required_commands,omitempty"`
	RecommendedCwd      string   `json:"recommended_cwd,omitempty" yaml:"recommended_cwd,omitempty"`
}

type Preset struct {
	Name          string             `json:"name" yaml:"name"`
	NetworkMode   NetworkMode        `json:"network,omitempty" yaml:"network,omitempty"`
	NetworkPolicy *NetworkPolicy     `json:"networkPolicy,omitempty" yaml:"networkPolicy,omitempty"`
	Rootfs        RootfsExpectations `json:"rootfs,omitempty" yaml:"rootfs,omitempty"`
	Description   string             `json:"description" yaml:"description"`
}

type Config struct {
	RootFS        string
	NetworkMode   NetworkMode
	NetworkPolicy *NetworkPolicy
	RuntimeMode   RuntimeMode
	ScopeName     string
	Preset        string
	PresetFile    string
	ROBind        []string
	RWBind        []string
	Env           []string
	StdoutLog     string
	StderrLog     string
	Cwd           string
	Hostname      string
	Memory        string
	Pids          int
	DryRun        bool
	Command       []string
}

func PresetNames() []string {
	names := make([]string, 0, len(BuiltInPresets))
	for name := range BuiltInPresets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func ApplyPreset(cfg Config) (Config, error) {
	if cfg.Preset == "" {
		return cfg, nil
	}
	presets, err := AvailablePresets(cfg.PresetFile)
	if err != nil {
		return cfg, err
	}
	preset, ok := presets[cfg.Preset]
	if !ok {
		return cfg, fmt.Errorf("unknown preset %q", cfg.Preset)
	}
	if cfg.NetworkMode != "" && cfg.NetworkPolicy != nil {
		return cfg, errors.New("network and networkPolicy cannot both be set")
	}
	if cfg.NetworkMode == "" && cfg.NetworkPolicy == nil {
		cfg.NetworkMode = preset.NetworkMode
		if preset.NetworkPolicy != nil {
			cfg.NetworkPolicy = preset.NetworkPolicy
		}
		return cfg, nil
	}
	if cfg.NetworkMode != "" && preset.NetworkPolicy != nil {
		return cfg, fmt.Errorf("preset %q defines networkPolicy; --net cannot be combined with that preset", cfg.Preset)
	}
	if cfg.NetworkPolicy != nil && preset.NetworkMode != "" {
		return cfg, fmt.Errorf("preset %q defines network %q; inline networkPolicy cannot be combined with that preset", cfg.Preset, preset.NetworkMode)
	}
	return cfg, nil
}

func AvailablePresets(presetFile string) (map[string]Preset, error) {
	presets := make(map[string]Preset, len(BuiltInPresets))
	for name, preset := range BuiltInPresets {
		presets[name] = preset
	}
	if presetFile == "" {
		return presets, nil
	}

	loaded, err := LoadPresetFile(presetFile)
	if err != nil {
		return nil, err
	}
	for name, preset := range loaded {
		presets[name] = preset
	}
	return presets, nil
}

func normalizeCommands(commands []string) []string {
	var out []string
	for _, command := range commands {
		command = strings.TrimSpace(command)
		if command == "" || slicesContains(out, command) {
			continue
		}
		out = append(out, command)
	}
	return out
}

func slicesContains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func Validate(cfg Config) error {
	var problems []error
	if cfg.RootFS == "" {
		problems = append(problems, errors.New("rootfs is required"))
	}
	switch NormalizeRuntimeMode(cfg.RuntimeMode) {
	case RuntimeModeDirect, RuntimeModeInit:
	default:
		problems = append(problems, fmt.Errorf("invalid runtime mode %q", cfg.RuntimeMode))
	}
	if cfg.NetworkMode != "" && cfg.NetworkPolicy != nil {
		problems = append(problems, errors.New("network and networkPolicy cannot both be set"))
	}
	if cfg.NetworkPolicy != nil {
		if err := ValidateNetworkPolicy(cfg.NetworkPolicy); err != nil {
			problems = append(problems, err)
		}
	} else {
		switch cfg.NetworkMode {
		case NetworkNone, NetworkHost:
		case "":
			problems = append(problems, errors.New("network mode or networkPolicy is required"))
		default:
			problems = append(problems, fmt.Errorf("invalid network mode %q", cfg.NetworkMode))
		}
	}
	if len(cfg.Command) == 0 {
		problems = append(problems, errors.New("command is required after --"))
	}
	if NormalizeRuntimeMode(cfg.RuntimeMode) == RuntimeModeInit && (cfg.RootFS == "" || cfg.RootFS == "/") {
		problems = append(problems, errors.New("runtime-mode init requires a dedicated rootfs; use --rootfs with a non-root directory"))
	}
	if NormalizeRuntimeMode(cfg.RuntimeMode) == RuntimeModeInit {
		for _, mount := range append(append([]string{}, cfg.ROBind...), cfg.RWBind...) {
			target, ok := bindMountTarget(mount)
			if !ok {
				continue
			}
			if reservedPath, ok := reservedInitMountPath(target); ok {
				if reservedPath == "/sys/fs/cgroup" {
					problems = append(problems, fmt.Errorf("runtime-mode init reserves guest path %q for the delegated cgroup tree", target))
					continue
				}
				problems = append(problems, fmt.Errorf("runtime-mode init manages guest path %q via its runtime mount contract", target))
			}
		}
	}
	if cfg.StdoutLog != "" && cfg.StderrLog != "" && cfg.StdoutLog == cfg.StderrLog {
		problems = append(problems, errors.New("stdout-log and stderr-log must be different paths"))
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
	if cfg.NetworkPolicy != nil {
		fmt.Fprintf(&b, "network-policy: v%d\n", cfg.NetworkPolicy.Version)
		fmt.Fprintf(&b, "network-policy-loopback-default: %s\n", cfg.NetworkPolicy.Loopback.Default)
		fmt.Fprintf(&b, "network-policy-ingress: default=%s rules=%d\n", cfg.NetworkPolicy.Ingress.Default, len(cfg.NetworkPolicy.Ingress.Rules))
		fmt.Fprintf(&b, "network-policy-egress: default=%s rules=%d\n", cfg.NetworkPolicy.Egress.Default, len(cfg.NetworkPolicy.Egress.Rules))
	} else {
		fmt.Fprintf(&b, "net: %s\n", cfg.NetworkMode)
	}
	fmt.Fprintf(&b, "runtime-mode: %s\n", NormalizeRuntimeMode(cfg.RuntimeMode))
	if cfg.Preset != "" {
		fmt.Fprintf(&b, "preset: %s\n", cfg.Preset)
	}
	if cfg.PresetFile != "" {
		fmt.Fprintf(&b, "preset-file: %s\n", cfg.PresetFile)
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
	if len(cfg.Env) > 0 {
		fmt.Fprintf(&b, "env:\n")
		for _, item := range cfg.Env {
			fmt.Fprintf(&b, "  - %s\n", item)
		}
	}
	if cfg.StdoutLog != "" {
		fmt.Fprintf(&b, "stdout-log: %s\n", cfg.StdoutLog)
	}
	if cfg.StderrLog != "" {
		fmt.Fprintf(&b, "stderr-log: %s\n", cfg.StderrLog)
	}
	fmt.Fprintf(&b, "command: %s\n", strings.Join(cfg.Command, " "))
	return b.String()
}

func NormalizeRuntimeMode(mode RuntimeMode) RuntimeMode {
	if mode == "" {
		return RuntimeModeDirect
	}
	return mode
}

func bindMountTarget(entry string) (string, bool) {
	_, target, ok := strings.Cut(entry, ":")
	if !ok || target == "" || !filepath.IsAbs(target) {
		return "", false
	}
	return filepath.Clean(target), true
}

func reservedInitMountPath(target string) (string, bool) {
	for _, root := range []string{"/sys/fs/cgroup", "/proc", "/tmp", "/run", "/dev", "/sys"} {
		if target == root || strings.HasPrefix(target, root+"/") {
			return root, true
		}
	}
	return "", false
}
