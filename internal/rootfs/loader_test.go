package rootfs

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestLoadTemplatesFromFS(t *testing.T) {
	templates, err := loadTemplatesFromFS(fstest.MapFS{
		"templates/custom.yaml": {
			Data: []byte(`version: v1
name: custom
description: Custom template
binaries:
  - target_path: /bin/sh
    lookup_name: sh
    copy_dependencies: true
`),
		},
	}, "templates")
	if err != nil {
		t.Fatalf("loadTemplatesFromFS returned error: %v", err)
	}

	template, ok := templates["custom"]
	if !ok {
		t.Fatalf("expected custom template to be loaded, got %v", templates)
	}
	if template.Binaries[0].LookupName != "sh" {
		t.Fatalf("unexpected template content: %#v", template)
	}
}

func TestLoadTemplatesFromFSRejectsFilenameMismatch(t *testing.T) {
	_, err := loadTemplatesFromFS(fstest.MapFS{
		"templates/one.yaml": {
			Data: []byte(`version: v1
name: duplicate
description: First template
binaries:
  - target_path: /bin/sh
    lookup_name: sh
    copy_dependencies: true
`),
		},
	}, "templates")
	if err == nil {
		t.Fatal("expected mismatched file/template name to fail")
	}
	if !strings.Contains(err.Error(), `must declare name "one"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadTemplatesFromFSRejectsUnknownFields(t *testing.T) {
	_, err := loadTemplatesFromFS(fstest.MapFS{
		"templates/custom.yaml": {
			Data: []byte(`version: v1
name: custom
description: Custom template
unknown_field: true
`),
		},
	}, "templates")
	if err == nil {
		t.Fatal("expected unknown field to fail")
	}
	if !strings.Contains(err.Error(), "unknown_field") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadBuiltInTemplatesFromEmbed(t *testing.T) {
	templates, err := loadTemplatesFromFS(builtInTemplateFiles, "templates")
	if err != nil {
		t.Fatalf("loadTemplatesFromFS returned error: %v", err)
	}
	if len(templates) != len(BuiltInTemplates) {
		t.Fatalf("unexpected built-in template count: got %d want %d", len(templates), len(BuiltInTemplates))
	}
	for _, name := range []string{"basic", "debian", "openclaw", "openclaw-root", "openclaw-systemd"} {
		if _, ok := templates[name]; !ok {
			t.Fatalf("expected built-in embedded template %q, got %v", name, TemplateNames())
		}
	}
}
