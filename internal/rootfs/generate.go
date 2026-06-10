package rootfs

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

const (
	testSkipBootstrapEnv         = "MIRAGE_TEST_SKIP_MMDEBSTRAP"
	debianRelease                = "bookworm"
	debianMirror                 = "http://deb.debian.org/debian"
	minimalAptConfigPath         = "/etc/apt/apt.conf.d/99sandbox-minimal"
	minimalAptConfigContent      = "APT::Install-Recommends \"false\";\nAPT::Install-Suggests \"false\";\nAPT::Sandbox::User \"root\";\n"
	mmdebstrapIncludePackageList = "apt,ca-certificates,bash,coreutils,util-linux,procps,psmisc,iproute2,curl,tar,gzip,xz-utils,git"
)

var currentEUID = os.Geteuid
var readMountInfo = os.ReadFile
var detectBootstrapHostArchitecture = defaultDetectBootstrapHostArchitecture

type MissingAsset struct {
	Source     string
	TargetPath string
	Reason     string
}

func (asset MissingAsset) Message() string {
	switch {
	case asset.TargetPath != "" && asset.Reason != "":
		return fmt.Sprintf("missing host asset %q for %q (%s)", asset.Source, asset.TargetPath, asset.Reason)
	case asset.TargetPath != "":
		return fmt.Sprintf("missing host asset %q for %q", asset.Source, asset.TargetPath)
	case asset.Reason != "":
		return fmt.Sprintf("missing host asset %q (%s)", asset.Source, asset.Reason)
	default:
		return fmt.Sprintf("missing host asset %q", asset.Source)
	}
}

type GenerateReport struct {
	Architecture  string
	MissingAssets []MissingAsset
	Warnings      []string
}

type GenerateOptions struct {
	AllowOverwrite bool
	LogOutput      io.Writer
	Architecture   string
}

var supportedRootfsArchitectures = []string{"x86_64", "arm64", "arm32", "riscv64"}

func SupportedArchitectures() []string {
	return append([]string(nil), supportedRootfsArchitectures...)
}

func NormalizeArchitecture(raw string) (string, error) {
	return normalizeRootfsArchitecture(raw)
}

func GoBuildTargetForArchitecture(architecture string) (string, string, error) {
	switch architecture {
	case "x86_64":
		return "amd64", "", nil
	case "arm64":
		return "arm64", "", nil
	case "arm32":
		return "arm", "7", nil
	case "riscv64":
		return "riscv64", "", nil
	default:
		return "", "", fmt.Errorf("unsupported architecture %q (supported: %s)", architecture, strings.Join(supportedRootfsArchitectures, ", "))
	}
}

func (report *GenerateReport) addMissing(asset MissingAsset) {
	report.MissingAssets = append(report.MissingAssets, asset)
}

func (report *GenerateReport) addWarning(message string) {
	report.Warnings = append(report.Warnings, message)
}

func (report *GenerateReport) merge(other GenerateReport) {
	report.MissingAssets = append(report.MissingAssets, other.MissingAssets...)
	report.Warnings = append(report.Warnings, other.Warnings...)
}

func Generate(outputRoot string, template Template) error {
	_, err := GenerateWithReportWithOptions(outputRoot, template, GenerateOptions{})
	return err
}

func Bootstrap(outputRoot string) error {
	_, err := BootstrapWithReportWithOptions(outputRoot, GenerateOptions{})
	return err
}

func BootstrapWithOptions(outputRoot string, options GenerateOptions) error {
	_, err := BootstrapWithReportWithOptions(outputRoot, options)
	return err
}

func BootstrapWithReport(outputRoot string) (GenerateReport, error) {
	return BootstrapWithReportWithOptions(outputRoot, GenerateOptions{})
}

func BootstrapWithReportWithOptions(outputRoot string, options GenerateOptions) (GenerateReport, error) {
	if os.Getenv(testSkipBootstrapEnv) != "1" && currentEUID() != 0 {
		return GenerateReport{}, errors.New("rootfs init requires root privileges; run via sudo ./bin/mirage rootfs init ...")
	}
	if strings.TrimSpace(outputRoot) == "" {
		return GenerateReport{}, errors.New("output rootfs path cannot be empty")
	}
	architecture, err := resolveRootfsArchitecture(options.Architecture)
	if err != nil {
		return GenerateReport{}, err
	}
	report := GenerateReport{Architecture: architecture}
	debianArchitecture, err := debianArchitectureForRootfsArch(architecture)
	if err != nil {
		return report, err
	}
	root, err := filepath.Abs(outputRoot)
	if err != nil {
		return report, fmt.Errorf("resolve output rootfs %q: %w", outputRoot, err)
	}
	if err := validateBootstrapTarget(root); err != nil {
		return report, err
	}
	if err := prepareOutputRoot(root, options.AllowOverwrite); err != nil {
		return report, err
	}
	if err := bootstrapDebianBaseRootfs(root, debianArchitecture, options.LogOutput); err != nil {
		return report, err
	}
	if err := writeMinimalAptConfig(root, options.LogOutput); err != nil {
		return report, err
	}

	nssReport, err := EnsureNSSRuntimeWithReport(root)
	if err != nil {
		return report, err
	}
	report.merge(nssReport)
	return report, nil
}

