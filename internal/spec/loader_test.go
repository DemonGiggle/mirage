package spec

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestLoadPresetsFromFS(t *testing.T) {
	presets, err := loadPresetsFromFS(fstest.MapFS{
		"presets/custom.yaml": {
			Data: []byte(`presets:
  - name: custom
    network: isolated
    allow_hosts:
      - example.com:443
    description: Custom preset
`),
		},
	}, "presets")
	if err != nil {
		t.Fatalf("loadPresetsFromFS returned error: %v", err)
	}

	preset, ok := presets["custom"]
	if !ok {
		t.Fatalf("expected custom preset to be loaded, got %v", presets)
	}
	if len(preset.AllowHosts) != 1 || preset.AllowHosts[0] != "example.com:443" {
		t.Fatalf("unexpected preset content: %#v", preset)
	}
}

func TestLoadPresetsFromFSRejectsUnknownFields(t *testing.T) {
	_, err := loadPresetsFromFS(fstest.MapFS{
		"presets/custom.yaml": {
			Data: []byte(`presets:
  - name: custom
    network: isolated
    unknown_field: true
`),
		},
	}, "presets")
	if err == nil {
		t.Fatal("expected unknown field to fail")
	}
	if !strings.Contains(err.Error(), "unknown_field") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadBuiltInPresetsFromEmbed(t *testing.T) {
	presets, err := loadPresetsFromFS(builtInPresetFiles, "presets")
	if err != nil {
		t.Fatalf("loadPresetsFromFS returned error: %v", err)
	}
	if len(presets) != len(BuiltInPresets) {
		t.Fatalf("unexpected built-in preset count: got %d want %d", len(presets), len(BuiltInPresets))
	}
	for _, name := range []string{"offline", "github", "openai", "openclaw-offline", "openclaw-openai"} {
		if _, ok := presets[name]; !ok {
			t.Fatalf("expected built-in embedded preset %q, got %v", name, PresetNames())
		}
	}
}
