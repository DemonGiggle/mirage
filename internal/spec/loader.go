package spec

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed presets/*.yaml
var builtInPresetFiles embed.FS

var BuiltInPresets = mustLoadBuiltInPresets()

type presetFileDocument struct {
	Presets []Preset `json:"presets" yaml:"presets"`
}

func mustLoadBuiltInPresets() map[string]Preset {
	presets, err := loadPresetsFromFS(builtInPresetFiles, "presets")
	if err != nil {
		panic(fmt.Sprintf("load built-in presets: %v", err))
	}
	return presets
}

func loadPresetsFromFS(fsys fs.FS, dir string) (map[string]Preset, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read preset directory %q: %w", dir, err)
	}

	presets := make(map[string]Preset)
	var found bool
	for _, entry := range entries {
		ext := strings.ToLower(path.Ext(entry.Name()))
		if entry.IsDir() || (ext != ".yaml" && ext != ".yml") {
			continue
		}

		found = true
		filePath := path.Join(dir, entry.Name())
		data, err := fs.ReadFile(fsys, filePath)
		if err != nil {
			return nil, fmt.Errorf("read preset file %q: %w", filePath, err)
		}

		filePresets, err := parsePresetFileYAML(data, filePath)
		if err != nil {
			return nil, err
		}
		for name, preset := range filePresets {
			if _, exists := presets[name]; exists {
				return nil, fmt.Errorf("preset %q is duplicated across preset files", name)
			}
			presets[name] = preset
		}
	}

	if !found {
		return nil, fmt.Errorf("no preset files found in %q", dir)
	}
	return presets, nil
}

func LoadPresetFile(path string) (map[string]Preset, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
	default:
		return nil, fmt.Errorf("preset file %q must use a .yaml or .yml extension", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read preset file %q: %w", path, err)
	}

	return parsePresetFileYAML(data, path)
}

func parsePresetFileYAML(data []byte, source string) (map[string]Preset, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	var doc presetFileDocument
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse preset file %q: %w", source, err)
	}

	var extra any
	err := decoder.Decode(&extra)
	switch {
	case err == io.EOF:
		return validatePresetDocument(doc, source)
	case err != nil:
		return nil, fmt.Errorf("parse preset file %q: %w", source, err)
	default:
		return nil, fmt.Errorf("parse preset file %q: multiple YAML documents are not supported", source)
	}
}

func validatePresetDocument(doc presetFileDocument, source string) (map[string]Preset, error) {
	if len(doc.Presets) == 0 {
		return nil, fmt.Errorf("preset file %q does not define any presets", source)
	}

	out := make(map[string]Preset, len(doc.Presets))
	for _, preset := range doc.Presets {
		if preset.Name == "" {
			return nil, fmt.Errorf("preset file %q contains a preset without a name", source)
		}
		if _, exists := out[preset.Name]; exists {
			return nil, fmt.Errorf("preset file %q defines duplicate preset %q", source, preset.Name)
		}
		if preset.NetworkPolicy == nil {
			return nil, fmt.Errorf("preset file %q preset %q must define networkPolicy", source, preset.Name)
		}
		policy, err := NormalizeNetworkPolicy(*preset.NetworkPolicy)
		if err != nil {
			return nil, fmt.Errorf("preset file %q preset %q has invalid networkPolicy: %w", source, preset.Name, err)
		}
		preset.NetworkPolicy = &policy
		preset.Rootfs.RecommendedTemplate = strings.TrimSpace(preset.Rootfs.RecommendedTemplate)
		preset.Rootfs.RecommendedCwd = strings.TrimSpace(preset.Rootfs.RecommendedCwd)
		if preset.Rootfs.RecommendedCwd != "" && !filepath.IsAbs(preset.Rootfs.RecommendedCwd) {
			return nil, fmt.Errorf("preset file %q preset %q has invalid recommended rootfs cwd %q", source, preset.Name, preset.Rootfs.RecommendedCwd)
		}
		preset.Rootfs.RequiredCommands = normalizeCommands(preset.Rootfs.RequiredCommands)
		out[preset.Name] = preset
	}
	return out, nil
}
