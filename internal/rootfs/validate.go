package rootfs

import (
	"debug/elf"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

const defaultCommandPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

type ValidationReport struct {
	Rootfs          string
	ResolvedCommand string
	WorkingDir      string
	Interpreter     string
	DependencyCount int
	RuntimePaths    []RuntimePathStatus
}

type RuntimePathStatus struct {
	Path      string
	Status    string
	Creatable bool
}

func ValidateRootfs(rootfsPath string, command string, cwd string) (ValidationReport, error) {
	root, err := filepath.Abs(rootfsPath)
	if err != nil {
		return ValidationReport{}, fmt.Errorf("resolve rootfs path %q: %w", rootfsPath, err)
	}

	report := ValidationReport{Rootfs: root}
	info, err := os.Stat(root)
	if err != nil {
		return report, fmt.Errorf("stat rootfs %q: %w", root, err)
	}
	if !info.IsDir() {
		return report, fmt.Errorf("rootfs %q is not a directory", root)
	}

	var problems []error
	report.RuntimePaths, problems = validateRuntimePaths(root, problems)

	if cwd != "" {
		guestCWD := normalizeGuestPath(cwd)
		cwdInfo, err := os.Stat(rootPath(root, guestCWD))
		if err != nil {
			problems = append(problems, fmt.Errorf("working directory %q does not exist inside rootfs %q", cwd, root))
		} else if !cwdInfo.IsDir() {
			problems = append(problems, fmt.Errorf("working directory %q inside rootfs %q is not a directory", cwd, root))
		} else {
			report.WorkingDir = guestCWD
		}
	}

	if command != "" {
		resolvedCommand, err := resolveCommandInRootfs(root, command)
		if err != nil {
			problems = append(problems, err)
		} else {
			report.ResolvedCommand = resolvedCommand
			state := validationState{
				visitedCommands: make(map[string]struct{}),
				libraries:       make(map[string]struct{}),
			}
			if err := validateGuestBinary(root, resolvedCommand, &report, &state); err != nil {
				problems = append(problems, err)
			}
			report.DependencyCount = len(state.libraries)
		}
	}

	if len(problems) == 0 {
		return report, nil
	}
	return report, errors.Join(problems...)
}

type validationState struct {
	visitedCommands map[string]struct{}
	libraries       map[string]struct{}
}

func validateRuntimePaths(root string, problems []error) ([]RuntimePathStatus, []error) {
	required := []string{"/proc", "/tmp", "/run"}
	statuses := make([]RuntimePathStatus, 0, len(required))
	for _, guestPath := range required {
		target := rootPath(root, guestPath)
		info, err := os.Stat(target)
		switch {
		case err == nil && info.IsDir():
			statuses = append(statuses, RuntimePathStatus{Path: guestPath, Status: "present"})
		case err == nil:
			statuses = append(statuses, RuntimePathStatus{Path: guestPath, Status: "invalid"})
			problems = append(problems, fmt.Errorf("runtime path %q exists inside rootfs %q but is not a directory", guestPath, root))
		case errors.Is(err, os.ErrNotExist):
			creatable := canCreatePath(target)
			status := "missing"
			if creatable {
				status = "creatable"
			} else {
				problems = append(problems, fmt.Errorf("runtime path %q is missing inside rootfs %q and cannot be created", guestPath, root))
			}
			statuses = append(statuses, RuntimePathStatus{Path: guestPath, Status: status, Creatable: creatable})
		default:
			statuses = append(statuses, RuntimePathStatus{Path: guestPath, Status: "error"})
			problems = append(problems, fmt.Errorf("stat runtime path %q inside rootfs %q: %w", guestPath, root, err))
		}
	}
	return statuses, problems
}

func canCreatePath(path string) bool {
	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() {
		return false
	}
	return syscall.Access(parent, 0o2) == nil
}

func resolveCommandInRootfs(root string, command string) (string, error) {
	if strings.ContainsRune(command, os.PathSeparator) {
		guestPath := normalizeGuestPath(command)
		if _, err := os.Stat(rootPath(root, guestPath)); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return "", fmt.Errorf("command %q does not exist inside rootfs %q", command, root)
			}
			return "", fmt.Errorf("stat command %q inside rootfs %q: %w", command, root, err)
		}
		return guestPath, nil
	}

	searchPath := os.Getenv("PATH")
	if searchPath == "" {
		searchPath = defaultCommandPath
	}
	for _, dir := range strings.Split(searchPath, ":") {
		if dir == "" {
			continue
		}
		guestPath := normalizeGuestPath(filepath.Join(dir, command))
		if _, err := os.Stat(rootPath(root, guestPath)); err == nil {
			return guestPath, nil
		}
	}
	return "", fmt.Errorf("command %q was not found inside rootfs %q", command, root)
}

