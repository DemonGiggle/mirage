package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/DemonGiggle/mirage/examples"
	"github.com/DemonGiggle/mirage/internal/rootfs"
)

func TestMain(m *testing.M) {
	if err := os.Setenv("MIRAGE_TEST_SKIP_MMDEBSTRAP", "1"); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func TestPresetCommandRemoved(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{"preset", "list"}, &out, &errBuf)
	if err == nil || !strings.Contains(err.Error(), `unknown command "preset"`) {
		t.Fatalf("expected removed preset command error, got %v", err)
	}
}

func TestRootHelpMentionsCommandSurface(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	if err := Run(nil, &out, &errBuf); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	for _, needle := range []string{
		"mirage <command> [flags]",
		"rootfs          bootstrap a Debian rootfs",
		"network-policy  list bundled example network policy files",
		"package         assemble a standalone release bundle",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected root help to contain %q, got %q", needle, got)
		}
	}
}

func TestRootfsHelpIncludesSubcommands(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	if err := Run([]string{"rootfs", "--help"}, &out, &errBuf); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	for _, needle := range []string{
		"mirage rootfs <subcommand> [flags]",
		"init            bootstrap a Debian rootfs",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected rootfs help to contain %q, got %q", needle, got)
		}
	}
}

func TestRunHelpExplainsPids(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	if err := Run([]string{"run", "--help"}, &out, &errBuf); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "--pids controls the maximum number of processes/threads in the sandbox process tree") {
		t.Fatalf("expected run help to explain --pids, got %q", got)
	}
}

func TestRunHelpMentionsBindMountFlags(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	if err := Run([]string{"run", "--help"}, &out, &errBuf); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	for _, needle := range []string{
		"--ro-bind and --rw-bind accept host:guest absolute path pairs",
		"--ro-bind /home/user/project:/workspace/project",
		"--rw-bind /tmp/mirage-cache:/workspace/cache",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected run help to contain %q, got %q", needle, got)
		}
	}
}

func TestNetworkPolicyListFilesShowsExamples(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	if err := Run([]string{"network-policy", "list"}, &out, &errBuf); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	for _, needle := range []string{
		"mirage network-policy list",
		"examples/network-policies/allow-all.yaml",
		"examples/network-policies/offline.yaml",
		"examples/network-policies/block-local-egress.yaml",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected network policy list output to contain %q, got %q", needle, got)
		}
	}
}

func TestPackageHelpIncludesBundleLayout(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	if err := Run([]string{"package", "--help"}, &out, &errBuf); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	for _, needle := range []string{
		"Assemble a standalone Mirage release bundle.",
		"mirage package --output <path> [--binary <path>] [--arch <arch>] [--allow-overwrite]",
		"share/mirage/network-policies",
		"share/mirage/presets",
		"Supported --arch values: x86_64, arm64, arm32, riscv64.",
		"--allow-overwrite replaces existing package-managed files in a directory output or replaces an existing archive file.",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected package help to contain %q, got %q", needle, got)
		}
	}
}

func TestPackageCreatesDirectoryBundle(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	tempDir := t.TempDir()
	binaryPath := filepath.Join(tempDir, "mirage")
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	outputDir := filepath.Join(tempDir, "release")

	if err := Run([]string{"package", "--output", outputDir, "--binary", binaryPath}, &out, &errBuf); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	for _, relPath := range []string{
		"bin/mirage",
		"share/mirage/network-policies/offline.yaml",
		"share/mirage/presets/openclaw-offline.yaml",
	} {
		fullPath := filepath.Join(outputDir, relPath)
		if _, err := os.Stat(fullPath); err != nil {
			t.Fatalf("expected packaged file %q to exist: %v", fullPath, err)
		}
	}

	networkPolicies, err := examples.NetworkPolicyNames()
	if err != nil {
		t.Fatalf("NetworkPolicyNames returned error: %v", err)
	}
	presets, err := examples.PresetNames()
	if err != nil {
		t.Fatalf("PresetNames returned error: %v", err)
	}

	got := out.String()
	for _, needle := range []string{
		"mirage package",
		"format: dir",
		"network-policies: " + strconv.Itoa(len(networkPolicies)),
		"presets: " + strconv.Itoa(len(presets)),
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected package output to contain %q, got %q", needle, got)
		}
	}
}

