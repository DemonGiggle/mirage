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
)

// EnsureNSSRuntime backfills the host lookup modules referenced by the guest
// hosts database so hostname resolution continues to work inside generated rootfses.
func EnsureNSSRuntime(rootfsPath string) error {
	root, err := filepath.Abs(rootfsPath)
	if err != nil {
		return fmt.Errorf("resolve rootfs path %q: %w", rootfsPath, err)
	}

	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("stat rootfs %q: %w", root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("rootfs %q is not a directory", root)
	}

	modules, err := requiredNSSModules(root)
	if err != nil {
		return err
	}
	if len(modules) == 0 {
		return nil
	}

	generator := generator{
		outputRoot:    root,
		copiedTargets: make(map[string]struct{}),
		copiedTrees:   make(map[string]struct{}),
	}
	for _, module := range modules {
		paths, err := resolveNSSModuleSupportPaths(module)
		if err != nil {
			return err
		}
		for _, path := range paths {
			if err := generator.copyHostBinaryIfMissing(path, path, true); err != nil {
				return err
			}
		}
	}
	return nil
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
		if strings.TrimSpace(database) != "hosts" {
			continue
		}
		for _, field := range strings.Fields(sources) {
			if field == "" || field == "[" || field == "]" {
				continue
			}
			if strings.HasPrefix(field, "[") || strings.HasSuffix(field, "]") || strings.Contains(field, "=") {
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
	modulePath, err := lookupSharedLibraryPath(fmt.Sprintf("libnss_%s.so.2", module))
	if err != nil {
		return nil, fmt.Errorf("resolve host NSS module %q: %w", module, err)
	}

	paths := []string{modulePath}
	dependencies, err := lddDependencies(modulePath)
	if err != nil {
		return nil, fmt.Errorf("resolve host NSS module dependencies for %q: %w", modulePath, err)
	}
	for _, dependency := range dependencies {
		if slices.Contains(paths, dependency) {
			continue
		}
		paths = append(paths, dependency)
	}
	return paths, nil
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
	output, err := exec.Command("ldconfig", "-p").Output()
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
	shellPath, err := exec.LookPath("sh")
	if err != nil {
		return nil
	}
	dependencies, err := lddDependencies(shellPath)
	if err != nil {
		return nil
	}
	var prefixes []string
	for _, dependency := range dependencies {
		dir := filepath.Dir(dependency)
		if slices.Contains(prefixes, dir) {
			continue
		}
		prefixes = append(prefixes, dir)
	}
	return prefixes
}

func scoreLibraryPath(path string, preferredPrefixes []string) int {
	for idx, prefix := range preferredPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return len(preferredPrefixes) - idx + 100
		}
	}
	return 0
}