func GenerateWithReport(outputRoot string, template Template) (GenerateReport, error) {
	return GenerateWithReportWithOptions(outputRoot, template, GenerateOptions{})
}

func GenerateWithOptions(outputRoot string, template Template, options GenerateOptions) error {
	_, err := GenerateWithReportWithOptions(outputRoot, template, options)
	return err
}

func GenerateWithReportWithOptions(outputRoot string, template Template, options GenerateOptions) (GenerateReport, error) {
	if err := ValidateTemplate(template); err != nil {
		return GenerateReport{}, err
	}

	report, err := BootstrapWithReportWithOptions(outputRoot, options)
	if err != nil {
		return report, err
	}
	root, err := filepath.Abs(outputRoot)
	if err != nil {
		return report, fmt.Errorf("resolve output rootfs %q: %w", outputRoot, err)
	}

	generator := generator{
		outputRoot:      root,
		allowOverwrite:  true,
		report:          report,
		copiedTargets:   make(map[string]struct{}),
		copiedTrees:     make(map[string]struct{}),
		missingReported: make(map[string]struct{}),
		shebangCache:    make(map[string]shebangCacheEntry),
		lddCache:        make(map[string]lddCacheEntry),
	}
	for _, dir := range template.Directories {
		if err := generator.ensureDirectory(dir); err != nil {
			return generator.report, err
		}
	}
	for _, runtimeTree := range template.RuntimeTrees {
		if err := generator.copyRuntimeTree(runtimeTree); err != nil {
			return generator.report, err
		}
	}
	for _, runtimeFile := range template.RuntimeFiles {
		if err := generator.copyRuntimeFile(runtimeFile); err != nil {
			return generator.report, err
		}
	}
	for _, generatedFile := range template.GeneratedFiles {
		if err := generator.writeGeneratedFile(generatedFile); err != nil {
			return generator.report, err
		}
	}
	for _, binary := range template.Binaries {
		if err := generator.copyTemplateBinary(binary); err != nil {
			return generator.report, err
		}
	}
	nssReport, err := EnsureNSSRuntimeWithReport(root)
	if err != nil {
		return generator.report, err
	}
	generator.report.merge(nssReport)
	return generator.report, nil
}

type generator struct {
	outputRoot      string
	allowOverwrite  bool
	report          GenerateReport
	copiedTargets   map[string]struct{}
	copiedTrees     map[string]struct{}
	missingReported map[string]struct{}
	shebangCache    map[string]shebangCacheEntry
	lddCache        map[string]lddCacheEntry
}

func prepareOutputRoot(root string, allowOverwrite bool) error {
	info, err := os.Stat(root)
	switch {
	case err == nil:
		if !info.IsDir() {
			return fmt.Errorf("output rootfs %q is not a directory", root)
		}
		if allowOverwrite {
			return clearDirectory(root)
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			return fmt.Errorf("read output rootfs %q: %w", root, err)
		}
		if len(entries) > 0 {
			return fmt.Errorf("output rootfs %q already exists and is not empty", root)
		}
		return nil
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(root, 0o755); err != nil {
			return fmt.Errorf("create output rootfs %q: %w", root, err)
		}
		return nil
	default:
		return fmt.Errorf("stat output rootfs %q: %w", root, err)
	}
}

func clearDirectory(root string) error {
	mounted, err := hasNestedMounts(root)
	if err != nil {
		return fmt.Errorf("check active mounts in %q: %w", root, err)
	}
	if mounted {
		return fmt.Errorf("refusing to clear directory %q because it contains active mount points; please unmount them first", root)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("read output rootfs %q: %w", root, err)
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
			return fmt.Errorf("clear output rootfs %q: remove %q: %w", root, entry.Name(), err)
		}
	}
	return nil
}

func validateBootstrapTarget(root string) error {
	root = filepath.Clean(root)
	switch root {
	case "/",
		"/bin",
		"/boot",
		"/dev",
		"/etc",
		"/home",
		"/lib",
		"/lib64",
		"/media",
		"/mnt",
		"/opt",
		"/proc",
		"/root",
		"/run",
		"/sbin",
		"/srv",
		"/sys",
		"/tmp",
		"/usr",
		"/var":
		return fmt.Errorf("refusing to use critical system directory %q as rootfs", root)
	default:
		return nil
	}
}

