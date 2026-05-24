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

func Generate(outputRoot string, template Template) error {
	if err := ValidateTemplate(template); err != nil {
		return err
	}

	root, err := filepath.Abs(outputRoot)
	if err != nil {
		return fmt.Errorf("resolve output rootfs %q: %w", outputRoot, err)
	}
	if err := prepareOutputRoot(root); err != nil {
		return err
	}

	generator := generator{
		outputRoot:    root,
		copiedTargets: make(map[string]struct{}),
	}
	for _, dir := range template.Directories {
		if err := generator.ensureDirectory(dir); err != nil {
			return err
		}
	}
	for _, runtimeFile := range template.RuntimeFiles {
		if err := generator.copyRuntimeFile(runtimeFile); err != nil {
			return err
		}
	}
	for _, binary := range template.Binaries {
		if err := generator.copyTemplateBinary(binary); err != nil {
			return err
		}
	}
	return nil
}

type generator struct {
	outputRoot    string
	copiedTargets map[string]struct{}
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

func (g generator) ensureDirectory(dir Directory) error {
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

func (g generator) copyRuntimeFile(runtimeFile RuntimeFile) error {
	return g.copyHostFile(runtimeFile.HostPath, runtimeFile.TargetPath, runtimeFile.Optional)
}

func (g generator) copyTemplateBinary(binary Binary) error {
	source, err := resolveBinarySource(binary)
	if err != nil {
		return err
	}
	return g.copyHostBinary(source, binary.TargetPath)
}

func resolveBinarySource(binary Binary) (string, error) {
	if binary.HostPath != "" {
		return binary.HostPath, nil
	}
	source, err := exec.LookPath(binary.LookupName)
	if err != nil {
		return "", fmt.Errorf("resolve binary %q on host PATH: %w", binary.LookupName, err)
	}
	return source, nil
}

func (g generator) copyHostBinary(sourcePath string, targetPath string) error {
	if err := g.copyHostFile(sourcePath, targetPath, false); err != nil {
		return err
	}

	requests, err := shebangRequests(sourcePath)
	if err != nil {
		return err
	}
	if len(requests) > 0 {
		for _, request := range requests {
			if err := g.copyHostBinary(request.hostPath, request.targetPath); err != nil {
				return err
			}
		}
		return nil
	}

	dependencies, err := lddDependencies(sourcePath)
	if err != nil {
		return err
	}
	for _, dependency := range dependencies {
		if err := g.copyHostFile(dependency, dependency, false); err != nil {
			return err
		}
	}
	return nil
}

type copyRequest struct {
	hostPath   string
	targetPath string
}

func shebangRequests(path string) ([]copyRequest, error) {
	line, ok, err := readShebang(path)
	if err != nil || !ok {
		return nil, err
	}

	fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, "#!")))
	if len(fields) == 0 {
		return nil, nil
	}
	interpreterPath := fields[0]
	if !filepath.IsAbs(interpreterPath) {
		return nil, fmt.Errorf("script %q uses non-absolute shebang interpreter %q", path, interpreterPath)
	}

	requests := []copyRequest{{hostPath: interpreterPath, targetPath: interpreterPath}}
	if interpreterPath != "/usr/bin/env" {
		return requests, nil
	}

	lookupName, ok := envLookupName(fields[1:])
	if !ok {
		return requests, nil
	}
	resolved, err := exec.LookPath(lookupName)
	if err != nil {
		return nil, fmt.Errorf("resolve shebang target %q on host PATH: %w", lookupName, err)
	}
	requests = append(requests, copyRequest{hostPath: resolved, targetPath: resolved})
	return requests, nil
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
	cmd := exec.Command("ldd", path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(output))
		if strings.Contains(text, "not a dynamic executable") || strings.Contains(text, "statically linked") {
			return nil, nil
		}
		return nil, fmt.Errorf("resolve library dependencies for %q: %w: %s", path, err, text)
	}
	return parseLDDOutput(output)
}

func parseLDDOutput(output []byte) ([]string, error) {
	var dependencies []string
	seen := make(map[string]struct{})

	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "linux-vdso.") || line == "statically linked" || line == "not a dynamic executable" {
			continue
		}

		if strings.Contains(line, "=> not found") {
			return nil, fmt.Errorf("missing shared library dependency: %s", line)
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
		return nil, fmt.Errorf("scan ldd output: %w", err)
	}
	return dependencies, nil
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

func (g generator) copyHostFile(sourcePath string, targetPath string, optional bool) error {
	if _, exists := g.copiedTargets[targetPath]; exists {
		return nil
	}

	info, err := os.Stat(sourcePath)
	if err != nil {
		if optional && errors.Is(err, os.ErrNotExist) {
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

func (g generator) rootPath(path string) string {
	return filepath.Join(g.outputRoot, strings.TrimPrefix(path, "/"))
}