func TestPackageRejectsExistingDirectoryWithoutAllowOverwrite(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	tempDir := t.TempDir()
	binaryPath := filepath.Join(tempDir, "mirage")
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	outputDir := filepath.Join(tempDir, "release")
	if err := os.MkdirAll(filepath.Join(outputDir, "stale"), 0o755); err != nil {
		t.Fatalf("create stale directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "stale", "demo.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("write stale file: %v", err)
	}

	err := Run([]string{"package", "--output", outputDir, "--binary", binaryPath}, &out, &errBuf)
	if err == nil || !strings.Contains(err.Error(), "must be empty") {
		t.Fatalf("expected existing directory rejection, got %v", err)
	}
}

func TestPackageAllowOverwritePreservesUnmanagedFiles(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	tempDir := t.TempDir()
	binaryPath := filepath.Join(tempDir, "mirage")
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	outputDir := filepath.Join(tempDir, "release")
	if err := os.MkdirAll(filepath.Join(outputDir, "bin"), 0o755); err != nil {
		t.Fatalf("create bin directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "bin", "mirage"), []byte("old-binary\n"), 0o755); err != nil {
		t.Fatalf("write existing package-managed binary: %v", err)
	}
	unmanagedPath := filepath.Join(outputDir, "FOO")
	if err := os.WriteFile(unmanagedPath, []byte("keep-me\n"), 0o644); err != nil {
		t.Fatalf("write unmanaged file: %v", err)
	}

	err := Run([]string{"package", "--output", outputDir, "--binary", binaryPath, "--allow-overwrite"}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	gotUnmanaged, err := os.ReadFile(unmanagedPath)
	if err != nil {
		t.Fatalf("read unmanaged file after overwrite: %v", err)
	}
	if string(gotUnmanaged) != "keep-me\n" {
		t.Fatalf("expected unmanaged file to be preserved, got %q", gotUnmanaged)
	}
	gotBinary, err := os.ReadFile(filepath.Join(outputDir, "bin", "mirage"))
	if err != nil {
		t.Fatalf("read packaged binary: %v", err)
	}
	if string(gotBinary) != "#!/bin/sh\n" {
		t.Fatalf("expected package-managed binary to be replaced, got %q", gotBinary)
	}
}

func TestPackageArchiveRequiresAllowOverwriteToReplaceExistingFile(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	tempDir := t.TempDir()
	binaryPath := filepath.Join(tempDir, "mirage")
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	outputPath := filepath.Join(tempDir, "mirage-release.tar.gz")
	if err := os.WriteFile(outputPath, []byte("old archive\n"), 0o644); err != nil {
		t.Fatalf("write existing archive: %v", err)
	}

	err := Run([]string{"package", "--output", outputPath, "--binary", binaryPath}, &out, &errBuf)
	if err == nil || !strings.Contains(err.Error(), "re-run with --allow-overwrite") {
		t.Fatalf("expected archive overwrite rejection, got %v", err)
	}

	err = Run([]string{"package", "--output", outputPath, "--binary", binaryPath, "--allow-overwrite"}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error with allow overwrite: %v", err)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("stat overwritten archive: %v", err)
	}
	if info.Size() == int64(len("old archive\n")) {
		t.Fatalf("expected archive file to be replaced, size remained %d", info.Size())
	}
}

func TestPackageBuildsRequestedArchitectureWhenBinaryOmitted(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	outputDir := filepath.Join(t.TempDir(), "release")
	err := Run([]string{"package", "--output", outputDir, "--arch", "arm64"}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(outputDir, "bin", "mirage")); err != nil {
		t.Fatalf("expected packaged binary to exist: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "architecture: arm64") {
		t.Fatalf("expected package output to report architecture, got %q", got)
	}
}

func TestPackageRejectsMismatchedBinaryArchitecture(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	binaryPath := buildLinuxMirageBinary(t, "x86_64")
	err := Run([]string{
		"package",
		"--output", filepath.Join(t.TempDir(), "release"),
		"--binary", binaryPath,
		"--arch", "arm64",
	}, &out, &errBuf)
	if err == nil || !strings.Contains(err.Error(), "but --arch requested arm64") {
		t.Fatalf("expected architecture mismatch error, got %v", err)
	}
}

func TestSubcommandHelpTopics(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "rootfs_init_help_topic",
			args: []string{"rootfs", "help", "init"},
			want: "Bootstrap a Debian minbase rootfs.",
		},
		{
			name: "network_policy_list_help_topic",
			args: []string{"network-policy", "help", "list"},
			want: "List bundled example network policy files.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			var errBuf bytes.Buffer

			if err := Run(tc.args, &out, &errBuf); err != nil {
				t.Fatalf("Run returned error: %v", err)
			}

			got := out.String()
			if !strings.Contains(got, tc.want) {
				t.Fatalf("expected help output to contain %q, got %q", tc.want, got)
			}
		})
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
		"note: network backend: routed policy namespace (allow loopback, host NAT uplink)",
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
		"--output", outputRoot,
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "mirage rootfs init") {
		t.Fatalf("expected rootfs init output, got %q", got)
	}
	for _, needle := range []string{
		"command: mmdebstrap",
		"command: sudo tee",
		"APT::Install-Recommends \"false\";",
		"architecture:",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected rootfs init output to contain %q, got %q", needle, got)
		}
	}
	for _, target := range []string{"bin/ls", "bin/sh", "proc", "tmp", "run", "etc/apt/apt.conf.d/99sandbox-minimal"} {
		if _, err := os.Stat(filepath.Join(outputRoot, target)); err != nil {
			t.Fatalf("expected generated target %q to exist: %v", target, err)
		}
	}
}

