package rootfs

import (
	"strings"
	"testing"
)

func TestTemplateNames(t *testing.T) {
	got := TemplateNames()
	want := []string{"basic", "node", "openclaw", "openclaw-systemd", "python"}
	if len(got) != len(want) {
		t.Fatalf("unexpected template count: got %d want %d (%v)", len(got), len(want), got)
	}
	for idx, name := range want {
		if got[idx] != name {
			t.Fatalf("unexpected template order at %d: got %q want %q", idx, got[idx], name)
		}
	}
}

func TestBuiltInTemplatesAreValid(t *testing.T) {
	for name, template := range AvailableTemplates() {
		if err := ValidateTemplate(template); err != nil {
			t.Fatalf("ValidateTemplate(%q) returned error: %v", name, err)
		}
	}
}

func TestLookupTemplateReturnsCopy(t *testing.T) {
	template, ok := LookupTemplate("basic")
	if !ok {
		t.Fatal("expected basic template to exist")
	}
	template.Binaries[0].LookupName = "changed"

	again, ok := LookupTemplate("basic")
	if !ok {
		t.Fatal("expected basic template to still exist")
	}
	if again.Binaries[0].LookupName != "sh" {
		t.Fatalf("expected built-in template to stay immutable, got %#v", again.Binaries[0])
	}
}

func TestValidateTemplateSupportsHostPathsAndPathLookups(t *testing.T) {
	template := Template{
		Version:     TemplateVersionV1,
		Name:        "custom",
		Description: "Custom template",
		Directories: []Directory{{Path: "/tmp", Mode: 0o1777}},
		Binaries: []Binary{
			{
				TargetPath:       "/bin/sh",
				HostPath:         "/bin/sh",
				CopyDependencies: true,
			},
			{
				TargetPath:       "/usr/bin/python3",
				LookupName:       "python3",
				CopyDependencies: true,
			},
		},
		RuntimeFiles: []RuntimeFile{
			{
				HostPath:   "/etc/hosts",
				TargetPath: "/etc/hosts",
			},
		},
	}

	if err := ValidateTemplate(template); err != nil {
		t.Fatalf("ValidateTemplate returned error: %v", err)
	}
}

func TestValidateTemplateRejectsInvalidBinarySource(t *testing.T) {
	template := Template{
		Version:     TemplateVersionV1,
		Name:        "broken",
		Description: "Broken template",
		Binaries: []Binary{
			{
				TargetPath: "/bin/sh",
			},
		},
	}

	err := ValidateTemplate(template)
	if err == nil {
		t.Fatal("expected invalid binary source to fail")
	}
	if !strings.Contains(err.Error(), "must set exactly one of host_path or lookup_name") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateTemplateRejectsDuplicatePaths(t *testing.T) {
	template := Template{
		Version:     TemplateVersionV1,
		Name:        "broken",
		Description: "Broken template",
		Directories: []Directory{
			{Path: "/workspace", Mode: 0o755},
			{Path: "/workspace", Mode: 0o777},
		},
		Binaries: []Binary{
			{
				TargetPath:       "/bin/sh",
				HostPath:         "/bin/sh",
				CopyDependencies: true,
			},
			{
				TargetPath:       "/bin/sh",
				LookupName:       "bash",
				CopyDependencies: true,
			},
		},
		RuntimeFiles: []RuntimeFile{
			{
				HostPath:   "/etc/hosts",
				TargetPath: "/etc/hosts",
			},
			{
				HostPath:   "/etc/hosts",
				TargetPath: "/etc/hosts",
				Optional:   true,
			},
		},
	}

	err := ValidateTemplate(template)
	if err == nil {
		t.Fatal("expected duplicate paths to fail")
	}
	for _, needle := range []string{
		`directory path "/workspace" is duplicated`,
		`binary target path "/bin/sh" is duplicated`,
		`runtime file target path "/etc/hosts" is duplicated`,
	} {
		if !strings.Contains(err.Error(), needle) {
			t.Fatalf("expected error to contain %q, got %v", needle, err)
		}
	}
}

func TestOpenclawTemplateIncludesNodeAndGit(t *testing.T) {
	template, ok := LookupTemplate("openclaw")
	if !ok {
		t.Fatal("expected openclaw template to exist")
	}

	var lookups []string
	for _, binary := range template.Binaries {
		lookups = append(lookups, binary.LookupName)
	}
	for _, want := range []string{"node", "npm", "npx", "git", "bash"} {
		if !contains(lookups, want) {
			t.Fatalf("expected openclaw template to include %q, got %v", want, lookups)
		}
	}
}

func TestOpenclawSystemdTemplateIncludesSystemdAssets(t *testing.T) {
	template, ok := LookupTemplate("openclaw-systemd")
	if !ok {
		t.Fatal("expected openclaw-systemd template to exist")
	}

	var lookups []string
	for _, binary := range template.Binaries {
		lookups = append(lookups, binary.LookupName)
	}
	for _, want := range []string{"node", "systemd", "systemctl", "journalctl", "systemd-tmpfiles"} {
		if !contains(lookups, want) {
			t.Fatalf("expected openclaw-systemd template to include %q, got %v", want, lookups)
		}
	}

	var generatedTargets []string
	for _, file := range template.GeneratedFiles {
		generatedTargets = append(generatedTargets, file.TargetPath)
	}
	if !contains(generatedTargets, "/etc/machine-id") {
		t.Fatalf("expected openclaw-systemd template to seed /etc/machine-id, got %v", generatedTargets)
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
