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
	if got.NetworkPolicy == nil || got.NetworkPolicy.Egress.Default != PolicyDeny {
		t.Fatalf("unexpected network policy: %#v", got.NetworkPolicy)
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

func TestLoadPresetFileWithNetworkPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.yaml")
	if err := os.WriteFile(path, []byte(`presets:
  - name: team-policy
    networkPolicy:
      version: 1
      loopback:
        default: allow
      ingress:
        default: deny
        rules: []
      egress:
        default: deny
        rules:
          - name: allow-lan
            action: allow
            destination:
              cidr: 192.168.1.10/16
            protocol: any
    description: Team policy preset
`), 0o644); err != nil {
		t.Fatalf("write preset file: %v", err)
	}

	presets, err := LoadPresetFile(path)
	if err != nil {
		t.Fatalf("LoadPresetFile returned error: %v", err)
	}

	got := presets["team-policy"]
	if got.NetworkPolicy == nil {
		t.Fatal("expected network policy to be loaded")
	}
	if got.NetworkPolicy.Egress.Rules[0].Destination.CIDR != "192.168.0.0/16" {
		t.Fatalf("expected normalized policy selector, got %#v", got.NetworkPolicy.Egress.Rules[0].Destination)
	}
}

func TestLoadPresetFileRejectsLegacyNetworkField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.yaml")
	if err := os.WriteFile(path, []byte(`presets:
  - name: legacy
    network: none
    description: Legacy preset
`), 0o644); err != nil {
		t.Fatalf("write preset file: %v", err)
	}

	_, err := LoadPresetFile(path)
	if err == nil || !strings.Contains(err.Error(), "field network not found") {
		t.Fatalf("expected legacy network field error, got %v", err)
	}
}

func TestLoadPresetFileRejectsDuplicatePresetNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.yaml")
	if err := os.WriteFile(path, []byte(`presets:
  - name: dup
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
    description: first
  - name: dup
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
	if preset.NetworkPolicy == nil || preset.NetworkPolicy.Egress.Default != PolicyDeny {
		t.Fatalf("unexpected network policy: %#v", preset.NetworkPolicy)
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
	if got.NetworkPolicy == nil || got.NetworkPolicy.Egress.Default != PolicyAllow {
		t.Fatalf("expected local preset to override built-in, got %#v", got)
	}
	if got.Description != "Override built-in offline preset" {
		t.Fatalf("unexpected preset description: %#v", got)
	}
}

func TestValidateRejectsInvalidRuntimeMode(t *testing.T) {
	policy := AllowAllNetworkPolicy()
	err := Validate(Config{
		RootFS:        "/",
		NetworkPolicy: &policy,
		RuntimeMode:   "sidecar",
		Command:       []string{"/bin/true"},
	})
	if err == nil || !strings.Contains(err.Error(), `invalid runtime mode "sidecar"`) {
		t.Fatalf("expected invalid runtime mode error, got %v", err)
	}
}

func TestValidateRejectsMissingNetworkPolicy(t *testing.T) {
	err := Validate(Config{
		RootFS:      "/srv/rootfs",
		RuntimeMode: RuntimeModeInit,
		Command:     []string{"/sbin/init"},
	})
	if err == nil || !strings.Contains(err.Error(), "networkPolicy is required") {
		t.Fatalf("expected missing network policy error, got %v", err)
	}
}

func TestValidateAllowsNetworkPolicy(t *testing.T) {
	policy := NetworkPolicy{
		Version:  1,
		Loopback: LoopbackPolicy{Default: PolicyAllow},
		Ingress:  IngressPolicy{Default: PolicyDeny, Rules: []IngressRule{}},
		Egress:   EgressPolicy{Default: PolicyDeny, Rules: []EgressRule{}},
	}
	err := Validate(Config{
		RootFS:        "/srv/rootfs",
		NetworkPolicy: &policy,
		RuntimeMode:   RuntimeModeInit,
		Command:       []string{"/sbin/init"},
	})
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestNormalizeRuntimeModeDefaultsToDirect(t *testing.T) {
	if got := NormalizeRuntimeMode(""); got != RuntimeModeDirect {
		t.Fatalf("expected empty runtime mode to normalize to %q, got %q", RuntimeModeDirect, got)
	}
}

func TestValidateRejectsInitModeWithHostRootfs(t *testing.T) {
	policy := AllowAllNetworkPolicy()
	err := Validate(Config{
		RootFS:        "/",
		NetworkPolicy: &policy,
		RuntimeMode:   RuntimeModeInit,
		Command:       []string{"/sbin/init"},
	})
	if err == nil || !strings.Contains(err.Error(), "runtime-mode init requires a dedicated rootfs") {
		t.Fatalf("expected init-mode rootfs error, got %v", err)
	}
}

func TestValidateRejectsInitModeWithoutRootfs(t *testing.T) {
	policy := AllowAllNetworkPolicy()
	err := Validate(Config{
		NetworkPolicy: &policy,
		RuntimeMode:   RuntimeModeInit,
		Command:       []string{"/sbin/init"},
	})
	if err == nil || !strings.Contains(err.Error(), "runtime-mode init requires a dedicated rootfs") {
		t.Fatalf("expected init-mode missing rootfs error, got %v", err)
	}
}

func TestValidateRejectsBindMountsOverGuestCgroupTreeInInitMode(t *testing.T) {
	policy := AllowAllNetworkPolicy()
	err := Validate(Config{
		RootFS:        "/srv/rootfs",
		NetworkPolicy: &policy,
		RuntimeMode:   RuntimeModeInit,
		RWBind:        []string{"/host/path:/sys/fs/cgroup"},
		Command:       []string{"/sbin/init"},
	})
	if err == nil || !strings.Contains(err.Error(), `runtime-mode init reserves guest path "/sys/fs/cgroup"`) {
		t.Fatalf("expected init-mode cgroup bind error, got %v", err)
	}
}

func TestValidateRejectsManagedRuntimeMountTargetsInInitMode(t *testing.T) {
	policy := AllowAllNetworkPolicy()
	err := Validate(Config{
		RootFS:        "/srv/rootfs",
		NetworkPolicy: &policy,
		RuntimeMode:   RuntimeModeInit,
		ROBind:        []string{"/host/path:/dev/null"},
		Command:       []string{"/sbin/init"},
	})
	if err == nil || !strings.Contains(err.Error(), `runtime-mode init manages guest path "/dev/null"`) {
		t.Fatalf("expected init-mode runtime mount error, got %v", err)
	}
}