func TestRootfsInitAllowOverwrite(t *testing.T) {
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
		"--output", outputRoot,
		"--allow-overwrite",
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	_, err = os.ReadFile(filepath.Join(outputRoot, "etc", "demo.conf"))
	if !os.IsNotExist(err) {
		t.Fatalf("expected overwrite rebuild to remove previous file, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputRoot, "etc", "apt", "apt.conf.d", "99sandbox-minimal")); err != nil {
		t.Fatalf("expected rebuilt rootfs apt config to exist: %v", err)
	}
}

func TestRootfsInitWithSupportedArchitecture(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	outputRoot := filepath.Join(t.TempDir(), "rootfs")
	err := Run([]string{
		"rootfs",
		"init",
		"--output", outputRoot,
		"--arch", "x86_64",
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	for _, needle := range []string{
		"--architectures=amd64",
		"architecture: x86_64",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected rootfs init output to contain %q, got %q", needle, got)
		}
	}
}

func TestRootfsInitPassesDebianRelease(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	outputRoot := filepath.Join(t.TempDir(), "rootfs")
	err := Run([]string{
		"rootfs",
		"init",
		"--output", outputRoot,
		"--debian-release", "bookworm",
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if !strings.Contains(out.String(), " bookworm ") {
		t.Fatalf("expected rootfs init output to contain requested Debian release, got %q", out.String())
	}
}

func TestRootfsInitIncludesExtraPackages(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	outputRoot := filepath.Join(t.TempDir(), "rootfs")
	err := Run([]string{
		"rootfs",
		"init",
		"--output", outputRoot,
		"--extra-pkg", "jq, vim ,jq",
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "--include=apt,ca-certificates,bash,coreutils,util-linux,procps,psmisc,iproute2,curl,tar,gzip,xz-utils,git,jq,vim") {
		t.Fatalf("expected rootfs init output to contain merged include list, got %q", got)
	}
}

func TestRootfsInitRejectsInvalidArchitecture(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{
		"rootfs",
		"init",
		"--output", filepath.Join(t.TempDir(), "rootfs"),
		"--arch", "x86/64",
	}, &out, &errBuf)
	if err == nil || !strings.Contains(err.Error(), `unsupported architecture "x86/64"`) {
		t.Fatalf("expected invalid architecture error, got %v", err)
	}
}

func TestRootfsInitRejectsInvalidExtraPackage(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{
		"rootfs",
		"init",
		"--output", filepath.Join(t.TempDir(), "rootfs"),
		"--extra-pkg", "jq,bad/pkg",
	}, &out, &errBuf)
	if err == nil || !strings.Contains(err.Error(), `extra package "bad/pkg" is not a valid Debian package name`) {
		t.Fatalf("expected invalid extra package error, got %v", err)
	}
}

func TestRootfsInitRejectsWhitespaceDebianRelease(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{
		"rootfs",
		"init",
		"--output", filepath.Join(t.TempDir(), "rootfs"),
		"--debian-release", "book worm",
	}, &out, &errBuf)
	if err == nil || !strings.Contains(err.Error(), `debian release "book worm" must not contain whitespace`) {
		t.Fatalf("expected invalid Debian release error, got %v", err)
	}
}

func TestRootfsInitHelpListsSupportedArchitectures(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	if err := Run([]string{"rootfs", "init", "--help"}, &out, &errBuf); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	for _, needle := range []string{
		"The default Debian release is trixie.",
		"Supported --arch values: x86_64, arm64, arm32, riscv64.",
		"--arch <arch>",
		"--debian-release <codename>",
		"--extra-pkg <pkg1,pkg2>",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected rootfs init help to contain %q, got %q", needle, got)
		}
	}
}

func buildLinuxMirageBinary(t *testing.T, architecture string) string {
	t.Helper()

	outputPath := filepath.Join(t.TempDir(), "mirage")
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	goarch := map[string]string{
		"x86_64":  "amd64",
		"arm64":   "arm64",
		"arm32":   "arm",
		"riscv64": "riscv64",
	}[architecture]
	if goarch == "" {
		t.Fatalf("unsupported test architecture %q", architecture)
	}

	cmd := exec.Command("go", "build", "-o", outputPath, "./cmd/mirage")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+goarch)
	if architecture == "arm32" {
		cmd.Env = append(cmd.Env, "GOARM=7")
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build Linux mirage binary: %v\n%s", err, string(output))
	}
	return outputPath
}

