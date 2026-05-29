package spec

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type RuntimeMode string

const (
	RuntimeModeDirect RuntimeMode = "direct"
	RuntimeModeInit   RuntimeMode = "init"
)

type RootfsPreset struct {
	Path             string   `json:"path,omitempty" yaml:"path,omitempty"`
	Template         string   `json:"template,omitempty" yaml:"template,omitempty"`
	RequiredCommands []string `json:"required_commands,omitempty" yaml:"required_commands,omitempty"`
}

type Preset struct {
	Rootfs            RootfsPreset   `json:"rootfs,omitempty" yaml:"rootfs,omitempty"`
	NetworkPolicy     *NetworkPolicy `json:"networkPolicy,omitempty" yaml:"networkPolicy,omitempty"`
	NetworkPolicyFile string         `json:"networkPolicyFile,omitempty" yaml:"networkPolicyFile,omitempty"`
	ROBind            []string       `json:"roBind,omitempty" yaml:"roBind,omitempty"`
	RWBind            []string       `json:"rwBind,omitempty" yaml:"rwBind,omitempty"`
	Env               []string       `json:"env,omitempty" yaml:"env,omitempty"`
	Cwd               string         `json:"cwd,omitempty" yaml:"cwd,omitempty"`
	Hostname          string         `json:"hostname,omitempty" yaml:"hostname,omitempty"`
	Memory            string         `json:"memory,omitempty" yaml:"memory,omitempty"`
	Pids              int            `json:"pids,omitempty" yaml:"pids,omitempty"`
	Description       string         `json:"description,omitempty" yaml:"description,omitempty"`
}

type Config struct {
	RootFS            string
	NetworkPolicyFile string
	NetworkPolicy     *NetworkPolicy
	RuntimeMode       RuntimeMode
	ScopeName         string
	PresetFile        string
	ROBind            []string
	RWBind            []string
	Env               []string
	StdoutLog         string
	StderrLog         string
	Cwd               string
	Hostname          string
	Memory            string
	Pids              int
	DryRun            bool
	Command           []string
}

func ApplyPresetFile(cfg Config) (Config, Preset, error) {
	if cfg.PresetFile == "" {
		return cfg, Preset{}, nil
	}

	preset, err := LoadPresetFile(cfg.PresetFile)
	if err != nil {
		return Config{}, Preset{}, err
	}

	if preset.Rootfs.Path != "" {
		cfg.RootFS = preset.Rootfs.Path
	}
	if preset.NetworkPolicy != nil {
		cfg.NetworkPolicy = preset.NetworkPolicy
	}
	if len(preset.ROBind) > 0 {
		cfg.ROBind = append([]string(nil), preset.ROBind...)
	}
	if len(preset.RWBind) > 0 {
		cfg.RWBind = append([]string(nil), preset.RWBind...)
	}
	if len(preset.Env) > 0 {
		cfg.Env = append([]string(nil), preset.Env...)
	}
	if preset.Cwd != "" {
		cfg.Cwd = preset.Cwd
	}
	if preset.Hostname != "" {
		cfg.Hostname = preset.Hostname
	}
	if preset.Memory != "" {
		cfg.Memory = preset.Memory
	}
	if preset.Pids > 0 {
		cfg.Pids = preset.Pids
	}

	return cfg, preset, nil
}

func normalizeRequiredCommands(commands []string) []string {
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
	if cfg.NetworkPolicy == nil {
		problems = append(problems, errors.New("networkPolicy is required"))
	} else if err := ValidateNetworkPolicy(cfg.NetworkPolicy); err != nil {
		problems = append(problems, err)
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
	}
	if cfg.NetworkPolicyFile != "" {
		fmt.Fprintf(&b, "network-policy-file: %s\n", cfg.NetworkPolicyFile)
	}
	fmt.Fprintf(&b, "runtime-mode: %s\n", NormalizeRuntimeMode(cfg.RuntimeMode))
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
