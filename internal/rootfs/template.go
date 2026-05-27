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
	Version        string          `json:"version" yaml:"version"`
	Name           string          `json:"name" yaml:"name"`
	Description    string          `json:"description" yaml:"description"`
	Directories    []Directory     `json:"directories,omitempty" yaml:"directories,omitempty"`
	Binaries       []Binary        `json:"binaries,omitempty" yaml:"binaries,omitempty"`
	RuntimeTrees   []RuntimeTree   `json:"runtime_trees,omitempty" yaml:"runtime_trees,omitempty"`
	RuntimeFiles   []RuntimeFile   `json:"runtime_files,omitempty" yaml:"runtime_files,omitempty"`
	GeneratedFiles []GeneratedFile `json:"generated_files,omitempty" yaml:"generated_files,omitempty"`
}

type Directory struct {
	Path string `json:"path" yaml:"path"`
	Mode uint32 `json:"mode,omitempty" yaml:"mode,omitempty"`
}

type Binary struct {
	TargetPath       string `json:"target_path" yaml:"target_path"`
	HostPath         string `json:"host_path,omitempty" yaml:"host_path,omitempty"`
	LookupName       string `json:"lookup_name,omitempty" yaml:"lookup_name,omitempty"`
	CopyDependencies bool   `json:"copy_dependencies,omitempty" yaml:"copy_dependencies,omitempty"`
	Optional         bool   `json:"optional,omitempty" yaml:"optional,omitempty"`
}

type RuntimeTree struct {
	HostPath   string `json:"host_path" yaml:"host_path"`
	TargetPath string `json:"target_path" yaml:"target_path"`
	Optional   bool   `json:"optional,omitempty" yaml:"optional,omitempty"`
}

type RuntimeFile struct {
	HostPath   string `json:"host_path" yaml:"host_path"`
	TargetPath string `json:"target_path" yaml:"target_path"`
	Optional   bool   `json:"optional,omitempty" yaml:"optional,omitempty"`
}

type GeneratedFile struct {
	TargetPath string `json:"target_path" yaml:"target_path"`
	Content    string `json:"content,omitempty" yaml:"content,omitempty"`
	Mode       uint32 `json:"mode,omitempty" yaml:"mode,omitempty"`
}

var BuiltInTemplates = mustLoadBuiltInTemplates()

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
	seenPaths := make(map[string]struct{}, len(template.RuntimeTrees)+len(template.RuntimeFiles)+len(template.GeneratedFiles))
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
	for idx, runtimeTree := range template.RuntimeTrees {
		if !filepath.IsAbs(runtimeTree.HostPath) {
			problems = append(problems, fmt.Errorf("template %q runtime tree %d host path %q must be absolute", template.Name, idx, runtimeTree.HostPath))
		}
		if !filepath.IsAbs(runtimeTree.TargetPath) {
			problems = append(problems, fmt.Errorf("template %q runtime tree %d target path %q must be absolute", template.Name, idx, runtimeTree.TargetPath))
		}
		if _, exists := seenPaths[runtimeTree.TargetPath]; exists {
			problems = append(problems, fmt.Errorf("template %q runtime tree target path %q is duplicated", template.Name, runtimeTree.TargetPath))
			continue
		}
		seenPaths[runtimeTree.TargetPath] = struct{}{}
	}
	for idx, runtimeFile := range template.RuntimeFiles {
		if !filepath.IsAbs(runtimeFile.HostPath) {
			problems = append(problems, fmt.Errorf("template %q runtime file %d host path %q must be absolute", template.Name, idx, runtimeFile.HostPath))
		}
		if !filepath.IsAbs(runtimeFile.TargetPath) {
			problems = append(problems, fmt.Errorf("template %q runtime file %d target path %q must be absolute", template.Name, idx, runtimeFile.TargetPath))
		}
		if _, exists := seenPaths[runtimeFile.TargetPath]; exists {
			problems = append(problems, fmt.Errorf("template %q runtime file target path %q is duplicated", template.Name, runtimeFile.TargetPath))
			continue
		}
		seenPaths[runtimeFile.TargetPath] = struct{}{}
	}
	for idx, generatedFile := range template.GeneratedFiles {
		if !filepath.IsAbs(generatedFile.TargetPath) {
			problems = append(problems, fmt.Errorf("template %q generated file %d target path %q must be absolute", template.Name, idx, generatedFile.TargetPath))
		}
		if _, exists := seenPaths[generatedFile.TargetPath]; exists {
			problems = append(problems, fmt.Errorf("template %q generated file target path %q is duplicated", template.Name, generatedFile.TargetPath))
			continue
		}
		seenPaths[generatedFile.TargetPath] = struct{}{}
	}
	if len(problems) == 0 {
		return nil
	}
	return errors.Join(problems...)
}

func cloneTemplate(template Template) Template {
	template.Directories = slices.Clone(template.Directories)
	template.Binaries = slices.Clone(template.Binaries)
	template.RuntimeTrees = slices.Clone(template.RuntimeTrees)
	template.RuntimeFiles = slices.Clone(template.RuntimeFiles)
	template.GeneratedFiles = slices.Clone(template.GeneratedFiles)
	return template
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

func appendUniqueBinaries(existing []Binary, extra ...Binary) []Binary {
	for _, binary := range extra {
		if slices.ContainsFunc(existing, func(candidate Binary) bool {
			return candidate.TargetPath == binary.TargetPath
		}) {
			continue
		}
		existing = append(existing, binary)
	}
	return existing
}

func appendUniqueRuntimeTrees(existing []RuntimeTree, extra ...RuntimeTree) []RuntimeTree {
	for _, runtimeTree := range extra {
		if slices.ContainsFunc(existing, func(candidate RuntimeTree) bool {
			return candidate.TargetPath == runtimeTree.TargetPath
		}) {
			continue
		}
		existing = append(existing, runtimeTree)
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