func hasNestedMounts(root string) (bool, error) {
	data, err := readMountInfo("/proc/self/mountinfo")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}

	root = filepath.Clean(root)
	rootPrefix := root + string(filepath.Separator)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 5 {
			continue
		}
		mountpoint := filepath.Clean(unescapeMountInfoPath(fields[4]))
		if mountpoint != root && strings.HasPrefix(mountpoint, rootPrefix) {
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func unescapeMountInfoPath(raw string) string {
	replacer := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return replacer.Replace(raw)
}

func bootstrapDebianBaseRootfs(root string, architecture string, logOutput io.Writer) error {
	logCommand(logOutput, "mmdebstrap",
		"--architectures="+architecture,
		"--variant=minbase",
		`--aptopt=APT::Install-Recommends "false"`,
		"--include="+mmdebstrapIncludePackageList,
		debianRelease,
		root,
		debianMirror,
	)
	if os.Getenv(testSkipBootstrapEnv) == "1" {
		for _, dir := range []string{"proc", "tmp", "run", "dev", "etc/apt/apt.conf.d", "usr/bin", "usr/lib", "usr/lib64"} {
			if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
				return fmt.Errorf("prepare fake bootstrap directory %q: %w", dir, err)
			}
		}
		for linkPath, linkTarget := range map[string]string{
			"bin":   "usr/bin",
			"lib":   "usr/lib",
			"lib64": "usr/lib64",
		} {
			if err := os.Symlink(linkTarget, filepath.Join(root, linkPath)); err != nil && !errors.Is(err, os.ErrExist) {
				return fmt.Errorf("prepare fake bootstrap symlink %q: %w", linkPath, err)
			}
		}
		for _, name := range []string{"sh", "bash", "ls"} {
			source, err := exec.LookPath(name)
			if err != nil {
				return fmt.Errorf("resolve fake bootstrap command %q: %w", name, err)
			}
			resolvedSource, err := filepath.EvalSymlinks(source)
			if err != nil {
				return fmt.Errorf("resolve fake bootstrap command symlink %q: %w", name, err)
			}
			if err := copyBootstrapBinary(root, resolvedSource, filepath.Join("/bin", name)); err != nil {
				return fmt.Errorf("prepare fake bootstrap command %q: %w", name, err)
			}
		}
		logLine(logOutput, "output: [test mode] skipped mmdebstrap execution")
		return nil
	}

	args := []string{
		"--architectures=" + architecture,
		"--variant=minbase",
		`--aptopt=APT::Install-Recommends "false"`,
		"--include=" + mmdebstrapIncludePackageList,
		debianRelease,
		root,
		debianMirror,
	}
	var stderrBuf bytes.Buffer
	cmd := exec.Command("mmdebstrap", args...)
	if logOutput != nil {
		cmd.Stdout = logOutput
		cmd.Stderr = logOutput
	} else {
		cmd.Stderr = &stderrBuf
	}
	err := cmd.Run()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return errors.New("mmdebstrap is required on the host to initialize a rootfs")
		}
		if stderr := strings.TrimSpace(stderrBuf.String()); stderr != "" {
			return fmt.Errorf("bootstrap rootfs with mmdebstrap: %w: %s", err, stderr)
		}
		return fmt.Errorf("bootstrap rootfs with mmdebstrap: %w", err)
	}
	return nil
}

func resolveRootfsArchitecture(requested string) (string, error) {
	value := strings.TrimSpace(requested)
	if value == "" {
		hostArchitecture, err := detectBootstrapHostArchitecture()
		if err != nil {
			return "", err
		}
		value = hostArchitecture
	}
	return normalizeRootfsArchitecture(value)
}

func normalizeRootfsArchitecture(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "x86_64", "arm64", "arm32", "riscv64":
		return value, nil
	default:
		return "", fmt.Errorf("unsupported architecture %q (supported: %s)", raw, strings.Join(supportedRootfsArchitectures, ", "))
	}
}

func debianArchitectureForRootfsArch(architecture string) (string, error) {
	switch architecture {
	case "x86_64":
		return "amd64", nil
	case "arm64":
		return "arm64", nil
	case "arm32":
		return "armhf", nil
	case "riscv64":
		return "riscv64", nil
	default:
		return "", fmt.Errorf("unsupported architecture %q (supported: %s)", architecture, strings.Join(supportedRootfsArchitectures, ", "))
	}
}

func defaultDetectBootstrapHostArchitecture() (string, error) {
	if dpkgPath, err := exec.LookPath("dpkg"); err == nil {
		output, err := exec.Command(dpkgPath, "--print-architecture").Output()
		if err == nil {
			switch strings.TrimSpace(string(output)) {
			case "amd64":
				return "x86_64", nil
			case "arm64":
				return "arm64", nil
			case "armhf":
				return "arm32", nil
			case "riscv64":
				return "riscv64", nil
			}
		}
	}
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64", nil
	case "arm64":
		return "arm64", nil
	case "arm":
		return "arm32", nil
	case "riscv64":
		return "riscv64", nil
	default:
		return "", fmt.Errorf("could not detect a supported host architecture from GOARCH=%q; pass --arch explicitly (supported: %s)", runtime.GOARCH, strings.Join(supportedRootfsArchitectures, ", "))
	}
}

func copyBootstrapBinary(root string, sourcePath string, targetPath string) error {
	if err := copyBootstrapFile(root, sourcePath, targetPath); err != nil {
		return err
	}
	report, err := lddDependencyReport(sourcePath)
	if err != nil {
		return err
	}
	for _, dependency := range report.missing {
		return fmt.Errorf("missing shared library dependency: %s", dependency)
	}
	for _, dependency := range report.paths {
		if err := copyBootstrapFile(root, dependency, dependency); err != nil {
			return err
		}
	}
	return nil
}

