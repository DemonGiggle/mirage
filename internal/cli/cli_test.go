package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DemonGiggle/mirage/internal/rootfs"
	"github.com/DemonGiggle/mirage/internal/spec"
)

func installBuiltInTemplate(t *testing.T, template rootfs.Template) {
	t.Helper()

	previous, existed := rootfs.BuiltInTemplates[template.Name]
	rootfs.BuiltInTemplates[template.Name] = template
	t.Cleanup(func() {
		if existed {
			rootfs.BuiltInTemplates[template.Name] = previous
			return
		}
		delete(rootfs.BuiltInTemplates, template.Name)
	})
}

func TestPresetCommandRemoved(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{"preset", "list"}, &out, &errBuf)
	if err == nil || !strings.Contains(err.Error(), `unknown command "preset"`) {
		t.Fatalf("expected removed preset command error, got %v", err)
	}
}

func writePresetFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "preset.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write preset file: %v", err)
	}
	return path
}

func writePolicyPresetFile(t *testing.T, fixtureName string) string {
	t.Helper()
	policyPath, err := filepath.Abs(filepath.Join("..", "..", "testdata", "network-policies", fixtureName))
	if err != nil {
		t.Fatalf("resolve policy fixture path: %v", err)
	}
	return writePresetFile(t, `rootfs:
  path: /srv/rootfs
networkPolicyFile: `+policyPath+`
`)
}

func TestRunDryRun(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{
		"run",
		"--rootfs", "/srv/rootfs",
		"--network-policy-file", filepath.Join("..", "..", "testdata", "network-policies", "offline.yaml"),
		"--dry-run",
		"--",
		"echo", "hello",
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "network-policy-file: ../../testdata/network-policies/offline.yaml") {
		t.Fatalf("expected dry run output to mention network policy file, got %q", got)
	}
	if !strings.Contains(got, "execution: skipped (--dry-run)") {
		t.Fatalf("expected dry run output to mention skipped execution, got %q", got)
	}
	if !strings.Contains(got, "note: execution backend: linux namespace runner") {
		t.Fatalf("expected dry run output to mention execution backend, got %q", got)
	}
	if !strings.Contains(got, "note: one sandbox = one isolated process tree") {
		t.Fatalf("expected dry run output to mention process tree model, got %q", got)
	}
}

