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
      "rootfs": {
        "template": "openclaw-developer",
        "required_commands": ["node"],
        "recommended_cwd": "/workspace"
      },
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

func TestBuiltInOpenclawPresetIncludesRootfsExpectations(t *testing.T) {
	preset := BuiltInPresets["openclaw-openai"]
	if len(preset.AllowHosts) != 1 || preset.AllowHosts[0] != "127.0.0.1:18789" {
		t.Fatalf("unexpected allow hosts: %#v", preset.AllowHosts)
	}
	if len(preset.AllowPorts) != 1 || preset.AllowPorts[0] != "443" {
		t.Fatalf("unexpected allow ports: %#v", preset.AllowPorts)
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

func TestValidateRejectsInitModeWithObservedNetworking(t *testing.T) {
	err := Validate(Config{
		RootFS:      "/srv/rootfs",
		NetworkMode: NetworkIsolated,
		RuntimeMode: RuntimeModeInit,
		Command:     []string{"/sbin/init"},
	})
	if err == nil || !strings.Contains(err.Error(), "runtime-mode init is incompatible with observed networking") {
		t.Fatalf("expected init-mode networking error, got %v", err)
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