func copyBootstrapFile(root string, sourcePath string, targetPath string) error {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return fmt.Errorf("stat host file %q: %w", sourcePath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("host path %q is a directory; only files are supported", sourcePath)
	}

	target := filepath.Join(root, strings.TrimPrefix(targetPath, "/"))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create parent directory for %q: %w", targetPath, err)
	}
	if targetInfo, err := os.Lstat(target); err == nil {
		if targetInfo.IsDir() {
			return fmt.Errorf("target path %q already exists and is a directory", targetPath)
		}
		if err := os.Remove(target); err != nil {
			return fmt.Errorf("remove existing target path %q: %w", targetPath, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("lstat target path %q: %w", targetPath, err)
	}

	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open host file %q: %w", sourcePath, err)
	}
	defer sourceFile.Close()

	targetFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("create target file %q: %w", targetPath, err)
	}
	if _, err := io.Copy(targetFile, sourceFile); err != nil {
		targetFile.Close()
		return fmt.Errorf("copy %q to %q: %w", sourcePath, targetPath, err)
	}
	if err := targetFile.Close(); err != nil {
		return fmt.Errorf("close target file %q: %w", targetPath, err)
	}
	if err := os.Chmod(target, info.Mode().Perm()); err != nil {
		return fmt.Errorf("set mode for target file %q: %w", targetPath, err)
	}
	return nil
}

func writeMinimalAptConfig(root string, logOutput io.Writer) error {
	target := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(minimalAptConfigPath, "/")))
	logAptConfigCommand(logOutput, target)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create apt config directory for %q: %w", minimalAptConfigPath, err)
	}
	if err := os.WriteFile(target, []byte(minimalAptConfigContent), 0o644); err != nil {
		return fmt.Errorf("write apt config %q: %w", minimalAptConfigPath, err)
	}
	return nil
}

func logWriterOrDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

func logCommand(w io.Writer, name string, args ...string) {
	if w == nil {
		return
	}
	parts := append([]string{name}, args...)
	var rendered []string
	for _, part := range parts {
		rendered = append(rendered, strconvQuote(part))
	}
	_, _ = fmt.Fprintf(w, "command: %s\n", strings.Join(rendered, " "))
}

func logLine(w io.Writer, message string) {
	if w == nil {
		return
	}
	_, _ = fmt.Fprintln(w, message)
}

func logAptConfigCommand(w io.Writer, target string) {
	if w == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "command: sudo tee %s >/dev/null <<'EOF'\n", strconvQuote(target))
	_, _ = fmt.Fprint(w, minimalAptConfigContent)
	_, _ = fmt.Fprintln(w, "EOF")
}

func strconvQuote(s string) string {
	if s == "" {
		return `""`
	}
	if strings.IndexFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '"' || r == '\''
	}) >= 0 {
		return fmt.Sprintf("%q", s)
	}
	return s
}

func (g *generator) ensureDirectory(dir Directory) error {
	target := g.rootPath(dir.Path)
	mode := os.FileMode(dir.Mode)
	if mode == 0 {
		mode = 0o755
	}
	if err := g.prepareTargetPath(dir.Path, true); err != nil {
		return err
	}
	if err := os.MkdirAll(target, mode); err != nil {
		return fmt.Errorf("create directory %q: %w", dir.Path, err)
	}
	if err := os.Chmod(target, mode); err != nil {
		return fmt.Errorf("set directory mode for %q: %w", dir.Path, err)
	}
	return nil
}

func (g *generator) copyRuntimeFile(runtimeFile RuntimeFile) error {
	return g.copyHostFile(runtimeFile.HostPath, runtimeFile.TargetPath, runtimeFile.Optional)
}

func (g *generator) copyRuntimeTree(runtimeTree RuntimeTree) error {
	if _, err := os.Stat(runtimeTree.HostPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if !runtimeTree.Optional {
				g.recordMissing(runtimeTree.HostPath, runtimeTree.TargetPath, "runtime tree")
			}
			return nil
		}
		return fmt.Errorf("stat host tree %q: %w", runtimeTree.HostPath, err)
	}
	sourceRoot, err := filepath.EvalSymlinks(runtimeTree.HostPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if !runtimeTree.Optional {
				g.recordMissing(runtimeTree.HostPath, runtimeTree.TargetPath, "runtime tree symlink target")
			}
			return nil
		}
		return fmt.Errorf("resolve host tree %q: %w", runtimeTree.HostPath, err)
	}
	return g.copyHostTree(sourceRoot, runtimeTree.TargetPath)
}

func (g *generator) writeGeneratedFile(generatedFile GeneratedFile) error {
	target := g.rootPath(generatedFile.TargetPath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create parent directory for generated file %q: %w", generatedFile.TargetPath, err)
	}
	if err := g.prepareTargetPath(generatedFile.TargetPath, false); err != nil {
		return fmt.Errorf("write generated file %q: %w", generatedFile.TargetPath, err)
	}
	mode := os.FileMode(generatedFile.Mode)
	if mode == 0 {
		mode = 0o644
	}
	if err := os.WriteFile(target, []byte(generatedFile.Content), mode); err != nil {
		return fmt.Errorf("write generated file %q: %w", generatedFile.TargetPath, err)
	}
	return nil
}

