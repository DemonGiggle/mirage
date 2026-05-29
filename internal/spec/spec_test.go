package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPresetFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preset.yaml")
	if err := os.WriteFile(path, []byte(`rootfs:
  path: /srv/rootfs
  template: openclaw-developer
  required_commands:
    - node
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
cwd: /workspace
hostname: demo
memory: 512M
pids: 64
description: Team preset
`), 0o644); err != nil {
		t.Fatalf("write preset file: %v", err)
	}

	preset, err := LoadPresetFile(path)
	if err != nil {
		t.Fatalf("LoadPresetFile returned error: %v", err)
	}

	if preset.NetworkPolicy == nil || preset.NetworkPolicy.Egress.Default != PolicyDeny {
		t.Fatalf("unexpected network policy: %#v", preset.NetworkPolicy)
	}
	if preset.Rootfs.Path != "/srv/rootfs" {
		t.Fatalf("unexpected rootfs path: %#v", preset.Rootfs)
	}
	if preset.Rootfs.Template != "openclaw-developer" {
		t.Fatalf("unexpected rootfs template: %#v", preset.Rootfs)
	}
	if len(preset.Rootfs.RequiredCommands) != 1 || preset.Rootfs.RequiredCommands[0] != "node" {
		t.Fatalf("unexpected required commands: %#v", preset.Rootfs)
	}
	if preset.Cwd != "/workspace" {
		t.Fatalf("unexpected cwd: %#v", preset)
	}
	if preset.Hostname != "demo" || preset.Memory != "512M" || preset.Pids != 64 {
		t.Fatalf("unexpected preset runtime fields: %#v", preset)
	}
}

func TestLoadPresetFileWithNetworkPolicyReference(t *testing.T) {
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "offline.yaml")
	if err := os.WriteFile(policyPath, []byte(`networkPolicy:
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
`), 0o644); err != nil {
		t.Fatalf("write network policy file: %v", err)
	}

	presetPath := filepath.Join(dir, "preset.yaml")
	if err := os.WriteFile(presetPath, []byte(`rootfs:
  path: /srv/rootfs
networkPolicyFile: ./offline.yaml
`), 0o644); err != nil {
		t.Fatalf("write preset file: %v", err)
	}

	preset, err := LoadPresetFile(presetPath)
	if err != nil {
		t.Fatalf("LoadPresetFile returned error: %v", err)
	}

	if preset.NetworkPolicy == nil {
		t.Fatal("expected referenced network policy to be loaded")
	}
	if preset.NetworkPolicy.Egress.Rules[0].Destination.CIDR != "192.168.0.0/16" {
		t.Fatalf("expected normalized policy selector, got %#v", preset.NetworkPolicy.Egress.Rules[0].Destination)
	}
}

func TestLoadPresetFileRejectsRelativeCwd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preset.yaml")
	if err := os.WriteFile(path, []byte(`rootfs:
  path: /srv/rootfs
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
cwd: workspace
`), 0o644); err != nil {
		t.Fatalf("write preset file: %v", err)
	}

	_, err := LoadPresetFile(path)
	if err == nil || !strings.Contains(err.Error(), "cwd must be an absolute path") {
		t.Fatalf("expected relative cwd error, got %v", err)
	}
}

func TestApplyPresetFileMergesConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preset.yaml")
	if err := os.WriteFile(path, []byte(`rootfs:
  path: /srv/rootfs
  template: basic
  required_commands:
    - /bin/sh
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
roBind:
  - /host:/guest
env:
  - FOO=bar
cwd: /workspace
hostname: demo
memory: 256M
pids: 32
`), 0o644); err != nil {
		t.Fatalf("write preset file: %v", err)
	}

	cfg, preset, err := ApplyPresetFile(Config{PresetFile: path})
	if err != nil {
		t.Fatalf("ApplyPresetFile returned error: %v", err)
	}

	if cfg.RootFS != "/srv/rootfs" || cfg.Cwd != "/workspace" || cfg.Hostname != "demo" {
		t.Fatalf("unexpected merged config: %#v", cfg)
	}
	if cfg.Memory != "256M" || cfg.Pids != 32 {
		t.Fatalf("unexpected merged limits: %#v", cfg)
	}
	if len(cfg.ROBind) != 1 || cfg.ROBind[0] != "/host:/guest" {
		t.Fatalf("unexpected bind mounts: %#v", cfg)
	}
	if len(cfg.Env) != 1 || cfg.Env[0] != "FOO=bar" {
		t.Fatalf("unexpected env: %#v", cfg)
	}
	if preset.Rootfs.Template != "basic" || len(preset.Rootfs.RequiredCommands) != 1 {
		t.Fatalf("unexpected preset metadata: %#v", preset)
	}
}

func TestValidateRejectsMissingNetworkPolicy(t *testing.T) {
	err := Validate(Config{
		RootFS:  "/srv/rootfs",
		Command: []string{"/sbin/init"},
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
		Command:       []string{"/sbin/init"},
	})
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}
