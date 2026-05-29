package spec

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func LoadPresetFile(path string) (Preset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Preset{}, fmt.Errorf("load preset file %q: %w", path, err)
	}

	preset, err := parsePresetFileYAML(data, path)
	if err != nil {
		return Preset{}, err
	}
	return preset, nil
}

func parsePresetFileYAML(data []byte, source string) (Preset, error) {
	if hasLegacyPresetList(data) {
		return Preset{}, fmt.Errorf("load preset file %q: legacy preset lists are no longer supported; pass a file containing exactly one preset document", source)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	var preset Preset
	if err := decoder.Decode(&preset); err != nil {
		return Preset{}, fmt.Errorf("load preset file %q: %w", source, err)
	}

	if err := decoder.Decode(&struct{}{}); err != nil && err != io.EOF {
		return Preset{}, fmt.Errorf("load preset file %q: expected a single YAML document: %w", source, err)
	}

	preset, err := validatePreset(preset, source)
	if err != nil {
		return Preset{}, fmt.Errorf("load preset file %q: %w", source, err)
	}
	return preset, nil
}

func hasLegacyPresetList(data []byte) bool {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return false
	}
	if len(node.Content) == 0 {
		return false
	}

	root := node.Content[0]
	if root.Kind != yaml.MappingNode {
		return false
	}

	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "presets" {
			return true
		}
	}
	return false
}

func validatePreset(preset Preset, source string) (Preset, error) {
	preset.Description = strings.TrimSpace(preset.Description)
	preset.Cwd = strings.TrimSpace(preset.Cwd)
	preset.Hostname = strings.TrimSpace(preset.Hostname)
	preset.Memory = strings.TrimSpace(preset.Memory)
	preset.NetworkPolicyFile = strings.TrimSpace(preset.NetworkPolicyFile)
	preset.Rootfs.Path = strings.TrimSpace(preset.Rootfs.Path)
	preset.Rootfs.Template = strings.TrimSpace(preset.Rootfs.Template)
	preset.Rootfs.RequiredCommands = normalizeRequiredCommands(preset.Rootfs.RequiredCommands)

	switch {
	case preset.NetworkPolicy != nil && preset.NetworkPolicyFile != "":
		return Preset{}, fmt.Errorf("specify either networkPolicy or networkPolicyFile, not both")
	case preset.NetworkPolicy == nil && preset.NetworkPolicyFile == "":
		return Preset{}, fmt.Errorf("networkPolicy or networkPolicyFile is required")
	}

	if preset.NetworkPolicyFile != "" {
		policyPath := preset.NetworkPolicyFile
		if !filepath.IsAbs(policyPath) {
			policyPath = filepath.Join(filepath.Dir(source), policyPath)
		}

		policy, err := LoadNetworkPolicyFile(policyPath)
		if err != nil {
			return Preset{}, fmt.Errorf("load referenced network policy %q: %w", preset.NetworkPolicyFile, err)
		}
		preset.NetworkPolicy = &policy
	}

	if preset.NetworkPolicy != nil {
		normalized, err := NormalizeNetworkPolicy(*preset.NetworkPolicy)
		if err != nil {
			return Preset{}, fmt.Errorf("networkPolicy: %w", err)
		}
		preset.NetworkPolicy = &normalized
	}

	if preset.Cwd != "" && !filepath.IsAbs(preset.Cwd) {
		return Preset{}, fmt.Errorf("cwd must be an absolute path, got %q", preset.Cwd)
	}

	if preset.Pids < 0 {
		return Preset{}, fmt.Errorf("pids must be zero or greater, got %d", preset.Pids)
	}

	return preset, nil
}
