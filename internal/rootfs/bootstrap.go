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

	"github.com/DemonGiggle/mirage/internal/hostenv"
)

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

func bootstrapDebianBaseRootfs(root string, architecture string, release string, extraPackages []string, strategy bootstrapStrategy, logOutput io.Writer) error {
	includePackages := append([]string{mmdebstrapIncludePackageList}, extraPackages...)
	includeArg := strings.Join(includePackages, ",")
	args := []string{
		"--architectures=" + architecture,
		"--variant=minbase",
		`--aptopt=APT::Install-Recommends "false"`,
		"--include=" + includeArg,
		release,
		root,
		debianMirror,
	}
	if os.Getenv(testSkipBootstrapEnv) == "1" {
		loggedArgs := slices.Clone(args)
		if strategy.hostEnvironment() == hostenv.Rootless {
			loggedArgs = append([]string{"--mode=unshare", "--format=tar"}, loggedArgs...)
		}
		logCommand(logOutput, "mmdebstrap", loggedArgs...)
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

	return strategy.bootstrap(root, args, logOutput)
}

func normalizeDebianRelease(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return defaultDebianRelease, nil
	}
	if strings.ContainsAny(value, " \t\r\n") {
		return "", fmt.Errorf("debian release %q must not contain whitespace", raw)
	}
	return value, nil
}

func normalizeExtraPackages(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	basePackages := make(map[string]struct{})
	for _, name := range strings.Split(mmdebstrapIncludePackageList, ",") {
		basePackages[name] = struct{}{}
	}

	seen := make(map[string]struct{})
	var packages []string
	for _, item := range raw {
		name := strings.TrimSpace(item)
		if name == "" {
			return nil, errors.New("extra package list contains an empty package name")
		}
		if strings.Contains(name, ",") {
			return nil, fmt.Errorf("extra package %q must not contain commas", name)
		}
		if !debianPackageNamePattern.MatchString(name) {
			return nil, fmt.Errorf("extra package %q is not a valid Debian package name", name)
		}
		if _, ok := basePackages[name]; ok {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		packages = append(packages, name)
	}
	return packages, nil
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

func writeMinimalAptConfig(root string, environment hostenv.Kind, logOutput io.Writer) error {
	target := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(minimalAptConfigPath, "/")))
	logAptConfigCommand(logOutput, environment, target)
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

func logAptConfigCommand(w io.Writer, environment hostenv.Kind, target string) {
	if w == nil {
		return
	}
	command := "tee"
	if environment.IsRoot() {
		command = "sudo tee"
	}
	_, _ = fmt.Fprintf(w, "command: %s %s >/dev/null <<'EOF'\n", command, strconvQuote(target))
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