func (g *generator) copyTemplateBinary(binary Binary) error {
	if binary.HostPath != "" {
		if binary.Optional {
			available, err := g.binaryCopyAvailable(binary.HostPath, binary.TargetPath, binary.CopyDependencies)
			if err != nil {
				return err
			}
			if !available {
				return nil
			}
		}
		return g.copyHostBinary(binary.HostPath, binary.TargetPath, binary.CopyDependencies)
	}
	source, err := exec.LookPath(binary.LookupName)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			if binary.Optional {
				return nil
			}
			g.recordMissing(fmt.Sprintf("PATH lookup %q", binary.LookupName), binary.TargetPath, "template binary")
			return nil
		}
		return fmt.Errorf("resolve binary %q on host PATH: %w", binary.LookupName, err)
	}
	if binary.Optional {
		available, err := g.binaryCopyAvailable(source, binary.TargetPath, binary.CopyDependencies)
		if err != nil {
			return err
		}
		if !available {
			return nil
		}
	}
	return g.copyHostBinary(source, binary.TargetPath, binary.CopyDependencies)
}

func (g *generator) binaryCopyAvailable(sourcePath string, targetPath string, copyDependencies bool) (bool, error) {
	return g.binaryCopyAvailableWithVisited(sourcePath, targetPath, copyDependencies, make(map[string]struct{}))
}

func (g *generator) binaryCopyAvailableWithVisited(sourcePath string, targetPath string, copyDependencies bool, visited map[string]struct{}) (bool, error) {
	if _, seen := visited[sourcePath]; seen {
		return false, nil
	}
	visited[sourcePath] = struct{}{}
	defer delete(visited, sourcePath)

	linkInfo, err := os.Lstat(sourcePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("lstat host file %q: %w", sourcePath, err)
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 {
		resolvedSource, err := filepath.EvalSymlinks(sourcePath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			return false, fmt.Errorf("resolve symlink %q: %w", sourcePath, err)
		}
		nextTarget := translatedSymlinkTarget(targetPath, sourcePath)
		return g.binaryCopyAvailableWithVisited(resolvedSource, nextTarget, copyDependencies, visited)
	}

	requests, missingAssets, err := g.cachedShebangRequests(sourcePath)
	if err != nil {
		return false, err
	}
	if len(missingAssets) > 0 {
		return false, nil
	}
	if len(requests) > 0 {
		for _, request := range requests {
			available, err := g.binaryCopyAvailableWithVisited(request.hostPath, request.targetPath, true, visited)
			if err != nil {
				return false, err
			}
			if !available {
				return false, nil
			}
		}
		return true, nil
	}
	if !copyDependencies {
		return true, nil
	}

	lddReport, err := g.cachedLDDDependencyReport(sourcePath)
	if err != nil {
		return false, err
	}
	return len(lddReport.missing) == 0, nil
}

func (g *generator) copyHostBinary(sourcePath string, targetPath string, copyDependencies bool) error {
	return g.copyHostBinaryWithVisited(sourcePath, targetPath, copyDependencies, make(map[string]struct{}))
}

func (g *generator) copyHostBinaryWithVisited(sourcePath string, targetPath string, copyDependencies bool, visited map[string]struct{}) error {
	effectiveTargetPath, err := g.rewriteTargetPathForHostAncestorSymlinks(sourcePath, targetPath)
	if err != nil {
		return err
	}

	if _, seen := visited[sourcePath]; seen {
		return fmt.Errorf("circular shebang dependency involving %q", sourcePath)
	}
	visited[sourcePath] = struct{}{}
	defer delete(visited, sourcePath)

	linkInfo, err := os.Lstat(sourcePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			g.recordMissing(sourcePath, effectiveTargetPath, "binary")
			return nil
		}
		return fmt.Errorf("lstat host file %q: %w", sourcePath, err)
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 {
		nextTarget := translatedSymlinkTarget(effectiveTargetPath, sourcePath)
		resolvedSource, err := filepath.EvalSymlinks(sourcePath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				g.recordMissing(fmt.Sprintf("symlink target of %q", sourcePath), nextTarget, "binary")
				return nil
			}
			return fmt.Errorf("resolve symlink %q: %w", sourcePath, err)
		}
		if nextTarget == effectiveTargetPath {
			return g.copyHostBinaryWithVisited(resolvedSource, effectiveTargetPath, copyDependencies, visited)
		}
		if err := g.copyHostSymlink(sourcePath, effectiveTargetPath); err != nil {
			return err
		}
		return g.copyHostBinaryWithVisited(resolvedSource, nextTarget, copyDependencies, visited)
	}

	if err := g.copyHostFile(sourcePath, effectiveTargetPath, false); err != nil {
		return err
	}

	requests, missingAssets, err := g.cachedShebangRequests(sourcePath)
	if err != nil {
		return err
	}
	for _, asset := range missingAssets {
		g.recordMissing(asset.Source, asset.TargetPath, asset.Reason)
	}
	if len(requests) > 0 {
		for _, request := range requests {
			if err := g.copyHostBinaryWithVisited(request.hostPath, request.targetPath, true, visited); err != nil {
				return err
			}
		}
		if err := g.copyScriptSupportTree(sourcePath, targetPath); err != nil {
			return err
		}
		return nil
	}
	if !copyDependencies {
		return nil
	}

	lddReport, err := g.cachedLDDDependencyReport(sourcePath)
	if err != nil {
		return err
	}
	for _, dependency := range lddReport.missing {
		g.recordMissing(fmt.Sprintf("shared library dependency %q", dependency), "", fmt.Sprintf("required by %q", sourcePath))
	}
	for _, dependency := range lddReport.paths {
		if err := g.copyHostFile(dependency, dependency, false); err != nil {
			return err
		}
	}
	return nil
}

