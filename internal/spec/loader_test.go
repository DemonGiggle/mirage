package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPresetFileRejectsLegacyPresetLists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preset.yaml")
	if err := os.WriteFile(path, []byte(`presets:
  - networkPolicy:
      version: 1
      loopback:
        default: allow
      ingress:
        default: deny
        rules: []
      egress:
        default: deny
        rules: []
`), 0o644); err != nil {
		t.Fatalf("write preset file: %v", err)
	}

	_, err := LoadPresetFile(path)
	if err == nil || !strings.Contains(err.Error(), "legacy preset lists are no longer supported") {
		t.Fatalf("expected legacy preset list error, got %v", err)
	}
}

func TestLoadPresetFileRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preset.yaml")
	if err := os.WriteFile(path, []byte(`networkPolicy:
  version: 1
  loopback:
    default: allow
  ingress:
    default: deny
    rules: []
  egress:
    default: deny
    rules: []
unknownField: true
`), 0o644); err != nil {
		t.Fatalf("write preset file: %v", err)
	}

	_, err := LoadPresetFile(path)
	if err == nil || !strings.Contains(err.Error(), "unknownField") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestLoadPresetFileRejectsInlineAndReferencedPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preset.yaml")
	if err := os.WriteFile(path, []byte(`networkPolicyFile: ./offline.yaml
networkPolicy:
  version: 1
  loopback:
    default: allow
  ingress:
    default: deny
    rules: []
  egress:
    default: deny
    rules: []
`), 0o644); err != nil {
		t.Fatalf("write preset file: %v", err)
	}

	_, err := LoadPresetFile(path)
	if err == nil || !strings.Contains(err.Error(), "either networkPolicy or networkPolicyFile") {
		t.Fatalf("expected mutual exclusion error, got %v", err)
	}
}
