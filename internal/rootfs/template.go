package rootfs

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

const TemplateVersionV1 = "v1"

type Template struct {
	Version        string          `json:"version"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Directories    []Directory     `json:"directories,omitempty"`
	Binaries       []Binary        `json:"binaries,omitempty"`
	RuntimeFiles   []RuntimeFile   `json:"runtime_files,omitempty"`
	GeneratedFiles []GeneratedFile `json:"generated_files,omitempty"`
}

type Directory struct {
	Path string `json:"path"`
	Mode uint32 `json:"mode,omitempty"`
}

type Binary struct {
	TargetPath       string `json:"target_path"`
	HostPath         string `json:"host_path,omitempty"`
	LookupName       string `json:"lookup_name,omitempty"`
	CopyDependencies bool   `json:"copy_dependencies,omitempty"`
}

type RuntimeFile struct {
	HostPath   string `json:"host_path"`
	TargetPath string `json:"target_path"`
	Optional   bool   `json:"optional,omitempty"`
}

type GeneratedFile struct {
	TargetPath string `json:"target_path"`
	Content    string `json:"content,omitempty"`
	Mode       uint32 `json:"mode,omitempty"`
}

var BuiltInTemplates = map[string]Template{
	"basic":            basicTemplate(),
	"node":             nodeTemplate(),
	"python":           pythonTemplate(),
	"openclaw":         openclawTemplate(),
	"openclaw-systemd": openclawSystemdTemplate(),
}

func TemplateNames() []string {
	names := make([]string, 0, len(BuiltInTemplates))
	for name := range BuiltInTemplates {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func LookupTemplate(name string) (Template, bool) {
	template, ok := BuiltInTemplates[name]
	if !ok {
		return Template{}, false
	}
	return cloneTemplate(template), true
}

func AvailableTemplates() map[string]Template {
	templates := make(map[string]Template, len(BuiltInTemplates))
	for name, template := range BuiltInTemplates {
		templates[name] = cloneTemplate(template)
	}
	return templates
}

func ValidateTemplate(template Template) error {
	var problems []error
	seenDirectories := make(map[string]struct{}, len(template.Directories))
	seenBinaries := make(map[string]struct{}, len(template.Binaries))
	seenFiles := make(map[string]struct{}, len(template.RuntimeFiles)+len(template.GeneratedFiles))
	if template.Version != TemplateVersionV1 {
		problems = append(problems, fmt.Errorf("template %q must declare version %q", template.Name, TemplateVersionV1))
	}
	if strings.TrimSpace(template.Name) == "" {
		problems = append(problems, errors.New("template name is required"))
	}
	if strings.TrimSpace(template.Description) == "" {
		problems = append(problems, fmt.Errorf("template %q description is required", template.Name))
	}
	for idx, dir := range template.Directories {
		if !filepath.IsAbs(dir.Path) {
			problems = append(problems, fmt.Errorf("template %q directory %d path %q must be absolute", template.Name, idx, dir.Path))
		}
		if _, exists := seenDirectories[dir.Path]; exists {
			problems = append(problems, fmt.Errorf("template %q directory path %q is duplicated", template.Name, dir.Path))
			continue
		}
		seenDirectories[dir.Path] = struct{}{}
	}
	for idx, binary := range template.Binaries {
		if !filepath.IsAbs(binary.TargetPath) {
			problems = append(problems, fmt.Errorf("template %q binary %d target path %q must be absolute", template.Name, idx, binary.TargetPath))
		}
		if _, exists := seenBinaries[binary.TargetPath]; exists {
			problems = append(problems, fmt.Errorf("template %q binary target path %q is duplicated", template.Name, binary.TargetPath))
			continue
		}
		seenBinaries[binary.TargetPath] = struct{}{}
		hasHostPath := binary.HostPath != ""
		hasLookup := binary.LookupName != ""
		switch {
		case hasHostPath == hasLookup:
			problems = append(problems, fmt.Errorf("template %q binary %d must set exactly one of host_path or lookup_name", template.Name, idx))
		case hasHostPath && !filepath.IsAbs(binary.HostPath):
			problems = append(problems, fmt.Errorf("template %q binary %d host path %q must be absolute", template.Name, idx, binary.HostPath))
		case hasLookup && strings.ContainsRune(binary.LookupName, filepath.Separator):
			problems = append(problems, fmt.Errorf("template %q binary %d lookup name %q must be a PATH name, not a path", template.Name, idx, binary.LookupName))
		}
	}
	for idx, runtimeFile := range template.RuntimeFiles {
		if !filepath.IsAbs(runtimeFile.HostPath) {
			problems = append(problems, fmt.Errorf("template %q runtime file %d host path %q must be absolute", template.Name, idx, runtimeFile.HostPath))
		}
		if !filepath.IsAbs(runtimeFile.TargetPath) {
			problems = append(problems, fmt.Errorf("template %q runtime file %d target path %q must be absolute", template.Name, idx, runtimeFile.TargetPath))
		}
		if _, exists := seenFiles[runtimeFile.TargetPath]; exists {
			problems = append(problems, fmt.Errorf("template %q runtime file target path %q is duplicated", template.Name, runtimeFile.TargetPath))
			continue
		}
		seenFiles[runtimeFile.TargetPath] = struct{}{}
	}
	for idx, generatedFile := range template.GeneratedFiles {
		if !filepath.IsAbs(generatedFile.TargetPath) {
			problems = append(problems, fmt.Errorf("template %q generated file %d target path %q must be absolute", template.Name, idx, generatedFile.TargetPath))
		}
		if _, exists := seenFiles[generatedFile.TargetPath]; exists {
			problems = append(problems, fmt.Errorf("template %q generated file target path %q is duplicated", template.Name, generatedFile.TargetPath))
			continue
		}
		seenFiles[generatedFile.TargetPath] = struct{}{}
	}
	if len(problems) == 0 {
		return nil
	}
	return errors.Join(problems...)
}

func basicTemplate() Template {
	return Template{
		Version:     TemplateVersionV1,
		Name:        "basic",
		Description: "Small runnable base rootfs with shell and core inspection tools.",
		Directories: append([]Directory{}, commonRuntimeDirectories...),
		Binaries: []Binary{
			lookupBinary("sh", "/bin/sh"),
			lookupBinary("ls", "/bin/ls"),
			lookupBinary("cat", "/bin/cat"),
			lookupBinary("mkdir", "/bin/mkdir"),
			lookupBinary("pwd", "/bin/pwd"),
			lookupBinary("rm", "/bin/rm"),
			lookupBinary("true", "/bin/true"),
			lookupBinary("false", "/bin/false"),
			lookupBinary("env", "/usr/bin/env"),
		},
		RuntimeFiles: append([]RuntimeFile{}, commonRuntimeFiles...),
	}
}

func nodeTemplate() Template {
	template := basicTemplate()
	template.Name = "node"
	template.Description = "Node.js-oriented rootfs template with npm and HTTPS trust material."
	template.Directories = appendUniqueDirectories(template.Directories,
		directory("/workspace", 0o755),
		directory("/etc/ssl/certs", 0o755),
	)
	template.Binaries = append(template.Binaries,
		lookupBinary("node", "/usr/bin/node"),
		lookupBinary("npm", "/usr/bin/npm"),
		lookupBinary("npx", "/usr/bin/npx"),
	)
	template.RuntimeFiles = appendUniqueRuntimeFiles(template.RuntimeFiles,
		optionalRuntimeFile("/etc/ssl/certs/ca-certificates.crt", "/etc/ssl/certs/ca-certificates.crt"),
		optionalRuntimeFile("/etc/ssl/cert.pem", "/etc/ssl/cert.pem"),
		optionalRuntimeFile("/etc/pki/tls/certs/ca-bundle.crt", "/etc/pki/tls/certs/ca-bundle.crt"),
	)
	return template
}

func pythonTemplate() Template {
	template := basicTemplate()
	template.Name = "python"
	template.Description = "Python-oriented rootfs template with pip and common HTTPS trust material."
	template.Directories = appendUniqueDirectories(template.Directories,
		directory("/workspace", 0o755),
		directory("/etc/ssl/certs", 0o755),
	)
	template.Binaries = append(template.Binaries,
		lookupBinary("python3", "/usr/bin/python3"),
		lookupBinary("pip3", "/usr/bin/pip3"),
	)
	template.RuntimeFiles = appendUniqueRuntimeFiles(template.RuntimeFiles,
		optionalRuntimeFile("/etc/ssl/certs/ca-certificates.crt", "/etc/ssl/certs/ca-certificates.crt"),
		optionalRuntimeFile("/etc/ssl/cert.pem", "/etc/ssl/cert.pem"),
		optionalRuntimeFile("/etc/pki/tls/certs/ca-bundle.crt", "/etc/pki/tls/certs/ca-bundle.crt"),
	)
	return template
}

func openclawTemplate() Template {
	template := nodeTemplate()
	template.Name = "openclaw"
	template.Description = "OpenClaw-oriented rootfs template with Node.js, Git, and a writable workspace."
	template.Directories = appendUniqueDirectories(template.Directories,
		directory("/workspace", 0o755),
		directory("/home", 0o755),
	)
	template.Binaries = append(template.Binaries,
		lookupBinary("bash", "/bin/bash"),
		lookupBinary("git", "/usr/bin/git"),
	)
	return template
}

func openclawSystemdTemplate() Template {
	template := openclawTemplate()
	template.Name = "openclaw-systemd"
	template.Description = "OpenClaw-oriented rootfs template with guest systemd tooling and systemd-ready directories."
	template.Directories = appendUniqueDirectories(template.Directories,
		directory("/etc/systemd/system", 0o755),
		directory("/usr/lib/systemd/system", 0o755),
		directory("/var/lib/systemd", 0o755),
		directory("/var/log/journal", 0o755),
	)
	template.Binaries = append(template.Binaries,
		lookupBinary("systemd", "/usr/bin/systemd"),
		lookupBinary("systemctl", "/usr/bin/systemctl"),
		lookupBinary("journalctl", "/usr/bin/journalctl"),
		lookupBinary("systemd-tmpfiles", "/usr/bin/systemd-tmpfiles"),
	)
	template.RuntimeFiles = appendUniqueRuntimeFiles(template.RuntimeFiles,
		runtimeFile("/etc/passwd", "/etc/passwd"),
		runtimeFile("/etc/group", "/etc/group"),
		optionalRuntimeFile("/etc/os-release", "/etc/os-release"),
	)
	template.GeneratedFiles = appendUniqueGeneratedFiles(template.GeneratedFiles,
		generatedFile("/etc/machine-id", "", 0o644),
	)
	return template
}

var commonRuntimeDirectories = []Directory{
	directory("/proc", 0o755),
	directory("/tmp", 0o1777),
	directory("/run", 0o755),
}

var commonRuntimeFiles = []RuntimeFile{
	runtimeFile("/etc/hosts", "/etc/hosts"),
	runtimeFile("/etc/resolv.conf", "/etc/resolv.conf"),
	runtimeFile("/etc/nsswitch.conf", "/etc/nsswitch.conf"),
}

func cloneTemplate(template Template) Template {
	template.Directories = slices.Clone(template.Directories)
	template.Binaries = slices.Clone(template.Binaries)
	template.RuntimeFiles = slices.Clone(template.RuntimeFiles)
	template.GeneratedFiles = slices.Clone(template.GeneratedFiles)
	return template
}

func directory(path string, mode uint32) Directory {
	return Directory{Path: path, Mode: mode}
}

func lookupBinary(name string, targetPath string) Binary {
	return Binary{
		TargetPath:       targetPath,
		LookupName:       name,
		CopyDependencies: true,
	}
}

func runtimeFile(hostPath string, targetPath string) RuntimeFile {
	return RuntimeFile{
		HostPath:   hostPath,
		TargetPath: targetPath,
	}
}

func optionalRuntimeFile(hostPath string, targetPath string) RuntimeFile {
	return RuntimeFile{
		HostPath:   hostPath,
		TargetPath: targetPath,
		Optional:   true,
	}
}

func generatedFile(targetPath string, content string, mode uint32) GeneratedFile {
	return GeneratedFile{
		TargetPath: targetPath,
		Content:    content,
		Mode:       mode,
	}
}

func appendUniqueDirectories(existing []Directory, extra ...Directory) []Directory {
	for _, dir := range extra {
		if slices.ContainsFunc(existing, func(candidate Directory) bool {
			return candidate.Path == dir.Path
		}) {
			continue
		}
		existing = append(existing, dir)
	}
	return existing
}

func appendUniqueRuntimeFiles(existing []RuntimeFile, extra ...RuntimeFile) []RuntimeFile {
	for _, runtimeFile := range extra {
		if slices.ContainsFunc(existing, func(candidate RuntimeFile) bool {
			return candidate.TargetPath == runtimeFile.TargetPath && candidate.HostPath == runtimeFile.HostPath
		}) {
			continue
		}
		existing = append(existing, runtimeFile)
	}
	return existing
}

func appendUniqueGeneratedFiles(existing []GeneratedFile, extra ...GeneratedFile) []GeneratedFile {
	for _, generatedFile := range extra {
		if slices.ContainsFunc(existing, func(candidate GeneratedFile) bool {
			return candidate.TargetPath == generatedFile.TargetPath
		}) {
			continue
		}
		existing = append(existing, generatedFile)
	}
	return existing
}