type shebangCacheEntry struct {
	requests      []copyRequest
	missingAssets []MissingAsset
	err           error
}

func (g *generator) cachedShebangRequests(path string) ([]copyRequest, []MissingAsset, error) {
	if g.shebangCache == nil {
		g.shebangCache = make(map[string]shebangCacheEntry)
	}
	if entry, ok := g.shebangCache[path]; ok {
		return slices.Clone(entry.requests), slices.Clone(entry.missingAssets), entry.err
	}
	requests, missingAssets, err := shebangRequests(path)
	g.shebangCache[path] = shebangCacheEntry{
		requests:      slices.Clone(requests),
		missingAssets: slices.Clone(missingAssets),
		err:           err,
	}
	return requests, missingAssets, err
}

type lddCacheEntry struct {
	report lddReport
	err    error
}

func (g *generator) cachedLDDDependencyReport(path string) (lddReport, error) {
	if g.lddCache == nil {
		g.lddCache = make(map[string]lddCacheEntry)
	}
	if entry, ok := g.lddCache[path]; ok {
		return entry.report, entry.err
	}
	report, err := lddDependencyReport(path)
	g.lddCache[path] = lddCacheEntry{report: report, err: err}
	return report, err
}

func (g *generator) copyHostBinaryIfMissing(sourcePath string, targetPath string, copyDependencies bool) error {
	if _, exists := g.copiedTargets[targetPath]; exists {
		return nil
	}
	if !g.allowOverwrite {
		if _, err := os.Lstat(g.rootPath(targetPath)); err == nil {
			g.copiedTargets[targetPath] = struct{}{}
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat target path %q: %w", targetPath, err)
		}
	}
	return g.copyHostBinary(sourcePath, targetPath, copyDependencies)
}

type copyRequest struct {
	hostPath   string
	targetPath string
}

func shebangRequests(path string) ([]copyRequest, []MissingAsset, error) {
	line, ok, err := readShebang(path)
	if err != nil || !ok {
		return nil, nil, err
	}

	fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, "#!")))
	if len(fields) == 0 {
		return nil, nil, nil
	}
	interpreterPath := fields[0]
	if !filepath.IsAbs(interpreterPath) {
		return nil, nil, fmt.Errorf("script %q uses non-absolute shebang interpreter %q", path, interpreterPath)
	}

	requests := []copyRequest{{hostPath: interpreterPath, targetPath: interpreterPath}}
	if interpreterPath != "/usr/bin/env" {
		return requests, nil, nil
	}

	lookupName, ok := envLookupName(fields[1:])
	if !ok {
		return requests, nil, nil
	}
	resolved, err := exec.LookPath(lookupName)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return requests, []MissingAsset{
				{
					Source: fmt.Sprintf("PATH lookup %q", lookupName),
					Reason: fmt.Sprintf("shebang target required by %q", path),
				},
			}, nil
		}
		return nil, nil, fmt.Errorf("resolve shebang target %q on host PATH: %w", lookupName, err)
	}
	requests = append(requests, copyRequest{hostPath: resolved, targetPath: resolved})
	return requests, nil, nil
}

func readShebang(path string) (string, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", false, fmt.Errorf("open %q for shebang inspection: %w", path, err)
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", false, fmt.Errorf("read shebang from %q: %w", path, err)
	}
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "#!") {
		return "", false, nil
	}
	return line, true, nil
}

func envLookupName(args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	if args[0] == "-S" {
		args = strings.Fields(strings.Join(args[1:], " "))
	}
	for _, arg := range args {
		if arg == "" || strings.HasPrefix(arg, "-") {
			continue
		}
		return arg, true
	}
	return "", false
}

func lddDependencies(path string) ([]string, error) {
	report, err := lddDependencyReport(path)
	if err != nil {
		return nil, err
	}
	if len(report.missing) > 0 {
		return nil, fmt.Errorf("missing shared library dependency: %s", report.missing[0])
	}
	return report.paths, nil
}

type lddReport struct {
	paths   []string
	missing []string
}

func lddDependencyReport(path string) (lddReport, error) {
	cmd := exec.Command("ldd", path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(output))
		if strings.Contains(text, "not a dynamic executable") || strings.Contains(text, "statically linked") {
			return lddReport{}, nil
		}
		return lddReport{}, fmt.Errorf("resolve library dependencies for %q: %w: %s", path, err, text)
	}
	return parseLDDOutput(output)
}