func TestRunDryRunWithPresetFile(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	presetFile := writePresetFile(t, `rootfs:
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
description: Team preset
`)

	err := Run([]string{
		"run",
		"--preset-file", presetFile,
		"--dry-run",
		"--",
		"echo", "hello",
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "preset-file: "+presetFile) {
		t.Fatalf("expected dry run output to mention preset file, got %q", got)
	}
	if !strings.Contains(got, "rootfs: /srv/rootfs") || !strings.Contains(got, "network-policy-egress: default=deny rules=0") {
		t.Fatalf("expected dry run output to mention preset policy, got %q", got)
	}
}

func TestRunDryRunWithPolicyPresetFile(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	presetFile := writePresetFile(t, `rootfs:
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
    rules:
      - name: allow-lan
        action: allow
        destination:
          cidr: 192.168.0.0/16
        protocol: any
description: Team policy preset
`)

	err := Run([]string{
		"run",
		"--preset-file", presetFile,
		"--dry-run",
		"--",
		"echo", "hello",
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	for _, needle := range []string{
		"network-policy: v1",
		"network-policy-loopback-default: allow",
		"network-policy-egress: default=deny rules=1",
		"note: network backend: networkPolicy unsupported by current backend",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected dry run output to contain %q, got %q", needle, got)
		}
	}
}

func TestRunDryRunWithOfflinePolicyFixture(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	presetFile := writePolicyPresetFile(t, "offline.yaml")
	err := Run([]string{
		"run",
		"--preset-file", presetFile,
		"--dry-run",
		"--",
		"echo", "hello",
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	for _, needle := range []string{
		"network-policy: v1",
		"network-policy-egress: default=deny rules=0",
		"note: network backend: isolated policy namespace (allow loopback)",
		"execution: skipped (--dry-run)",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected dry run output to contain %q, got %q", needle, got)
		}
	}
}

func TestRunDryRunWithNetworkPolicyFile(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{
		"run",
		"--rootfs", "/srv/rootfs",
		"--network-policy-file", filepath.Join("..", "..", "testdata", "network-policies", "allow-all.yaml"),
		"--dry-run",
		"--",
		"echo", "hello",
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	for _, needle := range []string{
		"network-policy-file: ../../testdata/network-policies/allow-all.yaml",
		"network-policy-egress: default=allow rules=0",
		"note: network backend: allow-all policy via host namespace passthrough",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected dry run output to contain %q, got %q", needle, got)
		}
	}
}

func TestRunRejectsPresetFileConflict(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	presetFile := writePresetFile(t, `rootfs:
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
`)

	err := Run([]string{
		"run",
		"--preset-file", presetFile,
		"--network-policy-file", filepath.Join("..", "..", "testdata", "network-policies", "offline.yaml"),
		"--dry-run",
		"--",
		"echo", "hello",
	}, &out, &errBuf)
	if err == nil || !strings.Contains(err.Error(), "does not allow --network-policy-file together with --preset-file") {
		t.Fatalf("expected policy ambiguity error, got %v", err)
	}
}

func TestRunRejectsRemovedPresetFlag(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{
		"run",
		"--rootfs", "/srv/rootfs",
		"--preset", "offline",
		"--dry-run",
		"--",
		"echo", "hello",
	}, &out, &errBuf)
	if err == nil || !strings.Contains(err.Error(), "--preset has been removed") {
		t.Fatalf("expected removed preset flag error, got %v", err)
	}
}

func TestRunRejectsLegacyNetFlag(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{
		"run",
		"--rootfs", "/srv/rootfs",
		"--net", "isolated",
		"--",
		"echo", "hello",
	}, &out, &errBuf)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "flag provided but not defined: -net") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunDryRunShowsLogExport(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	stdoutLog := filepath.Join(t.TempDir(), "stdout.log")

	err := Run([]string{
		"run",
		"--rootfs", "/srv/rootfs",
		"--network-policy-file", filepath.Join("..", "..", "testdata", "network-policies", "allow-all.yaml"),
		"--stdout-log", stdoutLog,
		"--dry-run",
		"--",
		"echo", "hello",
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "stdout-log: "+stdoutLog) {
		t.Fatalf("expected stdout log path in preview, got %q", got)
	}
	if !strings.Contains(got, "note: host log export: stdout") {
		t.Fatalf("expected host log export note, got %q", got)
	}
}

func TestRootfsInit(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	outputRoot := filepath.Join(t.TempDir(), "rootfs")
	err := Run([]string{
		"rootfs",
		"init",
		"--template", "basic",
		"--output", outputRoot,
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "mirage rootfs init") || !strings.Contains(got, "template: basic") {
		t.Fatalf("expected rootfs init output, got %q", got)
	}
	for _, target := range []string{"bin/ls", "bin/sh", "proc", "tmp", "run"} {
		if _, err := os.Stat(filepath.Join(outputRoot, target)); err != nil {
			t.Fatalf("expected generated target %q to exist: %v", target, err)
		}
	}
}

func TestRootfsInitAllowOverwrite(t *testing.T) {
	const templateName = "test-allow-overwrite"
	rootfs.BuiltInTemplates[templateName] = rootfs.Template{
		Version:     rootfs.TemplateVersionV1,
		Name:        templateName,
		Description: "Test template for overwrite behavior",
		GeneratedFiles: []rootfs.GeneratedFile{
			{TargetPath: "/etc/demo.conf", Content: "updated=yes\n", Mode: 0o600},
		},
	}
	t.Cleanup(func() {
		delete(rootfs.BuiltInTemplates, templateName)
	})

	outputRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outputRoot, "etc"), 0o755); err != nil {
		t.Fatalf("create existing etc dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outputRoot, "etc", "demo.conf"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("write existing target file: %v", err)
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	err := Run([]string{
		"rootfs",
		"init",
		"--template", templateName,
		"--output", outputRoot,
		"--allow-overwrite",
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outputRoot, "etc", "demo.conf"))
	if err != nil {
		t.Fatalf("read overwritten file: %v", err)
	}
	if string(data) != "updated=yes\n" {
		t.Fatalf("expected overwritten file content, got %q", string(data))
	}
}

func TestRootfsInitReportsMissingAssets(t *testing.T) {
	const templateName = "test-missing-assets"
	installBuiltInTemplate(t, rootfs.Template{
		Version:     rootfs.TemplateVersionV1,
		Name:        templateName,
		Description: "Test template for missing host assets",
		Directories: []rootfs.Directory{{Path: "/work", Mode: 0o755}},
		RuntimeFiles: []rootfs.RuntimeFile{
			{
				HostPath:   filepath.Join(t.TempDir(), "missing-hosts"),
				TargetPath: "/etc/hosts",
			},
		},
	})

	var out bytes.Buffer
	var errBuf bytes.Buffer
	outputRoot := filepath.Join(t.TempDir(), "rootfs")
	err := Run([]string{
		"rootfs",
		"init",
		"--template", templateName,
		"--output", outputRoot,
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "warnings: 1") {
		t.Fatalf("expected rootfs init output to include warning count, got %q", got)
	}
	if !strings.Contains(got, `warning: missing host asset "`) || !strings.Contains(got, `for "/etc/hosts"`) {
		t.Fatalf("expected rootfs init output to report missing asset, got %q", got)
	}
}

func TestEnsurePresetRootfsAutoGeneratesTemplate(t *testing.T) {
	const templateName = "test-preset-template"
	installBuiltInTemplate(t, rootfs.Template{
		Version:     rootfs.TemplateVersionV1,
		Name:        templateName,
		Description: "Test template for preset-generated rootfs",
		Directories: []rootfs.Directory{{Path: "/workspace", Mode: 0o755}},
		GeneratedFiles: []rootfs.GeneratedFile{
			{TargetPath: "/etc/demo.conf", Content: "demo=yes\n", Mode: 0o644},
		},
	})

	rootfsPath := filepath.Join(t.TempDir(), "preset-rootfs")
	cfg := spec.Config{
		RootFS:     rootfsPath,
		PresetFile: "preset.yaml",
	}
	preset := spec.Preset{
		Rootfs: spec.RootfsPreset{Template: templateName},
	}

	var errBuf bytes.Buffer
	if err := ensurePresetRootfs(cfg, preset, &errBuf); err != nil {
		t.Fatalf("ensurePresetRootfs returned error: %v", err)
	}

	for _, target := range []string{"workspace", "etc/demo.conf"} {
		if _, err := os.Stat(filepath.Join(rootfsPath, target)); err != nil {
			t.Fatalf("expected generated target %q to exist: %v", target, err)
		}
	}
}

func TestEnsurePresetRootfsReportsMissingAssets(t *testing.T) {
	const templateName = "test-preset-missing-assets"
	installBuiltInTemplate(t, rootfs.Template{
		Version:     rootfs.TemplateVersionV1,
		Name:        templateName,
		Description: "Test template for preset-generated missing assets",
		Directories: []rootfs.Directory{{Path: "/work", Mode: 0o755}},
		RuntimeFiles: []rootfs.RuntimeFile{
			{
				HostPath:   filepath.Join(t.TempDir(), "missing-hosts"),
				TargetPath: "/etc/hosts",
			},
		},
	})

	rootfsPath := filepath.Join(t.TempDir(), "preset-rootfs")
	cfg := spec.Config{
		RootFS:     rootfsPath,
		PresetFile: "preset.yaml",
	}
	preset := spec.Preset{
		Rootfs: spec.RootfsPreset{Template: templateName},
	}

	var errBuf bytes.Buffer
	if err := ensurePresetRootfs(cfg, preset, &errBuf); err != nil {
		t.Fatalf("ensurePresetRootfs returned error: %v", err)
	}

	got := errBuf.String()
	if !strings.Contains(got, "warnings: 1") {
		t.Fatalf("expected preset rootfs warning count, got %q", got)
	}
	if !strings.Contains(got, "warning: preset rootfs missing host asset") {
		t.Fatalf("expected preset rootfs warning output, got %q", got)
	}
}

func TestDoctorReportsNetworkPolicyInputs(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	if err := Run([]string{"doctor"}, &out, &errBuf); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "network policy config: available via --preset-file and --network-policy-file") {
		t.Fatalf("expected doctor output to report network policy inputs, got %q", got)
	}
}

func TestDoctorValidatesRootfsCommand(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	rootfsPath := filepath.Join(t.TempDir(), "rootfs")
	template, ok := rootfs.LookupTemplate("basic")
	if !ok {
		t.Fatal("expected basic template to exist")
	}
	if err := rootfs.Generate(rootfsPath, template); err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	err := Run([]string{
		"doctor",
		"--rootfs", rootfsPath,
		"--command", "/bin/ls",
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	for _, needle := range []string{
		"resolved command: /bin/ls",
		"rootfs validation: ok",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected doctor output to contain %q, got %q", needle, got)
		}
	}
}

func TestDoctorPrintsDeduplicatedPresetCommands(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	rootfsPath := filepath.Join(t.TempDir(), "rootfs")
	template, ok := rootfs.LookupTemplate("basic")
	if !ok {
		t.Fatal("expected basic template to exist")
	}
	if err := rootfs.Generate(rootfsPath, template); err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	presetFile := writePresetFile(t, `rootfs:
  path: `+rootfsPath+`
  required_commands:
    - " /bin/ls "
    - /bin/sh
    - /bin/ls
    - ""
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
description: Basic rootfs validation preset.
`)

	err := Run([]string{
		"doctor",
		"--preset-file", presetFile,
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "- preset required rootfs commands: /bin/ls, /bin/sh") {
		t.Fatalf("expected deduplicated preset command list, got %q", got)
	}
	if strings.Count(got, "- preset required command /bin/ls: ok") != 1 {
		t.Fatalf("expected /bin/ls to be validated once, got %q", got)
	}
}

func TestDoctorUsesPresetRootfsRequirements(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	rootfsPath := filepath.Join(t.TempDir(), "rootfs")
	template, ok := rootfs.LookupTemplate("basic")
	if !ok {
		t.Fatal("expected basic template to exist")
	}
	if err := rootfs.Generate(rootfsPath, template); err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	err := Run([]string{
		"doctor",
		"--preset-file", writePresetFile(t, `rootfs:
  path: `+rootfsPath+`
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
`),
	}, &out, &errBuf)
	if err == nil {
		t.Fatal("expected preset rootfs validation to fail on missing node")
	}

	got := out.String()
	if !strings.Contains(got, "preset rootfs template: openclaw-developer") {
		t.Fatalf("expected preset template hint, got %q", got)
	}
	if !strings.Contains(err.Error(), `command "node"`) {
		t.Fatalf("expected missing preset command error, got %v", err)
	}
}
