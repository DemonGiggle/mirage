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
	"slices"
	"strings"
)

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