func parseLDDOutput(output []byte) (lddReport, error) {
	var dependencies []string
	var missing []string
	seen := make(map[string]struct{})

	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "linux-vdso.") || line == "statically linked" || line == "not a dynamic executable" {
			continue
		}

		if strings.Contains(line, "not found") {
			missing = append(missing, strings.TrimSpace(strings.SplitN(line, "=>", 2)[0]))
			continue
		}

		candidate, ok := lddPath(line)
		if !ok {
			continue
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		dependencies = append(dependencies, candidate)
	}
	if err := scanner.Err(); err != nil {
		return lddReport{}, fmt.Errorf("scan ldd output: %w", err)
	}
	return lddReport{paths: dependencies, missing: missing}, nil
}

func lddPath(line string) (string, bool) {
	if strings.Contains(line, "=>") {
		parts := strings.SplitN(line, "=>", 2)
		fields := strings.Fields(strings.TrimSpace(parts[1]))
		if len(fields) == 0 {
			return "", false
		}
		if filepath.IsAbs(fields[0]) {
			return fields[0], true
		}
		return "", false
	}

	fields := strings.Fields(line)
	if len(fields) == 0 || !filepath.IsAbs(fields[0]) {
		return "", false
	}
	return fields[0], true
}

func (g *generator) copyHostSymlink(sourcePath string, targetPath string) error {
	if _, exists := g.copiedTargets[targetPath]; exists {
		return nil
	}

	linkTarget, err := os.Readlink(sourcePath)
	if err != nil {
		return fmt.Errorf("read host symlink %q: %w", sourcePath, err)
	}

	target := g.rootPath(targetPath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create parent directory for %q: %w", targetPath, err)
	}
	if err := g.prepareTargetPath(targetPath, false); err != nil {
		return err
	}
	if err := os.Symlink(linkTarget, target); err != nil {
		if !g.allowOverwrite && errors.Is(err, os.ErrExist) {
			g.copiedTargets[targetPath] = struct{}{}
			return nil
		}
		return fmt.Errorf("create target symlink %q: %w", targetPath, err)
	}

	g.copiedTargets[targetPath] = struct{}{}
	return nil
}

func translatedSymlinkTarget(targetPath string, sourcePath string) string {
	linkTarget, err := os.Readlink(sourcePath)
	if err != nil {
		return targetPath
	}
	if filepath.IsAbs(linkTarget) {
		return linkTarget
	}
	return filepath.Clean(filepath.Join(filepath.Dir(targetPath), linkTarget))
}

func (g *generator) copyScriptSupportTree(sourcePath string, targetPath string) error {
	sourceRoot, targetRoot, ok := nodeModulePackageRoots(sourcePath, targetPath)
	if !ok {
		return nil
	}
	return g.copyHostTree(sourceRoot, targetRoot)
}

func nodeModulePackageRoots(sourcePath string, targetPath string) (string, string, bool) {
	sourceRoot, ok := packageRootFromNodeModulesPath(sourcePath)
	if !ok {
		return "", "", false
	}
	targetRoot, ok := packageRootFromNodeModulesPath(targetPath)
	if !ok {
		return "", "", false
	}
	return sourceRoot, targetRoot, true
}

func packageRootFromNodeModulesPath(path string) (string, bool) {
	marker := string(filepath.Separator) + "node_modules" + string(filepath.Separator)
	idx := strings.Index(path, marker)
	if idx < 0 {
		return "", false
	}
	prefix := path[:idx]
	rest := path[idx+len(marker):]
	segments := strings.Split(rest, string(filepath.Separator))
	if len(segments) == 0 || segments[0] == "" {
		return "", false
	}
	pkgSegments := []string{segments[0]}
	if strings.HasPrefix(segments[0], "@") {
		if len(segments) < 2 || segments[1] == "" {
			return "", false
		}
		pkgSegments = append(pkgSegments, segments[1])
	}
	root := filepath.Join(append([]string{prefix, "node_modules"}, pkgSegments...)...)
	return root, true
}

func (g *generator) copyHostTree(sourceRoot string, targetRoot string) error {
	if _, exists := g.copiedTrees[targetRoot]; exists {
		return nil
	}

	info, err := os.Stat(sourceRoot)
	if err != nil {
		return fmt.Errorf("stat host tree %q: %w", sourceRoot, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("host tree %q is not a directory", sourceRoot)
	}

	targetAbs := g.rootPath(targetRoot)
	if err := filepath.WalkDir(sourceRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		targetGuestPath := targetRoot
		targetPath := targetAbs
		if rel != "." {
			targetGuestPath = filepath.Join(targetRoot, rel)
			targetPath = filepath.Join(targetAbs, rel)
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			if err := g.prepareTargetPath(targetGuestPath, true); err != nil {
				return err
			}
			if err := os.MkdirAll(targetPath, info.Mode().Perm()); err != nil {
				return err
			}
			return os.Chmod(targetPath, info.Mode().Perm())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return err
			}
			if err := g.prepareTargetPath(targetGuestPath, false); err != nil {
				return err
			}
			if err := os.Symlink(linkTarget, targetPath); err != nil {
				if !g.allowOverwrite && errors.Is(err, os.ErrExist) {
					g.copiedTargets[targetGuestPath] = struct{}{}
					return nil
				}
				return err
			}
			g.copiedTargets[targetGuestPath] = struct{}{}
			return nil
		}
		return g.copyHostFile(path, targetGuestPath, false)
	}); err != nil {
		return fmt.Errorf("copy host tree %q to %q: %w", sourceRoot, targetRoot, err)
	}

	g.copiedTrees[targetRoot] = struct{}{}
	return nil
}

