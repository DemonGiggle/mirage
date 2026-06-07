package rootfs

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

var (
	preferredPrefixesOnce  sync.Once
	preferredPrefixesCache []string
)

// EnsureNSSRuntime backfills the host lookup modules referenced by the guest
// NSS databases so identity and hostname resolution continue to work inside
// generated rootfses.
func EnsureNSSRuntime(rootfsPath string) error {
	_, err := EnsureNSSRuntimeWithReport(rootfsPath)
	return err
}

func EnsureNSSRuntimeWithReport(rootfsPath string) (GenerateReport, error) {
	root, err := filepath.Abs(rootfsPath)
	if err != nil {
		return GenerateReport{}, fmt.Errorf("resolve rootfs path %q: %w", rootfsPath, err)
	}

	info, err := os.Stat(root)
	if err != nil {
		return GenerateReport{}, fmt.Errorf("stat rootfs %q: %w", root, err)
	}
	if !info.IsDir() {
		return GenerateReport{}, fmt.Errorf("rootfs %q is not a directory", root)
	}

	modules, err := requiredNSSModules(root)
	if err != nil {
		return GenerateReport{}, err
	}
	if len(modules) == 0 {
		return GenerateReport{}, nil
	}

	generator := generator{
		outputRoot:      root,
		copiedTargets:   make(map[string]struct{}),
		copiedTrees:     make(map[string]struct{}),
		missingReported: make(map[string]struct{}),
		shebangCache:    make(map[string]shebangCacheEntry),
		lddCache:        make(map[string]lddCacheEntry),
	}
	for _, module := range modules {
		report, err := resolveNSSModuleSupportPathsReport(module)
		if err != nil {
			return generator.report, err
		}
		for _, asset := range report.missingAssets {
			generator.recordMissing(asset.Source, asset.TargetPath, asset.Reason)
		}
		for _, path := range report.paths {
			if err := generator.copyHostBinaryIfMissing(path, path, true); err != nil {
				return generator.report, err
			}
		}
	}
	return generator.report, nil
}

func requiredNSSModules(root string) ([]string, error) {
	data, err := os.ReadFile(rootPath(root, "/etc/nsswitch.conf"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read guest nsswitch.conf inside rootfs %q: %w", root, err)
	}

	var modules []string
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		database, sources, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(database) {
		case "passwd", "group", "shadow", "hosts":
		default:
			continue
		}
		insideBracket := false
		for _, field := range strings.Fields(sources) {
			if field == "" {
				continue
			}
			if strings.HasPrefix(field, "[") {
				insideBracket = true
			}
			if insideBracket {
				if strings.HasSuffix(field, "]") {
					insideBracket = false
				}
				continue
			}
			if slices.Contains(modules, field) {
				continue
			}
			modules = append(modules, field)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan guest nsswitch.conf inside rootfs %q: %w", root, err)
	}
	return modules, nil
}

func resolveNSSModuleSupportPaths(module string) ([]string, error) {
	report, err := resolveNSSModuleSupportPathsReport(module)
	if err != nil {
		return nil, err
	}
	if len(report.missingAssets) > 0 {
		return nil, errors.New(report.missingAssets[0].Message())
	}
	return report.paths, nil
}

type nssSupportReport struct {
	paths         []string
	missingAssets []MissingAsset
}

func resolveNSSModuleSupportPathsReport(module string) (nssSupportReport, error) {
	modulePath, err := lookupSharedLibraryPath(fmt.Sprintf("libnss_%s.so.2", module))
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nssSupportReport{
				missingAssets: []MissingAsset{
					{
						Source: fmt.Sprintf("shared library %q", fmt.Sprintf("libnss_%s.so.2", module)),
						Reason: "required by guest hosts database",
					},
				},
			}, nil
		}
		return nssSupportReport{}, fmt.Errorf("resolve host NSS module %q: %w", module, err)
	}

	paths := []string{modulePath}
	lddReport, err := lddDependencyReport(modulePath)
	if err != nil {
		return nssSupportReport{}, fmt.Errorf("resolve host NSS module dependencies for %q: %w", modulePath, err)
	}
	report := nssSupportReport{paths: paths}
	for _, dependency := range lddReport.missing {
		report.missingAssets = append(report.missingAssets, MissingAsset{
			Source: fmt.Sprintf("shared library dependency %q", dependency),
			Reason: fmt.Sprintf("required by NSS module %q", modulePath),
		})
	}
	for _, dependency := range lddReport.paths {
		if slices.Contains(paths, dependency) {
			continue
		}
		paths = append(paths, dependency)
	}
	report.paths = paths
	return report, nil
}

