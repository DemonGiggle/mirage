package rootfs

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var errRecursiveSymlink = errors.New("recursive symlink")

func TestMain(m *testing.M) {
	if err := os.Setenv(testSkipBootstrapEnv, "1"); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func TestParseLDDOutput(t *testing.T) {
	output := []byte(`
linux-vdso.so.1 (0x00007ffd0d1f0000)
libselinux.so.1 => /lib/x86_64-linux-gnu/libselinux.so.1 (0x00007f96c3a9f000)
libc.so.6 => /lib/x86_64-linux-gnu/libc.so.6 (0x00007f96c38be000)
/lib64/ld-linux-x86-64.so.2 (0x00007f96c3b0e000)
`)

	got, err := parseLDDOutput(output)
	if err != nil {
		t.Fatalf("parseLDDOutput returned error: %v", err)
	}
	if len(got.missing) != 0 {
		t.Fatalf("expected no missing dependencies, got %v", got.missing)
	}
	want := []string{
		"/lib/x86_64-linux-gnu/libselinux.so.1",
		"/lib/x86_64-linux-gnu/libc.so.6",
		"/lib64/ld-linux-x86-64.so.2",
	}
	if len(got.paths) != len(want) {
		t.Fatalf("unexpected dependency count: got %d want %d (%v)", len(got.paths), len(want), got.paths)
	}
	for idx, dep := range want {
		if got.paths[idx] != dep {
			t.Fatalf("unexpected dependency at %d: got %q want %q", idx, got.paths[idx], dep)
		}
	}
}

func TestParseLDDOutputCollectsMissingDependency(t *testing.T) {
	got, err := parseLDDOutput([]byte("libmissing.so.1 => not found"))
	if err != nil {
		t.Fatalf("parseLDDOutput returned error: %v", err)
	}
	if len(got.paths) != 0 {
		t.Fatalf("expected no resolved dependencies, got %v", got.paths)
	}
	if len(got.missing) != 1 || got.missing[0] != "libmissing.so.1" {
		t.Fatalf("expected missing dependency to be collected, got %v", got.missing)
	}
}

func TestBootstrapRequiresRootOutsideTestBypass(t *testing.T) {
	if err := os.Unsetenv(testSkipBootstrapEnv); err != nil {
		t.Fatalf("unset test skip env: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Setenv(testSkipBootstrapEnv, "1"); err != nil {
			t.Fatalf("restore test skip env: %v", err)
		}
	})

	previous := currentEUID
	currentEUID = func() int { return 1000 }
	t.Cleanup(func() {
		currentEUID = previous
	})

	_, err := BootstrapWithReport(filepath.Join(t.TempDir(), "rootfs"))
	if err == nil || !strings.Contains(err.Error(), "rootfs init requires root privileges") {
		t.Fatalf("expected root privilege error, got %v", err)
	}
}

func TestBootstrapRejectsEmptyOutputRoot(t *testing.T) {
	_, err := BootstrapWithReport("")
	if err == nil || !strings.Contains(err.Error(), "output rootfs path cannot be empty") {
		t.Fatalf("expected empty output root error, got %v", err)
	}
}

func TestBootstrapRejectsCriticalSystemDirectory(t *testing.T) {
	_, err := BootstrapWithReport("/tmp")
	if err == nil || !strings.Contains(err.Error(), `refusing to use critical system directory "/tmp" as rootfs`) {
		t.Fatalf("expected critical directory rejection, got %v", err)
	}
}

func TestClearDirectoryRejectsNestedMounts(t *testing.T) {
	root := filepath.Join(t.TempDir(), "rootfs")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("create rootfs: %v", err)
	}

	previous := readMountInfo
	readMountInfo = func(string) ([]byte, error) {
		return []byte("24 23 0:22 / " + root + " rw - ext4 /dev/root rw\n25 24 0:23 / " + filepath.Join(root, "proc") + " rw - proc proc rw\n"), nil
	}
	t.Cleanup(func() {
		readMountInfo = previous
	})

	err := clearDirectory(root)
	if err == nil || !strings.Contains(err.Error(), "contains active mount points") {
		t.Fatalf("expected active mount rejection, got %v", err)
	}
}

