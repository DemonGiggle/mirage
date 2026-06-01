package rootfs

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed templates/*.yaml
var builtInTemplateFiles embed.FS

func mustLoadBuiltInTemplates() map[string]Template {
	templates, err := loadTemplatesFromFS(builtInTemplateFiles, "templates")
	if err != nil {
		panic(fmt.Sprintf("load built-in rootfs templates: %v", err))
	}
	return templates
}

func loadTemplatesFromFS(fsys fs.FS, dir string) (map[string]Template, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read template directory %q: %w", dir, err)
	}

	templates := make(map[string]Template, len(entries))
	var found bool
	for _, entry := range entries {
		if entry.IsDir() || path.Ext(entry.Name()) != ".yaml" {
			continue
		}

		found = true
		filePath := path.Join(dir, entry.Name())
		data, err := fs.ReadFile(fsys, filePath)
		if err != nil {
			return nil, fmt.Errorf("read template file %q: %w", filePath, err)
		}

		template, err := parseTemplateYAML(data, filePath)
		if err != nil {
			return nil, err
		}

		expectedName := strings.TrimSuffix(entry.Name(), ".yaml")
		if template.Name != expectedName {
			return nil, fmt.Errorf("template file %q must declare name %q, got %q", filePath, expectedName, template.Name)
		}
		if _, exists := templates[template.Name]; exists {
			return nil, fmt.Errorf("template %q is duplicated", template.Name)
		}
		if err := ValidateTemplate(template); err != nil {
			return nil, fmt.Errorf("validate template file %q: %w", filePath, err)
		}
		templates[template.Name] = template
	}

	if !found {
		return nil, fmt.Errorf("no template files found in %q", dir)
	}
	return templates, nil
}

func parseTemplateYAML(data []byte, source string) (Template, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	var template Template
	if err := decoder.Decode(&template); err != nil {
		return Template{}, fmt.Errorf("parse template file %q: %w", source, err)
	}

	var extra any
	err := decoder.Decode(&extra)
	switch {
	case err == io.EOF:
		return template, nil
	case err != nil:
		return Template{}, fmt.Errorf("parse template file %q: %w", source, err)
	default:
		return Template{}, fmt.Errorf("parse template file %q: multiple YAML documents are not supported", source)
	}
}

func ExportBuiltInTemplates(outputRoot string) error {
	entries, err := fs.ReadDir(builtInTemplateFiles, "templates")
	if err != nil {
		return fmt.Errorf("read template directory %q: %w", "templates", err)
	}
	if err := os.MkdirAll(outputRoot, 0o755); err != nil {
		return fmt.Errorf("create output directory %q: %w", outputRoot, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || path.Ext(entry.Name()) != ".yaml" {
			continue
		}
		srcPath := path.Join("templates", entry.Name())
		data, err := fs.ReadFile(builtInTemplateFiles, srcPath)
		if err != nil {
			return fmt.Errorf("read template file %q: %w", srcPath, err)
		}
		destPath := filepath.Join(outputRoot, entry.Name())
		if err := os.WriteFile(destPath, data, 0o644); err != nil {
			return fmt.Errorf("write template file %q: %w", destPath, err)
		}
	}
	return nil
}