func lookupSharedLibraryPath(name string) (string, error) {
	if path, err := lookupSharedLibraryViaLDConfig(name); err == nil {
		return path, nil
	}

	for _, prefix := range []string{"/lib", "/lib64", "/usr/lib", "/usr/lib64", "/usr/local/lib"} {
		found, err := walkHostLibraryPrefix(prefix, name)
		if err != nil {
			return "", err
		}
		if found != "" {
			return found, nil
		}
	}
	return "", fmt.Errorf("shared library %q not found on host", name)
}

func lookupSharedLibraryViaLDConfig(name string) (string, error) {
	ldconfigPath, err := resolveLDConfigPath()
	if err != nil {
		return "", err
	}

	output, err := exec.Command(ldconfigPath, "-p").Output()
	if err != nil {
		return "", fmt.Errorf("run ldconfig -p: %w", err)
	}

	preferredPrefixes := preferredHostLibraryPrefixes()
	var bestPath string
	bestScore := -1
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, name+" ") {
			continue
		}
		idx := strings.LastIndex(line, " => ")
		if idx < 0 {
			continue
		}
		path := strings.TrimSpace(line[idx+4:])
		if filepath.IsAbs(path) {
			score := scoreLibraryPath(path, preferredPrefixes)
			if score > bestScore {
				bestPath = path
				bestScore = score
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan ldconfig output: %w", err)
	}
	if bestPath != "" {
		return bestPath, nil
	}
	return "", fmt.Errorf("shared library %q not listed by ldconfig", name)
}

func resolveLDConfigPath() (string, error) {
	if path, err := exec.LookPath("ldconfig"); err == nil {
		return path, nil
	}
	for _, fallback := range []string{"/sbin/ldconfig", "/usr/sbin/ldconfig"} {
		info, err := os.Stat(fallback)
		if err == nil && !info.IsDir() {
			return fallback, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat ldconfig fallback %q: %w", fallback, err)
		}
	}
	return "", errors.New("resolve ldconfig: executable not found on PATH or common sbin locations")
}

func walkHostLibraryPrefix(prefix string, name string) (string, error) {
	info, err := os.Stat(prefix)
	if errors.Is(err, os.ErrNotExist) || (err == nil && !info.IsDir()) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("stat host library prefix %q: %w", prefix, err)
	}

	var found string
	stop := errors.New("stop host library walk")
	err = filepath.WalkDir(prefix, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				return nil
			}
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Name() != name {
			return nil
		}
		found = path
		return stop
	})
	if err != nil && !errors.Is(err, stop) {
		return "", fmt.Errorf("walk host library prefix %q: %w", prefix, err)
	}
	return found, nil
}

func preferredHostLibraryPrefixes() []string {
	preferredPrefixesOnce.Do(func() {
		shellPath, err := exec.LookPath("sh")
		if err != nil {
			return
		}
		dependencies, err := lddDependencies(shellPath)
		if err != nil {
			return
		}
		for _, dependency := range dependencies {
			dir := filepath.Dir(dependency)
			if slices.Contains(preferredPrefixesCache, dir) {
				continue
			}
			preferredPrefixesCache = append(preferredPrefixesCache, dir)
		}
	})
	return slices.Clone(preferredPrefixesCache)
}

func scoreLibraryPath(path string, preferredPrefixes []string) int {
	for idx, prefix := range preferredPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return len(preferredPrefixes) - idx + 100
		}
	}
	return 0
}
