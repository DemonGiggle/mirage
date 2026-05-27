package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPresetFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.yaml")
	if err := os.WriteFile(path, []byte(`presets:
  - name: team-offline
    network: none
    rootfs:
      template: openclaw-developer
      required_commands:
        - node
      recommended_cwd: /workspace
    description: Team preset
`), 0o644); err != nil {
		t.Fatalf("write preset file: %v", err)
	}

	presets, err := LoadPresetFile(path)
	if err != nil {
		t.Fatalf("LoadPresetFile returned error: %v", err)
	}

	got, ok := presets["team-offline"]
	if !ok {
		t.Fatalf("expected team-offline preset, got %#v", presets)
	}
	if got.NetworkMode != NetworkNone {
		t.Fatalf("unexpected network mode: %q", got.NetworkMode)
	}
	if got.Rootfs.RecommendedTemplate != "openclaw-developer" {
		t.Fatalf("unexpected recommended template: %#v", got.Rootfs)
	}
	if len(got.Rootfs.RequiredCommands) != 1 || got.Rootfs.RequiredCommands[0] != "node" {
		t.Fatalf("unexpected required commands: %#v", got.Rootfs)
	}
	if got.Rootfs.RecommendedCwd != "/workspace" {
		t.Fatalf("unexpected recommended cwd: %#v", got.Rootfs)
	}
}

func TestLoadPresetFileRejectsDuplicatePresetNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.yaml")
	if err := os.WriteFile(path, []byte(`presets:
  - name: dup
    network: host
    description: first
  - name: dup
    network: host
    description: second
`), 0o644); err != nil {
		t.Fatalf("write preset file: %v", err)
	}

	_, err := LoadPresetFile(path)
	if err == nil || !strings.Contains(err.Error(), `duplicate preset "dup"`) {
		t.Fatalf("expected duplicate preset error, got %v", err)
	}
}

func TestLoadPresetFileRejectsJSONExtension(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.json")
	if err := os.WriteFile(path, []byte(`{"presets":[]}`), 0o644); err != nil {
		t.Fatalf("write preset file: %v", err)
	}

	_, err := LoadPresetFile(path)
	if err == nil || !strings.Contains(err.Error(), "must use a .yaml or .yml extension") {
		t.Fatalf("expected YAML extension error, got %v", err)
	}
}

func TestBuiltInOpenclawPresetIncludesRootfsExpectations(t *testing.T) {
	preset := BuiltInPresets["openclaw-offline"]
	if preset.NetworkMode != NetworkNone {
		t.Fatalf("unexpected network mode: %q", preset.NetworkMode)
	}
	if preset.Rootfs.RecommendedTemplate != "openclaw-developer" {
		t.Fatalf("unexpected recommended template: %#v", preset.Rootfs)
	}
	if len(preset.Rootfs.RequiredCommands) != 1 || preset.Rootfs.RequiredCommands[0] != "node" {
		t.Fatalf("unexpected required commands: %#v", preset.Rootfs)
	}
	if preset.Rootfs.RecommendedCwd != "/workspace" {
		t.Fatalf("unexpected recommended cwd: %#v", preset.Rootfs)
	}
}

func TestAvailablePresetsMergesLocalYAMLOverBuiltIns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.yaml")
	if err := os.WriteFile(path, []byte(`presets:
  - name: offline
    network: host
    description: Override built-in offline preset
`), 0o644); err != nil {
		t.Fatalf("write preset file: %v", err)
	}

	presets, err := AvailablePresets(path)
	if err != nil {
		t.Fatalf("AvailablePresets returned error: %v", err)
	}

	got, ok := presets["offline"]
	if !ok {
		t.Fatalf("expected offline preset, got %#v", presets)
	}
	if got.NetworkMode != NetworkHost {
		t.Fatalf("expected local preset to override built-in, got %#v", got)
	}
	if got.Description != "Override built-in offline preset" {
		t.Fatalf("unexpected preset description: %#v", got)
	}
}

func TestValidateRejectsInvalidRuntimeMode(t *testing.T) {
	err := Validate(Config{
		RootFS:      "/",
		NetworkMode: NetworkHost,
		RuntimeMode: "sidecar",
		Command:     []string{"/bin/true"},
	})
	if err == nil || !strings.Contains(err.Error(), `invalid runtime mode "sidecar"`) {
		t.Fatalf("expected invalid runtime mode error, got %v", err)
	}
}

func TestValidateRejectsUnsupportedNetworkMode(t *testing.T) {
	err := Validate(Config{
		RootFS:      "/srv/rootfs",
		NetworkMode: NetworkMode("isolated"),
		RuntimeMode: RuntimeModeInit,
		Command:     []string{"/sbin/init"},
	})
	if err == nil || !strings.Contains(err.Error(), `invalid network mode "isolated"`) {
		t.Fatalf("expected invalid network mode error, got %v", err)
	}
}

func TestNormalizeRuntimeModeDefaultsToDirect(t *testing.T) {
	if got := NormalizeRuntimeMode(""); got != RuntimeModeDirect {
		t.Fatalf("expected empty runtime mode to normalize to %q, got %q", RuntimeModeDirect, got)
	}
}

func TestValidateRejectsInitModeWithHostRootfs(t *testing.T) {
	err := Validate(Config{
		RootFS:      "/",
		NetworkMode: NetworkHost,
		RuntimeMode: RuntimeModeInit,
		Command:     []string{"/sbin/init"},
	})
	if err == nil || !strings.Contains(err.Error(), "runtime-mode init requires a dedicated rootfs") {
		t.Fatalf("expected init-mode rootfs error, got %v", err)
	}
}

func TestValidateRejectsInitModeWithoutRootfs(t *testing.T) {
	err := Validate(Config{
		NetworkMode: NetworkHost,
		RuntimeMode: RuntimeModeInit,
		Command:     []string{"/sbin/init"},
	})
	if err == nil || !strings.Contains(err.Error(), "runtime-mode init requires a dedicated rootfs") {
		t.Fatalf("expected init-mode missing rootfs error, got %v", err)
	}
}

func TestValidateRejectsBindMountsOverGuestCgroupTreeInInitMode(t *testing.T) {
	err := Validate(Config{
		RootFS:      "/srv/rootfs",
		NetworkMode: NetworkHost,
		RuntimeMode: RuntimeModeInit,
		RWBind:      []string{"/host/path:/sys/fs/cgroup"},
		Command:     []string{"/sbin/init"},
	})
	if err == nil || !strings.Contains(err.Error(), `runtime-mode init reserves guest path "/sys/fs/cgroup"`) {
		t.Fatalf("expected init-mode cgroup bind error, got %v", err)
	}
}

func TestValidateRejectsManagedRuntimeMountTargetsInInitMode(t *testing.T) {
	err := Validate(Config{
		RootFS:      "/srv/rootfs",
		NetworkMode: NetworkHost,
		RuntimeMode: RuntimeModeInit,
		ROBind:      []string{"/host/path:/dev/null"},
		Command:     []string{"/sbin/init"},
	})
	if err == nil || !strings.Contains(err.Error(), `runtime-mode init manages guest path "/dev/null"`) {
		t.Fatalf("expected init-mode runtime mount error, got %v", err)
	}
}