func validateGuestBinary(root string, guestPath string, report *ValidationReport, state *validationState) error {
	if _, seen := state.visitedCommands[guestPath]; seen {
		return nil
	}
	state.visitedCommands[guestPath] = struct{}{}

	hostPath := rootPath(root, guestPath)
	info, err := os.Stat(hostPath)
	if err != nil {
		return fmt.Errorf("command %q does not exist inside rootfs %q", guestPath, root)
	}
	if info.IsDir() {
		return fmt.Errorf("resolved command %q inside rootfs %q is a directory", guestPath, root)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("resolved command %q inside rootfs %q is not executable", guestPath, root)
	}

	shebang, ok, err := readShebang(hostPath)
	if err != nil {
		return err
	}
	if ok {
		return validateShebang(root, guestPath, shebang, report, state)
	}

	inspection, isELF, err := inspectELFBinary(hostPath, guestPath)
	if err != nil {
		return err
	}
	if !isELF {
		return nil
	}
	if inspection.Interpreter != "" {
		if report.Interpreter == "" {
			report.Interpreter = inspection.Interpreter
		}
		if _, err := os.Stat(rootPath(root, inspection.Interpreter)); err != nil {
			return fmt.Errorf("ELF interpreter %q for command %q is missing inside rootfs %q", inspection.Interpreter, reportCommand(report, guestPath), root)
		}
		if err := validateGuestBinary(root, inspection.Interpreter, report, state); err != nil {
			return err
		}
	}

	var missingLibraries []string
	for _, library := range inspection.Libraries {
		foundPath, ok, err := findLibraryInRootfs(root, library, inspection.SearchPaths)
		if err != nil {
			return err
		}
		if !ok {
			missingLibraries = append(missingLibraries, library)
			continue
		}
		state.libraries[foundPath] = struct{}{}
	}
	if len(missingLibraries) > 0 {
		return fmt.Errorf("shared library dependencies for command %q are missing inside rootfs %q: %s", reportCommand(report, guestPath), root, strings.Join(missingLibraries, ", "))
	}
	return nil
}

func reportCommand(report *ValidationReport, fallback string) string {
	if report != nil && report.ResolvedCommand != "" {
		return report.ResolvedCommand
	}
	return fallback
}

func validateShebang(root string, guestPath string, shebang string, report *ValidationReport, state *validationState) error {
	fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(shebang, "#!")))
	if len(fields) == 0 {
		return nil
	}
	interpreterPath := fields[0]
	if !filepath.IsAbs(interpreterPath) {
		return fmt.Errorf("script %q inside rootfs %q uses non-absolute shebang interpreter %q", guestPath, root, interpreterPath)
	}
	if err := validateGuestBinary(root, interpreterPath, report, state); err != nil {
		return err
	}
	if interpreterPath != "/usr/bin/env" {
		return nil
	}

	lookupName, ok := envLookupName(fields[1:])
	if !ok {
		return nil
	}
	resolved, err := resolveCommandInRootfs(root, lookupName)
	if err != nil {
		return fmt.Errorf("shebang target %q for command %q is missing inside rootfs %q", lookupName, guestPath, root)
	}
	return validateGuestBinary(root, resolved, report, state)
}

type elfInspection struct {
	Interpreter string
	Libraries   []string
	SearchPaths []string
}

