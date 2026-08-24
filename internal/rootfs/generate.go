package rootfs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/DemonGiggle/mirage/internal/hostenv"
)

const (
	testSkipBootstrapEnv         = "MIRAGE_TEST_SKIP_MMDEBSTRAP"
	testFinalizeOwnershipEnv     = "MIRAGE_TEST_FINALIZE_ROOTLESS_OWNERSHIP"
	testSudoBinaryEnv            = "MIRAGE_TEST_SUDO_BINARY"
	defaultDebianRelease         = "trixie"
	debianMirror                 = "http://deb.debian.org/debian"
	minimalAptConfigPath         = "/etc/apt/apt.conf.d/99sandbox-minimal"
	minimalAptConfigContent      = "APT::Install-Recommends \"false\";\nAPT::Install-Suggests \"false\";\nAPT::Sandbox::User \"root\";\n"
	mmdebstrapIncludePackageList = "apt,ca-certificates,bash,coreutils,util-linux,procps,psmisc,iproute2,curl,tar,gzip,xz-utils,git"
	defaultDistribution          = "debian"
)

var currentEUID = os.Geteuid
var readMountInfo = os.ReadFile
var requireRootlessIDMapSupport = RequireRootlessIDMapSupport
var debianPackageNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]*$`)

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
	Distribution    string
	Release         string
	Architecture    string
	HostEnvironment hostenv.Kind
	MissingAssets   []MissingAsset
	Warnings        []string
}

type GenerateOptions struct {
	AllowOverwrite  bool
	LogOutput       io.Writer
	Distribution    string
	Architecture    string
	DebianRelease   string
	TinyCoreRelease string
	ExtraPackages   []string
	IncludeSudo     bool
}

func DefaultDebianRelease() string {
	return defaultDebianRelease
}

func SupportedDistributions() []string {
	return []string{"debian", "tinycore"}
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
	return bootstrapWithReportWithOptions(outputRoot, options, true)
}

func bootstrapWithReportWithOptions(outputRoot string, options GenerateOptions, finalize bool) (GenerateReport, error) {
	if strings.TrimSpace(outputRoot) == "" {
		return GenerateReport{}, errors.New("output rootfs path cannot be empty")
	}
	strategy := selectBootstrapStrategy(currentEUID())
	distribution, err := normalizeDistribution(options.Distribution)
	if err != nil {
		return GenerateReport{}, err
	}
	architecture, err := resolveRootfsArchitecture(options.Architecture)
	if err != nil {
		return GenerateReport{}, err
	}
	report := GenerateReport{
		Distribution:    distribution,
		Architecture:    architecture,
		HostEnvironment: strategy.hostEnvironment(),
	}
	root, err := filepath.Abs(outputRoot)
	if err != nil {
		return report, fmt.Errorf("resolve output rootfs %q: %w", outputRoot, err)
	}
	if err := validateBootstrapTarget(root); err != nil {
		return report, err
	}
	if err := validateDistributionOptions(distribution, architecture, options); err != nil {
		return report, err
	}
	if report.HostEnvironment == hostenv.Rootless &&
		(os.Getenv(testSkipBootstrapEnv) != "1" || os.Getenv(testFinalizeOwnershipEnv) == "1") {
		if err := requireRootlessIDMapSupport(); err != nil {
			return report, err
		}
	}
	if err := strategy.prepareOutput(root, options.AllowOverwrite); err != nil {
		return report, err
	}
	switch distribution {
	case "debian":
		if strings.TrimSpace(options.TinyCoreRelease) != "" {
			return report, errors.New("--tinycore-release requires --distro tinycore")
		}
		debianArchitecture, err := debianArchitectureForRootfsArch(architecture)
		if err != nil {
			return report, err
		}
		release, err := normalizeDebianRelease(options.DebianRelease)
		if err != nil {
			return report, err
		}
		report.Release = release
		rawExtraPackages := append([]string{}, options.ExtraPackages...)
		if options.IncludeSudo {
			rawExtraPackages = append(rawExtraPackages, "sudo")
		}
		extraPackages, err := normalizeExtraPackages(rawExtraPackages)
		if err != nil {
			return report, err
		}
		if err := bootstrapDebianBaseRootfs(root, debianArchitecture, release, extraPackages, strategy, options.LogOutput); err != nil {
			return report, err
		}
		if err := writeMinimalAptConfig(root, strategy.hostEnvironment(), options.LogOutput); err != nil {
			return report, err
		}
		nssReport, err := EnsureNSSRuntimeWithReport(root)
		if err != nil {
			return report, err
		}
		report.merge(nssReport)
	case "tinycore":
		if strings.TrimSpace(options.DebianRelease) != "" {
			return report, errors.New("--debian-release requires --distro debian")
		}
		if len(options.ExtraPackages) > 0 {
			return report, errors.New("--extra-pkg is not supported with --distro tinycore; install extensions at runtime with tce-load")
		}
		release, err := normalizeTinyCoreRelease(options.TinyCoreRelease)
		if err != nil {
			return report, err
		}
		report.Release = release
		if err := bootstrapTinyCoreBaseRootfs(root, architecture, release, options.LogOutput); err != nil {
			return report, err
		}
	}
	if finalize && report.HostEnvironment == hostenv.Rootless {
		if os.Getenv(testSkipBootstrapEnv) != "1" || os.Getenv(testFinalizeOwnershipEnv) == "1" {
			if err := finalizeRootlessOwnership(root); err != nil {
				return report, fmt.Errorf("finalize rootless rootfs ownership: %w", err)
			}
		}
	}
	return report, nil
}

func normalizeDistribution(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return defaultDistribution, nil
	}
	if value == "debian" || value == "tinycore" {
		return value, nil
	}
	return "", fmt.Errorf("unsupported rootfs distribution %q (supported: %s)", raw, strings.Join(SupportedDistributions(), ", "))
}

func validateDistributionOptions(distribution, architecture string, options GenerateOptions) error {
	switch distribution {
	case "debian":
		if strings.TrimSpace(options.TinyCoreRelease) != "" {
			return errors.New("--tinycore-release requires --distro tinycore")
		}
		if _, err := normalizeDebianRelease(options.DebianRelease); err != nil {
			return err
		}
		rawExtraPackages := append([]string{}, options.ExtraPackages...)
		if options.IncludeSudo {
			rawExtraPackages = append(rawExtraPackages, "sudo")
		}
		_, err := normalizeExtraPackages(rawExtraPackages)
		return err
	case "tinycore":
		if architecture != "x86_64" {
			return fmt.Errorf("Tiny Core rootfs initialization supports only x86_64, got %q", architecture)
		}
		if strings.TrimSpace(options.DebianRelease) != "" {
			return errors.New("--debian-release requires --distro debian")
		}
		if len(options.ExtraPackages) > 0 {
			return errors.New("--extra-pkg is not supported with --distro tinycore; install extensions at runtime with tce-load")
		}
		_, err := normalizeTinyCoreRelease(options.TinyCoreRelease)
		return err
	default:
		return fmt.Errorf("unsupported rootfs distribution %q", distribution)
	}
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

	report, err := bootstrapWithReportWithOptions(outputRoot, options, false)
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
	if generator.report.HostEnvironment == hostenv.Rootless {
		if os.Getenv(testSkipBootstrapEnv) != "1" || os.Getenv(testFinalizeOwnershipEnv) == "1" {
			if err := finalizeRootlessOwnership(root); err != nil {
				return generator.report, fmt.Errorf("finalize rootless rootfs ownership: %w", err)
			}
		}
	}
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
	return pathInsideRoot(g.outputRoot, path)
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
