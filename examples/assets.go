package examples

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
)

//go:embed network-policies/*.yaml presets/*.yaml
var bundledFiles embed.FS

func NetworkPolicyNames() ([]string, error) {
	return listYAMLFiles("network-policies")
}

func PresetNames() ([]string, error) {
	return listYAMLFiles("presets")
}

func ReadNetworkPolicy(name string) ([]byte, error) {
	return readBundledFile("network-policies", name)
}

func ReadPreset(name string) ([]byte, error) {
	return readBundledFile("presets", name)
}

func ExportNetworkPolicies(outputRoot string) error {
	return exportDir("network-policies", outputRoot)
}

func ExportPresets(outputRoot string) error {
	return exportDir("presets", outputRoot)
}

func listYAMLFiles(dir string) ([]string, error) {
	entries, err := fs.ReadDir(bundledFiles, dir)
	if err != nil {
		return nil, fmt.Errorf("read bundled directory %q: %w", dir, err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || path.Ext(entry.Name()) != ".yaml" {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

func readBundledFile(dir, name string) ([]byte, error) {
	filePath := path.Join(dir, name)
	data, err := fs.ReadFile(bundledFiles, filePath)
	if err != nil {
		return nil, fmt.Errorf("read bundled file %q: %w", filePath, err)
	}
	return data, nil
}

func exportDir(dir, outputRoot string) error {
	names, err := listYAMLFiles(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outputRoot, 0o755); err != nil {
		return fmt.Errorf("create output directory %q: %w", outputRoot, err)
	}
	for _, name := range names {
		data, err := readBundledFile(dir, name)
		if err != nil {
			return err
		}
		destPath := filepath.Join(outputRoot, name)
		if err := os.WriteFile(destPath, data, 0o644); err != nil {
			return fmt.Errorf("write bundled file %q: %w", destPath, err)
		}
	}
	return nil
}