func inspectELFBinary(hostPath string, guestPath string) (elfInspection, bool, error) {
	file, err := elf.Open(hostPath)
	if err != nil {
		var formatErr *elf.FormatError
		if errors.As(err, &formatErr) {
			return elfInspection{}, false, nil
		}
		return elfInspection{}, false, fmt.Errorf("inspect ELF binary %q: %w", hostPath, err)
	}
	defer file.Close()

	libraries, err := file.ImportedLibraries()
	if err != nil {
		return elfInspection{}, false, fmt.Errorf("read imported libraries from %q: %w", hostPath, err)
	}

	var dynamicSearchPaths []string
	for _, tag := range []elf.DynTag{elf.DT_RUNPATH, elf.DT_RPATH} {
		paths, err := file.DynString(tag)
		if err != nil {
			return elfInspection{}, false, fmt.Errorf("read ELF search paths from %q: %w", hostPath, err)
		}
		for _, raw := range paths {
			for _, entry := range strings.Split(raw, ":") {
				entry = strings.TrimSpace(entry)
				if entry == "" {
					continue
				}
				dynamicSearchPaths = append(dynamicSearchPaths, expandSearchPath(entry, guestPath))
			}
		}
	}

	interpreter, err := elfInterpreter(file)
	if err != nil {
		return elfInspection{}, false, err
	}

	searchPaths := append([]string{}, dynamicSearchPaths...)
	if interpreter != "" {
		searchPaths = append(searchPaths, filepath.Dir(interpreter))
	}
	searchPaths = append(searchPaths,
		"/lib",
		"/lib64",
		"/usr/lib",
		"/usr/lib64",
		"/usr/local/lib",
	)

	return elfInspection{
		Interpreter: interpreter,
		Libraries:   libraries,
		SearchPaths: dedupePaths(searchPaths),
	}, true, nil
}

func elfInterpreter(file *elf.File) (string, error) {
	for _, prog := range file.Progs {
		if prog.Type != elf.PT_INTERP {
			continue
		}
		reader := prog.Open()
		data, err := io.ReadAll(reader)
		if err != nil {
			return "", fmt.Errorf("read ELF interpreter: %w", err)
		}
		return strings.TrimRight(string(data), "\x00"), nil
	}
	return "", nil
}

func expandSearchPath(path string, guestBinary string) string {
	origin := filepath.Dir(guestBinary)
	path = strings.ReplaceAll(path, "$ORIGIN", origin)
	if !filepath.IsAbs(path) {
		path = filepath.Clean(filepath.Join(origin, path))
	}
	return normalizeGuestPath(path)
}

func findLibraryInRootfs(root string, library string, searchPaths []string) (string, bool, error) {
	if filepath.IsAbs(library) {
		if _, err := os.Stat(rootPath(root, library)); err == nil {
			return library, true, nil
		}
		return "", false, nil
	}

	for _, searchPath := range searchPaths {
		candidate := normalizeGuestPath(filepath.Join(searchPath, library))
		if _, err := os.Stat(rootPath(root, candidate)); err == nil {
			return candidate, true, nil
		}
	}

	for _, prefix := range []string{"/lib", "/lib64", "/usr/lib", "/usr/lib64", "/usr/local/lib"} {
		base := rootPath(root, prefix)
		info, err := os.Stat(base)
		if errors.Is(err, os.ErrNotExist) || (err == nil && !info.IsDir()) {
			continue
		}
		if err != nil {
			return "", false, fmt.Errorf("scan library prefix %q: %w", prefix, err)
		}
		found, err := walkLibraryPrefix(root, prefix, library)
		if err != nil {
			return "", false, err
		}
		if found != "" {
			return found, true, nil
		}
	}

	return "", false, nil
}

func walkLibraryPrefix(root string, prefix string, library string) (string, error) {
	var found string
	stop := errors.New("stop library walk")

	err := filepath.WalkDir(rootPath(root, prefix), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Name() != library {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		found = normalizeGuestPath("/" + filepath.ToSlash(relative))
		return stop
	})
	if err != nil && !errors.Is(err, stop) {
		return "", fmt.Errorf("walk library prefix %q: %w", prefix, err)
	}
	return found, nil
}

func rootPath(root string, guestPath string) string {
	return filepath.Join(root, strings.TrimPrefix(normalizeGuestPath(guestPath), "/"))
}

func normalizeGuestPath(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean("/" + path)
}

func dedupePaths(paths []string) []string {
	var out []string
	for _, path := range paths {
		if path == "" {
			continue
		}
		path = normalizeGuestPath(path)
		if slices.Contains(out, path) {
			continue
		}
		out = append(out, path)
	}
	return out
}
