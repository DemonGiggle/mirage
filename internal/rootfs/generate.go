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
	"strings"
)

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
	MissingAssets []MissingAsset
}

func (report *GenerateReport) addMissing(asset MissingAsset) {
	report.MissingAssets = append(report.MissingAssets, asset)
}

func (report *GenerateReport) merge(other GenerateReport) {
	report.MissingAssets = append(report.MissingAssets, other.MissingAssets...)
}

func Generate(outputRoot string, template Template) error {
	_, err := GenerateWithReport(outputRoot, template)
	return err
}

func GenerateWithReport(outputRoot string, template Template) (GenerateReport, error) {
	if err := ValidateTemplate(template); err != nil {
		return GenerateReport{}, err
	}

	root, err := filepath.Abs(outputRoot)
	if err != nil {
		return GenerateReport{}, fmt.Errorf("resolve output rootfs %q: %w", outputRoot, err)
	}
	if err := prepareOutputRoot(root); err != nil {
		return GenerateReport{}, err
	}

	generator := generator{
		outputRoot:      root,
		copiedTargets:   make(map[string]struct{}),
		copiedTrees:     make(map[string]struct{}),
		missingReported: make(map[string]struct{}),
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
	report          GenerateReport
	copiedTargets   map[string]struct{}
	copiedTrees     map[string]struct{}
	missingReported map[string]struct{}
}

func prepareOutputRoot(root string) error {
	info, err := os.Stat(root)
	switch {
	case err == nil:
		if !info.IsDir() {
			return fmt.Errorf("output rootfs %q is not a directory", root)
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

func (g *generator) ensureDirectory(dir Directory) error {
	target := g.rootPath(dir.Path)
	mode := os.FileMode(dir.Mode)
	if mode == 0 {
		mode = 0o755
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
			available, err := binaryCopyAvailable(binary.HostPath, binary.TargetPath, binary.CopyDependencies)
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
		available, err := binaryCopyAvailable(source, binary.TargetPath, binary.CopyDependencies)
		if err != nil {
			return err
		}
		if !available {
			return nil
		}
	}
	return g.copyHostBinary(source, binary.TargetPath, binary.CopyDependencies)
}

func binaryCopyAvailable(sourcePath string, targetPath string, copyDependencies bool) (bool, error) {
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
		return binaryCopyAvailable(resolvedSource, translatedSymlinkTarget(targetPath, sourcePath), copyDependencies)
	}

	requests, missingAssets, err := shebangRequests(sourcePath)
	if err != nil {
		return false, err
	}
	if len(missingAssets) > 0 {
		return false, nil
	}
	if len(requests) > 0 {
		for _, request := range requests {
			available, err := binaryCopyAvailable(request.hostPath, request.targetPath, true)
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

	lddReport, err := lddDependencyReport(sourcePath)
	if err != nil {
		return false, err
	}
	return len(lddReport.missing) == 0, nil
}

func (g *generator) copyHostBinary(sourcePath string, targetPath string, copyDependencies bool) error {
	linkInfo, err := os.Lstat(sourcePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			g.recordMissing(sourcePath, targetPath, "binary")
			return nil
		}
		return fmt.Errorf("lstat host file %q: %w", sourcePath, err)
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 {
		if err := g.copyHostSymlink(sourcePath, targetPath); err != nil {
			return err
		}
		resolvedSource, err := filepath.EvalSymlinks(sourcePath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				g.recordMissing(fmt.Sprintf("symlink target of %q", sourcePath), translatedSymlinkTarget(targetPath, sourcePath), "binary")
				return nil
			}
			return fmt.Errorf("resolve symlink %q: %w", sourcePath, err)
		}
		return g.copyHostBinary(resolvedSource, translatedSymlinkTarget(targetPath, sourcePath), copyDependencies)
	}

	if err := g.copyHostFile(sourcePath, targetPath, false); err != nil {
		return err
	}

	requests, missingAssets, err := shebangRequests(sourcePath)
	if err != nil {
		return err
	}
	for _, asset := range missingAssets {
		g.recordMissing(asset.Source, asset.TargetPath, asset.Reason)
	}
	if len(requests) > 0 {
		for _, request := range requests {
			if err := g.copyHostBinary(request.hostPath, request.targetPath, true); err != nil {
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

	lddReport, err := lddDependencyReport(sourcePath)
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

func (g *generator) copyHostBinaryIfMissing(sourcePath string, targetPath string, copyDependencies bool) error {
	if _, exists := g.copiedTargets[targetPath]; exists {
		return nil
	}
	if _, err := os.Lstat(g.rootPath(targetPath)); err == nil {
		g.copiedTargets[targetPath] = struct{}{}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat target path %q: %w", targetPath, err)
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
	if err := os.Symlink(linkTarget, target); err != nil {
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
			if err := os.Symlink(linkTarget, targetPath); err != nil && !errors.Is(err, os.ErrExist) {
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
	if _, exists := g.copiedTargets[targetPath]; exists {
		return nil
	}

	info, err := os.Stat(sourcePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if !optional {
				g.recordMissing(sourcePath, targetPath, "runtime file")
			}
			return nil
		}
		return fmt.Errorf("stat host file %q: %w", sourcePath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("host path %q is a directory; only files are supported", sourcePath)
	}

	target := g.rootPath(targetPath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create parent directory for %q: %w", targetPath, err)
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
		return fmt.Errorf("set target mode for %q: %w", targetPath, err)
	}

	g.copiedTargets[targetPath] = struct{}{}
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
