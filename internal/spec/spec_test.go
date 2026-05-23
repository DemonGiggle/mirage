package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPresetFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.json")
	if err := os.WriteFile(path, []byte(`{
  "presets": [
    {
      "name": "team-openai",
      "network": "isolated",
      "allow_hosts": ["example.com:443"],
      "description": "Team preset"
    }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write preset file: %v", err)
	}

	presets, err := LoadPresetFile(path)
	if err != nil {
		t.Fatalf("LoadPresetFile returned error: %v", err)
	}

	got, ok := presets["team-openai"]
	if !ok {
		t.Fatalf("expected team-openai preset, got %#v", presets)
	}
	if got.NetworkMode != NetworkIsolated {
		t.Fatalf("unexpected network mode: %q", got.NetworkMode)
	}
	if len(got.AllowHosts) != 1 || got.AllowHosts[0] != "example.com:443" {
		t.Fatalf("unexpected allow hosts: %#v", got.AllowHosts)
	}
}

func TestLoadPresetFileRejectsDuplicatePresetNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.json")
	if err := os.WriteFile(path, []byte(`{
  "presets": [
    {"name": "dup", "network": "host", "description": "first"},
    {"name": "dup", "network": "host", "description": "second"}
  ]
}`), 0o644); err != nil {
		t.Fatalf("write preset file: %v", err)
	}

	_, err := LoadPresetFile(path)
	if err == nil || !strings.Contains(err.Error(), `duplicate preset "dup"`) {
		t.Fatalf("expected duplicate preset error, got %v", err)
	}
}