func TestPrintGenerateWarningsIncludesGenericWarnings(t *testing.T) {
	var out bytes.Buffer
	printGenerateWarnings(&out, rootfs.GenerateReport{
		Warnings: []string{"file capability \"security.capability\" from \"/bin/ping\" could not be preserved on \"/bin/ping\""},
	}, "")

	got := out.String()
	if !strings.Contains(got, "warnings: 1") {
		t.Fatalf("expected warning count, got %q", got)
	}
	if !strings.Contains(got, `warning: file capability "security.capability" from "/bin/ping" could not be preserved on "/bin/ping"`) {
		t.Fatalf("expected generic warning text, got %q", got)
	}
}

func TestDoctorReportsNetworkPolicyInputs(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	if err := Run([]string{"doctor"}, &out, &errBuf); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "network policy inputs: --preset-file, --network-policy-file, and mirage network-policy list") {
		t.Fatalf("expected doctor output to report network policy inputs, got %q", got)
	}
}

func TestDoctorValidatesRootfsCommand(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	rootfsPath := filepath.Join(t.TempDir(), "rootfs")
	if err := rootfs.Bootstrap(rootfsPath); err != nil {
		t.Fatalf("Bootstrap returned error: %v", err)
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
	if err := rootfs.Bootstrap(rootfsPath); err != nil {
		t.Fatalf("Bootstrap returned error: %v", err)
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
	if err := rootfs.Bootstrap(rootfsPath); err != nil {
		t.Fatalf("Bootstrap returned error: %v", err)
	}

	err := Run([]string{
		"doctor",
		"--preset-file", writePresetFile(t, `rootfs:
  path: `+rootfsPath+`
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

	if !strings.Contains(err.Error(), `command "node"`) {
		t.Fatalf("expected missing preset command error, got %v", err)
	}
}
