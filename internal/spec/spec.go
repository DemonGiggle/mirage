package spec

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type NetworkMode string

const (
	NetworkNone     NetworkMode = "none"
	NetworkIsolated NetworkMode = "isolated"
	NetworkHost     NetworkMode = "host"
)

var BuiltInPresets = map[string]Preset{
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
	"openclaw-offline": {
		Name:        "openclaw-offline",
		NetworkMode: NetworkNone,
		Rootfs: RootfsExpectations{
			RecommendedTemplate: "openclaw",
			RequiredCommands:    []string{"node"},
			RecommendedCwd:      "/workspace",
		},
		Description: "OpenClaw-oriented offline preset for local-only agent work.",
	},
	"openclaw-openai": {
		Name:        "openclaw-openai",
		NetworkMode: NetworkIsolated,
		AllowPorts:  []string{"443"},
		Rootfs: RootfsExpectations{
			RecommendedTemplate: "openclaw",
			RequiredCommands:    []string{"node"},
			RecommendedCwd:      "/workspace",
		},
		Description: "OpenClaw-oriented preset for HTTPS-capable agent work with the openclaw rootfs template.",
	},
}

type RootfsExpectations struct {
	RecommendedTemplate string   `json:"template,omitempty"`
	RequiredCommands    []string `json:"required_commands,omitempty"`
	RecommendedCwd      string   `json:"recommended_cwd,omitempty"`
}

type Preset struct {
	Name        string             `json:"name"`
	NetworkMode NetworkMode        `json:"network"`
	AllowHosts  []string           `json:"allow_hosts"`
	AllowCIDRs  []string           `json:"allow_cidrs"`
	AllowPorts  []string           `json:"allow_ports"`
	Rootfs      RootfsExpectations `json:"rootfs,omitempty"`
	Description string             `json:"description"`
}

type Config struct {
	RootFS      string
	NetworkMode NetworkMode
	Preset      string
	PresetFile  string
	Warn        []string
	ROBind      []string
	RWBind      []string
	AllowHosts  []string
	AllowCIDRs  []string
	AllowPorts  []string
	Env         []string
	StdoutLog   string
	StderrLog   string
	Cwd         string
	Hostname    string
	Memory      string
	Pids        int
	DryRun      bool
	Command     []string
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
	if cfg.NetworkMode == "" {
		cfg.NetworkMode = preset.NetworkMode
	}
	if len(cfg.AllowHosts) == 0 && len(preset.AllowHosts) > 0 {
		cfg.AllowHosts = append([]string{}, preset.AllowHosts...)
	}
	if len(cfg.AllowCIDRs) == 0 && len(preset.AllowCIDRs) > 0 {
		cfg.AllowCIDRs = append([]string{}, preset.AllowCIDRs...)
	}
	if len(cfg.AllowPorts) == 0 && len(preset.AllowPorts) > 0 {
		cfg.AllowPorts = append([]string{}, preset.AllowPorts...)
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

type presetFileDocument struct {
	Presets []Preset `json:"presets"`
}

func LoadPresetFile(path string) (map[string]Preset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read preset file %q: %w", path, err)
	}

	var doc presetFileDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse preset file %q: %w", path, err)
	}
	if len(doc.Presets) == 0 {
		return nil, fmt.Errorf("preset file %q does not define any presets", path)
	}

	out := make(map[string]Preset, len(doc.Presets))
	for _, preset := range doc.Presets {
		if preset.Name == "" {
			return nil, fmt.Errorf("preset file %q contains a preset without a name", path)
		}
		if _, exists := out[preset.Name]; exists {
			return nil, fmt.Errorf("preset file %q defines duplicate preset %q", path, preset.Name)
		}
		switch preset.NetworkMode {
		case NetworkNone, NetworkIsolated, NetworkHost:
		default:
			return nil, fmt.Errorf("preset file %q preset %q has invalid network mode %q", path, preset.Name, preset.NetworkMode)
		}
		preset.Rootfs.RecommendedTemplate = strings.TrimSpace(preset.Rootfs.RecommendedTemplate)
		preset.Rootfs.RecommendedCwd = strings.TrimSpace(preset.Rootfs.RecommendedCwd)
		if preset.Rootfs.RecommendedCwd != "" && !filepath.IsAbs(preset.Rootfs.RecommendedCwd) {
			return nil, fmt.Errorf("preset file %q preset %q has invalid recommended rootfs cwd %q", path, preset.Name, preset.Rootfs.RecommendedCwd)
		}
		preset.Rootfs.RequiredCommands = normalizeCommands(preset.Rootfs.RequiredCommands)
		for _, command := range preset.Rootfs.RequiredCommands {
			if command == "" {
				return nil, fmt.Errorf("preset file %q preset %q has an empty required rootfs command", path, preset.Name)
			}
		}
		out[preset.Name] = preset
	}
	return out, nil
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
	fmt.Fprintf(&b, "net: %s\n", cfg.NetworkMode)
	if cfg.Preset != "" {
		fmt.Fprintf(&b, "preset: %s\n", cfg.Preset)
	}
	if cfg.PresetFile != "" {
		fmt.Fprintf(&b, "preset-file: %s\n", cfg.PresetFile)
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
	if cfg.StdoutLog != "" {
		fmt.Fprintf(&b, "stdout-log: %s\n", cfg.StdoutLog)
	}
	if cfg.StderrLog != "" {
		fmt.Fprintf(&b, "stderr-log: %s\n", cfg.StderrLog)
	}
	fmt.Fprintf(&b, "command: %s\n", strings.Join(cfg.Command, " "))
	return b.String()
}