func TestBootstrapCapturesMmdebstrapStderrWithoutLogOutput(t *testing.T) {
	if err := os.Unsetenv(testSkipBootstrapEnv); err != nil {
		t.Fatalf("unset test skip env: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Setenv(testSkipBootstrapEnv, "1"); err != nil {
			t.Fatalf("restore test skip env: %v", err)
		}
	})

	previousEUID := currentEUID
	currentEUID = func() int { return 0 }
	t.Cleanup(func() {
		currentEUID = previousEUID
	})

	tempDir := t.TempDir()
	fakeMMDebstrap := filepath.Join(tempDir, "mmdebstrap")
	script := "#!/bin/sh\nprintf 'boom stderr\\n' >&2\nexit 1\n"
	if err := os.WriteFile(fakeMMDebstrap, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake mmdebstrap: %v", err)
	}

	previousPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", tempDir+string(os.PathListSeparator)+previousPath); err != nil {
		t.Fatalf("set PATH: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Setenv("PATH", previousPath); err != nil {
			t.Fatalf("restore PATH: %v", err)
		}
	})

	_, err := BootstrapWithReport(filepath.Join(tempDir, "rootfs"))
	if err == nil || !strings.Contains(err.Error(), "boom stderr") {
		t.Fatalf("expected stderr in bootstrap error, got %v", err)
	}
}

func TestNormalizeRootfsArchitectureAcceptsSupportedValues(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{input: "x86_64", want: "x86_64"},
		{input: "arm64", want: "arm64"},
		{input: "arm32", want: "arm32"},
		{input: "riscv64", want: "riscv64"},
	} {
		got, err := normalizeRootfsArchitecture(tc.input)
		if err != nil {
			t.Fatalf("normalizeRootfsArchitecture(%q) returned error: %v", tc.input, err)
		}
		if got != tc.want {
			t.Fatalf("normalizeRootfsArchitecture(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestNormalizeRootfsArchitectureRejectsAliasesAndInvalidValue(t *testing.T) {
	for _, input := range []string{"amd64", "aarch64", "x86/64"} {
		_, err := normalizeRootfsArchitecture(input)
		if err == nil || !strings.Contains(err.Error(), "unsupported architecture") {
			t.Fatalf("expected invalid architecture error for %q, got %v", input, err)
		}
	}
}

func TestDebianArchitectureForRootfsArchMapsSupportedValues(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{input: "x86_64", want: "amd64"},
		{input: "arm64", want: "arm64"},
		{input: "arm32", want: "armhf"},
		{input: "riscv64", want: "riscv64"},
	} {
		got, err := debianArchitectureForRootfsArch(tc.input)
		if err != nil {
			t.Fatalf("debianArchitectureForRootfsArch(%q) returned error: %v", tc.input, err)
		}
		if got != tc.want {
			t.Fatalf("debianArchitectureForRootfsArch(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestResolveRootfsArchitectureUsesHostDetectorByDefault(t *testing.T) {
	previous := detectBootstrapHostArchitecture
	detectBootstrapHostArchitecture = func() (string, error) { return "arm64", nil }
	t.Cleanup(func() {
		detectBootstrapHostArchitecture = previous
	})

	got, err := resolveRootfsArchitecture("")
	if err != nil {
		t.Fatalf("resolveRootfsArchitecture returned error: %v", err)
	}
	if got != "arm64" {
		t.Fatalf("resolveRootfsArchitecture() = %q, want %q", got, "arm64")
	}
}

func TestBootstrapLogsDebianArchitectureForSelectedRootfsArch(t *testing.T) {
	var out bytes.Buffer
	root := filepath.Join(t.TempDir(), "rootfs")

	report, err := BootstrapWithReportWithOptions(root, GenerateOptions{
		Architecture: "x86_64",
		LogOutput:    &out,
	})
	if err != nil {
		t.Fatalf("BootstrapWithReportWithOptions returned error: %v", err)
	}
	if report.Architecture != "x86_64" {
		t.Fatalf("report.Architecture = %q, want %q", report.Architecture, "x86_64")
	}
	if !strings.Contains(out.String(), "--architectures=amd64") {
		t.Fatalf("expected bootstrap log to contain Debian architecture flag, got %q", out.String())
	}
}

func TestDefaultDetectBootstrapHostArchitectureMapsDpkgArchitecture(t *testing.T) {
	previousPath := os.Getenv("PATH")
	tempDir := t.TempDir()
	fakeDPKG := filepath.Join(tempDir, "dpkg")
	if err := os.WriteFile(fakeDPKG, []byte("#!/bin/sh\necho armhf\n"), 0o755); err != nil {
		t.Fatalf("write fake dpkg: %v", err)
	}
	if err := os.Setenv("PATH", tempDir+string(os.PathListSeparator)+previousPath); err != nil {
		t.Fatalf("set PATH: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Setenv("PATH", previousPath); err != nil {
			t.Fatalf("restore PATH: %v", err)
		}
	})

	got, err := defaultDetectBootstrapHostArchitecture()
	if err != nil {
		t.Fatalf("defaultDetectBootstrapHostArchitecture returned error: %v", err)
	}
	if got != "arm32" {
		t.Fatalf("defaultDetectBootstrapHostArchitecture() = %q, want %q", got, "arm32")
	}
}

func TestResolveRootfsArchitectureRejectsInvalidValue(t *testing.T) {
	_, err := resolveRootfsArchitecture("x86/64")
	if err == nil || !strings.Contains(err.Error(), "unsupported architecture") {
		t.Fatalf("expected invalid architecture error, got %v", err)
	}
}

func TestGenerateCopiesFilesAndDependencies(t *testing.T) {
	shellPath, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("host PATH does not contain sh")
	}

	runtimeDir := t.TempDir()
	runtimeSource := filepath.Join(runtimeDir, "hosts")
	if err := os.WriteFile(runtimeSource, []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
		t.Fatalf("write runtime source: %v", err)
	}

	template := Template{
		Version:     TemplateVersionV1,
		Name:        "custom",
		Description: "Custom template",
		Directories: []Directory{{Path: "/work", Mode: 0o755}},
		Binaries: []Binary{
			{
				HostPath:         shellPath,
				TargetPath:       "/bin/sh",
				CopyDependencies: true,
			},
		},
		RuntimeFiles: []RuntimeFile{
			{
				HostPath:   runtimeSource,
				TargetPath: "/etc/hosts",
			},
		},
	}

	outputRoot := filepath.Join(t.TempDir(), "rootfs")
	if err := Generate(outputRoot, template); err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	for _, target := range []string{
		"/work",
		"/bin/sh",
		"/etc/hosts",
	} {
		if _, err := os.Stat(filepath.Join(outputRoot, strings.TrimPrefix(target, "/"))); err != nil {
			t.Fatalf("expected generated target %q to exist: %v", target, err)
		}
	}

	dependencies, err := lddDependencies(shellPath)
	if err != nil {
		t.Fatalf("lddDependencies returned error: %v", err)
	}
	if len(dependencies) == 0 {
		t.Fatalf("expected %q to have at least one dynamic dependency", shellPath)
	}
	firstDependency := filepath.Join(outputRoot, strings.TrimPrefix(dependencies[0], "/"))
	if _, err := os.Stat(firstDependency); err != nil {
		t.Fatalf("expected copied dependency %q to exist: %v", firstDependency, err)
	}
}

func TestGeneratePreservesMergedUsrLibrarySymlinkAncestors(t *testing.T) {
	shellPath, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("host PATH does not contain sh")
	}

	libInfo, err := os.Lstat("/lib")
	if err != nil {
		t.Fatalf("lstat /lib: %v", err)
	}
	lib64Info, err := os.Lstat("/lib64")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.Skip("host does not expose /lib64")
		}
		t.Fatalf("lstat /lib64: %v", err)
	}
	if libInfo.Mode()&os.ModeSymlink == 0 || lib64Info.Mode()&os.ModeSymlink == 0 {
		t.Skip("host is not using merged-usr library symlinks")
	}

	outputRoot := filepath.Join(t.TempDir(), "rootfs")
	template := Template{
		Version:     TemplateVersionV1,
		Name:        "custom",
		Description: "Custom template",
		Binaries: []Binary{
			{
				HostPath:         shellPath,
				TargetPath:       "/bin/sh",
				CopyDependencies: true,
			},
		},
	}

	if err := Generate(outputRoot, template); err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	for _, path := range []string{"/lib", "/lib64"} {
		info, err := os.Lstat(filepath.Join(outputRoot, strings.TrimPrefix(path, "/")))
		if err != nil {
			t.Fatalf("lstat generated %q: %v", path, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("expected generated %q to remain a symlink", path)
		}
	}
}

