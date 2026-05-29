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
    networkPolicy:
      version: 1
      loopback:
        default: allow
      ingress:
        default: allow
        rules: []
      egress:
        default: allow
        rules: []
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
	if preset.NetworkPolicy == nil || preset.NetworkPolicy.Egress.Default != PolicyAllow {
		t.Fatalf("unexpected preset content: %#v", preset)
	}
}

func TestLoadPresetsFromFSRejectsUnknownFields(t *testing.T) {
	_, err := loadPresetsFromFS(fstest.MapFS{
		"presets/custom.yaml": {
			Data: []byte(`presets:
  - name: custom
    networkPolicy:
      version: 1
      loopback:
        default: allow
      ingress:
        default: allow
        rules: []
      egress:
        default: allow
        rules: []
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
	for _, name := range []string{"allow-all", "offline", "openclaw-offline"} {
		if _, ok := presets[name]; !ok {
			t.Fatalf("expected built-in embedded preset %q, got %v", name, PresetNames())
		}
	}
}