func (g *generator) copyHostFile(sourcePath string, targetPath string, optional bool) error {
	effectiveTargetPath, err := g.rewriteTargetPathForHostAncestorSymlinks(sourcePath, targetPath)
	if err != nil {
		return err
	}

	if _, exists := g.copiedTargets[effectiveTargetPath]; exists {
		return nil
	}

	info, err := os.Stat(sourcePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if !optional {
				g.recordMissing(sourcePath, effectiveTargetPath, "runtime file")
			}
			return nil
		}
		return fmt.Errorf("stat host file %q: %w", sourcePath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("host path %q is a directory; only files are supported", sourcePath)
	}

	target := g.rootPath(effectiveTargetPath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create parent directory for %q: %w", effectiveTargetPath, err)
	}
	if err := g.prepareTargetPath(effectiveTargetPath, false); err != nil {
		return err
	}

	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open host file %q: %w", sourcePath, err)
	}
	defer sourceFile.Close()

	targetFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("create target file %q: %w", effectiveTargetPath, err)
	}

	if _, err := io.Copy(targetFile, sourceFile); err != nil {
		targetFile.Close()
		return fmt.Errorf("copy %q to %q: %w", sourcePath, effectiveTargetPath, err)
	}
	if err := targetFile.Close(); err != nil {
		return fmt.Errorf("close target file %q: %w", effectiveTargetPath, err)
	}
	if err := os.Chmod(target, info.Mode().Perm()); err != nil {
		return fmt.Errorf("set target mode for %q: %w", effectiveTargetPath, err)
	}
	if warning := preserveFileCapabilitiesWarning(sourcePath, effectiveTargetPath, target); warning != "" {
		g.report.addWarning(warning)
	}

	g.copiedTargets[effectiveTargetPath] = struct{}{}
	return nil
}

func (g *generator) rewriteTargetPathForHostAncestorSymlinks(sourcePath string, targetPath string) (string, error) {
	if !filepath.IsAbs(sourcePath) || !filepath.IsAbs(targetPath) {
		return targetPath, nil
	}

	targetParts := splitAbsolutePath(targetPath)
	maxDepth := len(targetParts)
	if maxDepth <= 1 {
		return targetPath, nil
	}

	for depth := 1; depth < maxDepth-1; depth++ {
		targetPrefix := joinAbsolutePath(targetParts[:depth+1])
		info, err := os.Lstat(targetPrefix)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return targetPath, nil
			}
			return "", fmt.Errorf("lstat host ancestor %q: %w", targetPrefix, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}

		if err := g.copyHostSymlink(targetPrefix, targetPrefix); err != nil {
			return "", err
		}

		translatedPrefix := translatedSymlinkTarget(targetPrefix, targetPrefix)
		if depth+1 >= len(targetParts) {
			return translatedPrefix, nil
		}
		return filepath.Join(append([]string{translatedPrefix}, targetParts[depth+1:]...)...), nil
	}

	return targetPath, nil
}

func splitAbsolutePath(path string) []string {
	cleaned := filepath.Clean(path)
	if cleaned == string(filepath.Separator) {
		return []string{""}
	}
	return append([]string{""}, strings.Split(strings.TrimPrefix(cleaned, string(filepath.Separator)), string(filepath.Separator))...)
}

func joinAbsolutePath(parts []string) string {
	if len(parts) <= 1 {
		return string(filepath.Separator)
	}
	return string(filepath.Separator) + filepath.Join(parts[1:]...)
}

func (g *generator) prepareTargetPath(targetPath string, wantDir bool) error {
	target := g.rootPath(targetPath)
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lstat target path %q: %w", targetPath, err)
	}
	if wantDir {
		if info.IsDir() {
			return nil
		}
		if !g.allowOverwrite {
			return fmt.Errorf("target path %q already exists and is not a directory", targetPath)
		}
		if err := os.Remove(target); err != nil {
			return fmt.Errorf("remove existing target path %q: %w", targetPath, err)
		}
		return nil
	}
	if info.IsDir() {
		return fmt.Errorf("target path %q already exists and is a directory", targetPath)
	}
	if !g.allowOverwrite {
		return nil
	}
	if err := os.Remove(target); err != nil {
		return fmt.Errorf("remove existing target path %q: %w", targetPath, err)
	}
	return nil
}

func (g *generator) rootPath(path string) string {
	return filepath.Join(g.outputRoot, strings.TrimPrefix(path, "/"))
}

func (g *generator) recordMissing(source string, targetPath string, reason string) {
	key := source + "\x00" + targetPath + "\x00" + reason
	if _, exists := g.missingReported[key]; exists {
		return
	}
	g.missingReported[key] = struct{}{}
	g.report.addMissing(MissingAsset{
		Source:     source,
		TargetPath: targetPath,
		Reason:     reason,
	})
}