func TestGeneratePreservesMergedUsrTargetSymlinkAncestors(t *testing.T) {
	shellPath, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("host PATH does not contain sh")
	}

	binInfo, err := os.Lstat("/bin")
	if err != nil {
		t.Fatalf("lstat /bin: %v", err)
	}
	if binInfo.Mode()&os.ModeSymlink == 0 {
		t.Skip("host /bin is not a symlink")
	}

	outputRoot := filepath.Join(t.TempDir(), "rootfs")
	template := Template{
		Version:     TemplateVersionV1,
		Name:        "custom",
		Description: "Custom template",
		Binaries: []Binary{
			{
				HostPath:         shellPath,
				TargetPath:       "/bin/sh",
				CopyDependencies: true,
			},
		},
	}

	if err := Generate(outputRoot, template); err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	info, err := os.Lstat(filepath.Join(outputRoot, "bin"))
	if err != nil {
		t.Fatalf("lstat generated /bin: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected generated /bin to remain a symlink")
	}
}

func TestGenerateWithReportReportsMissingAssetsWithoutFailing(t *testing.T) {
	scriptPath := filepath.Join(t.TempDir(), "demo-script")
	if err := os.WriteFile(scriptPath, []byte("#!/definitely/missing/interpreter\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write script fixture: %v", err)
	}

	missingRuntimeFile := filepath.Join(t.TempDir(), "missing-hosts")
	outputRoot := filepath.Join(t.TempDir(), "rootfs")
	report, err := GenerateWithReport(outputRoot, Template{
		Version:     TemplateVersionV1,
		Name:        "custom",
		Description: "Custom template",
		Directories: []Directory{{Path: "/work", Mode: 0o755}},
		Binaries: []Binary{
			{
				HostPath:         scriptPath,
				TargetPath:       "/usr/bin/demo-script",
				CopyDependencies: true,
			},
		},
		RuntimeFiles: []RuntimeFile{
			{
				HostPath:   missingRuntimeFile,
				TargetPath: "/etc/hosts",
			},
		},
	})
	if err != nil {
		t.Fatalf("GenerateWithReport returned error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(outputRoot, "work")); err != nil {
		t.Fatalf("expected generated directory to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputRoot, "usr", "bin", "demo-script")); err != nil {
		t.Fatalf("expected copied script to exist: %v", err)
	}
	if len(report.MissingAssets) != 2 {
		t.Fatalf("expected two missing assets, got %d (%v)", len(report.MissingAssets), report.MissingAssets)
	}

	var sawRuntimeFile bool
	var sawInterpreter bool
	for _, asset := range report.MissingAssets {
		switch {
		case asset.Source == missingRuntimeFile && asset.TargetPath == "/etc/hosts":
			sawRuntimeFile = true
		case asset.Source == "/definitely/missing/interpreter" && asset.TargetPath == "/definitely/missing/interpreter":
			sawInterpreter = true
		}
	}
	if !sawRuntimeFile {
		t.Fatalf("expected missing runtime file to be reported, got %v", report.MissingAssets)
	}
	if !sawInterpreter {
		t.Fatalf("expected missing shebang interpreter to be reported, got %v", report.MissingAssets)
	}
}

func TestGenerateWithReportSkipsOptionalMissingAssets(t *testing.T) {
	outputRoot := filepath.Join(t.TempDir(), "rootfs")
	report, err := GenerateWithReport(outputRoot, Template{
		Version:     TemplateVersionV1,
		Name:        "custom",
		Description: "Custom template",
		RuntimeFiles: []RuntimeFile{
			{
				HostPath:   filepath.Join(t.TempDir(), "missing-hosts"),
				TargetPath: "/etc/hosts",
				Optional:   true,
			},
		},
		RuntimeTrees: []RuntimeTree{
			{
				HostPath:   filepath.Join(t.TempDir(), "missing-locale"),
				TargetPath: "/usr/share/locale",
				Optional:   true,
			},
		},
	})
	if err != nil {
		t.Fatalf("GenerateWithReport returned error: %v", err)
	}
	if len(report.MissingAssets) != 0 {
		t.Fatalf("expected optional missing assets to stay silent, got %v", report.MissingAssets)
	}
}

func TestGenerateWithReportSkipsOptionalMissingBinaryLookup(t *testing.T) {
	outputRoot := filepath.Join(t.TempDir(), "rootfs")
	report, err := GenerateWithReport(outputRoot, Template{
		Version:     TemplateVersionV1,
		Name:        "custom",
		Description: "Custom template",
		Binaries: []Binary{
			{
				LookupName:       "definitely-missing-binary",
				TargetPath:       "/usr/bin/demo",
				CopyDependencies: true,
				Optional:         true,
			},
		},
	})
	if err != nil {
		t.Fatalf("GenerateWithReport returned error: %v", err)
	}
	if len(report.MissingAssets) != 0 {
		t.Fatalf("expected optional missing binary lookup to stay silent, got %v", report.MissingAssets)
	}
	if _, err := os.Stat(filepath.Join(outputRoot, "usr", "bin", "demo")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected optional missing binary to be skipped, stat err=%v", err)
	}
}

func TestGenerateWithReportSkipsOptionalBinaryWithMissingDependencies(t *testing.T) {
	hostPath, err := exec.LookPath("host")
	if err != nil {
		t.Skip("host PATH does not contain host")
	}
	lddReport, err := lddDependencyReport(hostPath)
	if err != nil {
		t.Skipf("host binary ldd inspection failed: %v", err)
	}
	if len(lddReport.missing) == 0 {
		t.Skip("host binary does not have missing shared library dependencies")
	}

	outputRoot := filepath.Join(t.TempDir(), "rootfs")
	report, err := GenerateWithReport(outputRoot, Template{
		Version:     TemplateVersionV1,
		Name:        "custom",
		Description: "Custom template",
		Binaries: []Binary{
			{
				HostPath:         hostPath,
				TargetPath:       "/usr/bin/host",
				CopyDependencies: true,
				Optional:         true,
			},
		},
	})
	if err != nil {
		t.Fatalf("GenerateWithReport returned error: %v", err)
	}
	if len(report.MissingAssets) != 0 {
		t.Fatalf("expected optional binary with missing dependencies to stay silent, got %v", report.MissingAssets)
	}
	if _, err := os.Stat(filepath.Join(outputRoot, "usr", "bin", "host")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected optional binary with missing dependencies to be skipped, stat err=%v", err)
	}
}

func TestBinaryCopyAvailableSkipsCircularShebangs(t *testing.T) {
	scriptPath := filepath.Join(t.TempDir(), "loop")
	if err := os.WriteFile(scriptPath, []byte("#!"+scriptPath+"\n"), 0o755); err != nil {
		t.Fatalf("write script fixture: %v", err)
	}

	g := generator{}
	available, err := g.binaryCopyAvailable(scriptPath, "/usr/bin/loop", true)
	if err != nil {
		t.Fatalf("binaryCopyAvailable returned error: %v", err)
	}
	if available {
		t.Fatal("expected circular shebang script to be unavailable")
	}
}

func TestCopyHostBinaryRejectsCircularShebangs(t *testing.T) {
	scriptPath := filepath.Join(t.TempDir(), "loop")
	if err := os.WriteFile(scriptPath, []byte("#!"+scriptPath+"\n"), 0o755); err != nil {
		t.Fatalf("write script fixture: %v", err)
	}

	g := generator{
		outputRoot:      filepath.Join(t.TempDir(), "rootfs"),
		copiedTargets:   make(map[string]struct{}),
		copiedTrees:     make(map[string]struct{}),
		missingReported: make(map[string]struct{}),
		shebangCache:    make(map[string]shebangCacheEntry),
		lddCache:        make(map[string]lddCacheEntry),
	}
	if err := os.MkdirAll(g.outputRoot, 0o755); err != nil {
		t.Fatalf("create output root: %v", err)
	}

	err := g.copyHostBinary(scriptPath, "/usr/bin/loop", true)
	if err == nil || !strings.Contains(err.Error(), "circular shebang dependency") {
		t.Fatalf("expected circular shebang error, got %v", err)
	}
}

func TestGenerateCopiesNSSModulesReferencedByNSSwitch(t *testing.T) {
	required := []string{"files", "dns"}
	for _, module := range required {
		if _, err := resolveNSSModuleSupportPaths(module); err != nil {
			t.Skipf("host NSS module %q unavailable: %v", module, err)
		}
	}

	runtimeDir := t.TempDir()
	nsswitchSource := filepath.Join(runtimeDir, "nsswitch.conf")
	if err := os.WriteFile(nsswitchSource, []byte("hosts: files dns\n"), 0o644); err != nil {
		t.Fatalf("write nsswitch source: %v", err)
	}

	outputRoot := filepath.Join(t.TempDir(), "rootfs")
	err := Generate(outputRoot, Template{
		Version:     TemplateVersionV1,
		Name:        "custom",
		Description: "Custom template",
		RuntimeFiles: []RuntimeFile{
			{
				HostPath:   nsswitchSource,
				TargetPath: "/etc/nsswitch.conf",
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	for _, module := range required {
		paths, err := resolveNSSModuleSupportPaths(module)
		if err != nil {
			t.Fatalf("resolveNSSModuleSupportPaths(%q) returned error: %v", module, err)
		}
		for _, path := range paths {
			if _, err := os.Stat(filepath.Join(outputRoot, strings.TrimPrefix(path, "/"))); err != nil {
				t.Fatalf("expected generated NSS support path %q to exist: %v", path, err)
			}
		}
	}
}

func TestGenerateHonorsCopyDependenciesFlag(t *testing.T) {
	bootstrapRoot := filepath.Join(t.TempDir(), "bootstrap-rootfs")
	if err := Bootstrap(bootstrapRoot); err != nil {
		t.Fatalf("Bootstrap returned error: %v", err)
	}

	var binaryPath string
	var uniqueDependency string
	for _, candidate := range []string{"python3", "git", "tar", "find", "env"} {
		resolved, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		dependencies, err := lddDependencies(resolved)
		if err != nil || len(dependencies) == 0 {
			continue
		}
		for _, dep := range dependencies {
			if _, err := os.Stat(filepath.Join(bootstrapRoot, strings.TrimPrefix(dep, "/"))); errors.Is(err, os.ErrNotExist) {
				binaryPath = resolved
				uniqueDependency = dep
				break
			}
		}
		if binaryPath != "" {
			break
		}
	}
	if binaryPath == "" {
		t.Skip("could not find host binary with a dependency absent from the bootstrapped test rootfs")
	}

	outputRoot := filepath.Join(t.TempDir(), "rootfs")
	err := Generate(outputRoot, Template{
		Version:     TemplateVersionV1,
		Name:        "custom",
		Description: "Custom template",
		Binaries: []Binary{
			{
				HostPath:         binaryPath,
				TargetPath:       "/bin/custom",
				CopyDependencies: false,
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(outputRoot, "bin", "custom")); err != nil {
		t.Fatalf("expected copied binary to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputRoot, strings.TrimPrefix(uniqueDependency, "/"))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected dependency %q to be skipped when copy_dependencies is false, stat err=%v", uniqueDependency, err)
	}
}

func TestGenerateRejectsNonEmptyOutputRoot(t *testing.T) {
	outputRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(outputRoot, "existing"), []byte("data"), 0o644); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	err := Generate(outputRoot, Template{
		Version:     TemplateVersionV1,
		Name:        "custom",
		Description: "Custom template",
	})
	if err == nil || !strings.Contains(err.Error(), "already exists and is not empty") {
		t.Fatalf("expected non-empty output root rejection, got %v", err)
	}
}

func TestGenerateWithAllowOverwriteClearsNonEmptyOutputRoot(t *testing.T) {
	outputRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outputRoot, "etc"), 0o755); err != nil {
		t.Fatalf("create existing etc dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outputRoot, "etc", "demo.conf"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("write existing target file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outputRoot, "keep.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatalf("write unrelated file: %v", err)
	}

	err := GenerateWithOptions(outputRoot, Template{
		Version:     TemplateVersionV1,
		Name:        "custom",
		Description: "Custom template",
		GeneratedFiles: []GeneratedFile{
			{TargetPath: "/etc/demo.conf", Content: "new=yes\n", Mode: 0o600},
		},
	}, GenerateOptions{AllowOverwrite: true})
	if err != nil {
		t.Fatalf("GenerateWithOptions returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outputRoot, "etc", "demo.conf"))
	if err != nil {
		t.Fatalf("read overwritten file: %v", err)
	}
	if string(data) != "new=yes\n" {
		t.Fatalf("expected overwritten file content, got %q", string(data))
	}
	if _, err := os.Stat(filepath.Join(outputRoot, "keep.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected unrelated file to be removed during overwrite bootstrap, got %v", err)
	}
}

func TestGenerateWithAllowOverwriteReplacesFileWithDirectory(t *testing.T) {
	outputRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(outputRoot, "workspace"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("write existing conflicting file: %v", err)
	}

	err := GenerateWithOptions(outputRoot, Template{
		Version:     TemplateVersionV1,
		Name:        "custom",
		Description: "Custom template",
		Directories: []Directory{{Path: "/workspace", Mode: 0o755}},
	}, GenerateOptions{AllowOverwrite: true})
	if err != nil {
		t.Fatalf("GenerateWithOptions returned error: %v", err)
	}

	info, err := os.Stat(filepath.Join(outputRoot, "workspace"))
	if err != nil {
		t.Fatalf("stat workspace directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected workspace to be a directory, got mode %v", info.Mode())
	}
}

func TestGenerateWithAllowOverwriteReplacesDirectoryAtFileTarget(t *testing.T) {
	outputRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outputRoot, "etc", "demo.conf"), 0o755); err != nil {
		t.Fatalf("create existing conflicting directory: %v", err)
	}

	err := GenerateWithOptions(outputRoot, Template{
		Version:     TemplateVersionV1,
		Name:        "custom",
		Description: "Custom template",
		GeneratedFiles: []GeneratedFile{
			{TargetPath: "/etc/demo.conf", Content: "new=yes\n", Mode: 0o600},
		},
	}, GenerateOptions{AllowOverwrite: true})
	if err != nil {
		t.Fatalf("GenerateWithOptions returned error: %v", err)
	}

	info, err := os.Stat(filepath.Join(outputRoot, "etc", "demo.conf"))
	if err != nil {
		t.Fatalf("stat replaced file target: %v", err)
	}
	if info.IsDir() {
		t.Fatalf("expected file target to replace existing directory")
	}
}

func TestGenerateWritesGeneratedFiles(t *testing.T) {
	outputRoot := filepath.Join(t.TempDir(), "rootfs")
	err := Generate(outputRoot, Template{
		Version:     TemplateVersionV1,
		Name:        "custom",
		Description: "Custom template",
		GeneratedFiles: []GeneratedFile{
			{TargetPath: "/etc/machine-id", Content: "", Mode: 0o644},
			{TargetPath: "/etc/demo.conf", Content: "demo=yes\n", Mode: 0o600},
		},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	for target, wantMode := range map[string]os.FileMode{
		filepath.Join(outputRoot, "etc", "machine-id"): 0o644,
		filepath.Join(outputRoot, "etc", "demo.conf"):  0o600,
	} {
		info, err := os.Stat(target)
		if err != nil {
			t.Fatalf("expected generated file %q to exist: %v", target, err)
		}
		if info.Mode().Perm() != wantMode {
			t.Fatalf("expected generated file %q to have mode %o, got %o", target, wantMode, info.Mode().Perm())
		}
	}
}

func TestGenerateCopiesRuntimeTrees(t *testing.T) {
	hostRoot := filepath.Join(t.TempDir(), "host")
	if err := os.MkdirAll(filepath.Join(hostRoot, "locale", "C"), 0o755); err != nil {
		t.Fatalf("create runtime tree dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hostRoot, "locale", "C", "messages.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write runtime tree file: %v", err)
	}

	outputRoot := filepath.Join(t.TempDir(), "rootfs")
	err := Generate(outputRoot, Template{
		Version:     TemplateVersionV1,
		Name:        "custom",
		Description: "Custom template",
		RuntimeTrees: []RuntimeTree{
			{HostPath: filepath.Join(hostRoot, "locale"), TargetPath: "/usr/share/locale"},
		},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	target := filepath.Join(outputRoot, "usr", "share", "locale", "C", "messages.txt")
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("expected copied runtime tree file %q to exist: %v", target, err)
	}
	if string(data) != "hello\n" {
		t.Fatalf("unexpected copied runtime tree content: %q", string(data))
	}
}

func TestGenerateCopiesRuntimeTreesFromSymlinkRoot(t *testing.T) {
	hostRoot := filepath.Join(t.TempDir(), "host")
	sourceRoot := filepath.Join(hostRoot, "locale")
	if err := os.MkdirAll(filepath.Join(sourceRoot, "C"), 0o755); err != nil {
		t.Fatalf("create runtime tree dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "C", "messages.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write runtime tree file: %v", err)
	}

	linkRoot := filepath.Join(t.TempDir(), "locale-link")
	if err := os.Symlink(sourceRoot, linkRoot); err != nil {
		t.Fatalf("create runtime tree symlink: %v", err)
	}

	outputRoot := filepath.Join(t.TempDir(), "rootfs")
	err := Generate(outputRoot, Template{
		Version:     TemplateVersionV1,
		Name:        "custom",
		Description: "Custom template",
		RuntimeTrees: []RuntimeTree{
			{HostPath: linkRoot, TargetPath: "/usr/share/locale"},
		},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	target := filepath.Join(outputRoot, "usr", "share", "locale", "C", "messages.txt")
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("expected copied runtime tree file %q to exist: %v", target, err)
	}
	if string(data) != "hello\n" {
		t.Fatalf("unexpected copied runtime tree content: %q", string(data))
	}
}

func TestGenerateCopiesSymlinkedNodeModuleLaunchers(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("host PATH does not contain node")
	}

	hostRoot := filepath.Join(t.TempDir(), "host")
	for _, dir := range []string{
		filepath.Join(hostRoot, "bin"),
		filepath.Join(hostRoot, "lib", "node_modules", "demo", "bin"),
		filepath.Join(hostRoot, "lib", "node_modules", "demo", "lib"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create fixture dir %q: %v", dir, err)
		}
	}

	launcherPath := filepath.Join(hostRoot, "lib", "node_modules", "demo", "bin", "demo.js")
	launcher := "#!/usr/bin/env node\nrequire('../lib/cli.js')\n"
	if err := os.WriteFile(launcherPath, []byte(launcher), 0o755); err != nil {
		t.Fatalf("write launcher: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hostRoot, "lib", "node_modules", "demo", "lib", "cli.js"), []byte("module.exports = () => {}\n"), 0o644); err != nil {
		t.Fatalf("write cli support file: %v", err)
	}
	if err := os.Symlink("../lib/node_modules/demo/bin/demo.js", filepath.Join(hostRoot, "bin", "demo")); err != nil {
		t.Fatalf("create launcher symlink: %v", err)
	}

	outputRoot := filepath.Join(t.TempDir(), "rootfs")
	err = Generate(outputRoot, Template{
		Version:     TemplateVersionV1,
		Name:        "custom",
		Description: "Custom template",
		Binaries: []Binary{
			{
				HostPath:         filepath.Join(hostRoot, "bin", "demo"),
				TargetPath:       "/usr/bin/demo",
				CopyDependencies: true,
			},
			{
				HostPath:         nodePath,
				TargetPath:       "/usr/bin/node",
				CopyDependencies: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	linkTarget, err := os.Readlink(filepath.Join(outputRoot, "usr", "bin", "demo"))
	if err != nil {
		t.Fatalf("read generated symlink: %v", err)
	}
	if linkTarget != "../lib/node_modules/demo/bin/demo.js" {
		t.Fatalf("unexpected generated symlink target: %q", linkTarget)
	}
	for _, target := range []string{
		filepath.Join(outputRoot, "usr", "lib", "node_modules", "demo", "bin", "demo.js"),
		filepath.Join(outputRoot, "usr", "lib", "node_modules", "demo", "lib", "cli.js"),
		filepath.Join(outputRoot, "usr", "bin", "node"),
	} {
		if _, err := os.Stat(target); err != nil {
			t.Fatalf("expected generated target %q to exist: %v", target, err)
		}
	}
}

func TestGenerateCollapsesRelocatedAbsoluteSymlinkLoops(t *testing.T) {
	hostRoot := filepath.Join(t.TempDir(), "host")
	if err := os.MkdirAll(filepath.Join(hostRoot, "bin"), 0o755); err != nil {
		t.Fatalf("create host bin dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(hostRoot, "usr", "bin"), 0o755); err != nil {
		t.Fatalf("create host usr/bin dir: %v", err)
	}

	hostTarget := filepath.Join(hostRoot, "bin", "bash")
	if err := os.WriteFile(hostTarget, []byte("bash binary\n"), 0o755); err != nil {
		t.Fatalf("write host target file: %v", err)
	}
	hostLink := filepath.Join(hostRoot, "usr", "bin", "bash")
	if err := os.Symlink(hostTarget, hostLink); err != nil {
		t.Fatalf("create host symlink: %v", err)
	}

	outputRoot := filepath.Join(t.TempDir(), "rootfs")
	err := Generate(outputRoot, Template{
		Version:     TemplateVersionV1,
		Name:        "custom",
		Description: "Custom template",
		Binaries: []Binary{
			{
				HostPath:         hostLink,
				TargetPath:       hostTarget,
				CopyDependencies: false,
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	targetPath := filepath.Join(outputRoot, strings.TrimPrefix(hostTarget, "/"))
	info, err := os.Lstat(targetPath)
	if err != nil {
		t.Fatalf("lstat generated target: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected relocated self-referential symlink to collapse into a file, got symlink")
	}
}

func TestGenerateOpenclawDeveloperHasNoRecursiveSymlinks(t *testing.T) {
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("host PATH does not contain bash")
	}
	info, err := os.Lstat(bashPath)
	if err != nil {
		t.Fatalf("lstat bash path: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Skip("host bash path is not a symlink on this host")
	}

	template, ok := LookupTemplate("openclaw-developer")
	if !ok {
		t.Fatal("expected openclaw-developer template to exist")
	}

	outputRoot := filepath.Join(t.TempDir(), "rootfs")
	if err := Generate(outputRoot, template); err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if err := assertNoRecursiveSymlinks(outputRoot); err != nil {
		t.Fatalf("expected generated rootfs to avoid recursive symlinks: %v", err)
	}
}

func assertNoRecursiveSymlinks(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink == 0 {
			return nil
		}
		if _, err := resolveRootfsSymlink(root, path); errors.Is(err, errRecursiveSymlink) {
			return fmt.Errorf("%s: %w", path, err)
		}
		return nil
	})
}

func resolveRootfsSymlink(root string, path string) (string, error) {
	seen := make(map[string]struct{})
	current := path
	for {
		if _, exists := seen[current]; exists {
			return "", errRecursiveSymlink
		}
		seen[current] = struct{}{}

		info, err := os.Lstat(current)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return "", nil
			}
			return "", err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return current, nil
		}

		linkTarget, err := os.Readlink(current)
		if err != nil {
			return "", err
		}
		if filepath.IsAbs(linkTarget) {
			current = filepath.Join(root, strings.TrimPrefix(linkTarget, "/"))
			continue
		}
		current = filepath.Clean(filepath.Join(filepath.Dir(current), linkTarget))
	}
}
