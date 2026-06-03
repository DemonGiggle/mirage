package rootfs

import (
	"strings"
	"testing"
)

func TestTemplateNames(t *testing.T) {
	got := TemplateNames()
	want := []string{
		"basic",
		"debian",
		"node",
		"openclaw",
		"openclaw-admin",
		"openclaw-chat-only",
		"openclaw-developer",
		"openclaw-root",
		"openclaw-systemd",
		"openclaw-work",
		"python",
	}
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
		RuntimeTrees: []RuntimeTree{
			{
				HostPath:   "/usr/share/zoneinfo",
				TargetPath: "/usr/share/zoneinfo",
				Optional:   true,
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
		RuntimeTrees: []RuntimeTree{
			{
				HostPath:   "/usr/share/zoneinfo",
				TargetPath: "/usr/share/zoneinfo",
			},
			{
				HostPath:   "/usr/share/zoneinfo",
				TargetPath: "/usr/share/zoneinfo",
				Optional:   true,
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
		`runtime tree target path "/usr/share/zoneinfo" is duplicated`,
		`runtime file target path "/etc/hosts" is duplicated`,
	} {
		if !strings.Contains(err.Error(), needle) {
			t.Fatalf("expected error to contain %q, got %v", needle, err)
		}
	}
}

func TestOpenclawLevelsComposeIncrementally(t *testing.T) {
	levels := []struct {
		name  string
		needs []string
	}{
		{name: "openclaw-chat-only", needs: []string{"node", "npm", "npx", "openssl"}},
		{name: "openclaw-work", needs: []string{"bash", "find", "jq", "rg"}},
		{name: "openclaw-developer", needs: []string{"git", "make", "fdfind", "python3", "sqlite3", "go", "rustc", "cargo"}},
		{name: "openclaw-admin", needs: []string{"ip", "ping", "dig", "lsof", "iptables", "nft", "nc", "ssh"}},
		{name: "openclaw-root", needs: []string{"sudo", "apt", "gpg", "strace", "gdb", "nsenter", "parted", "mkfs.ext4", "mkfs.xfs"}},
	}
	for _, level := range levels {
		template, ok := LookupTemplate(level.name)
		if !ok {
			t.Fatalf("expected %s template to exist", level.name)
		}

		var lookups []string
		for _, binary := range template.Binaries {
			lookups = append(lookups, binary.LookupName)
		}
		for _, want := range level.needs {
			if !contains(lookups, want) {
				t.Fatalf("expected %s template to include %q, got %v", level.name, want, lookups)
			}
		}
	}
}

func TestDebianTemplateIncludesAPTEnvironment(t *testing.T) {
	template, ok := LookupTemplate("debian")
	if !ok {
		t.Fatal("expected debian template to exist")
	}

	var lookups []string
	for _, binary := range template.Binaries {
		lookups = append(lookups, binary.LookupName)
	}
	for _, want := range []string{"apt", "apt-get", "apt-cache", "apt-config", "apt-key", "dpkg", "dpkg-deb", "dpkg-query", "gpg", "gpgv"} {
		if !contains(lookups, want) {
			t.Fatalf("expected debian template to include %q, got %v", want, lookups)
		}
	}

	var targets []string
	for _, binary := range template.Binaries {
		targets = append(targets, binary.TargetPath)
	}
	for _, want := range []string{
		"/usr/lib/apt/methods/http",
		"/usr/lib/apt/methods/https",
		"/usr/lib/apt/methods/gpgv",
	} {
		if !contains(targets, want) {
			t.Fatalf("expected debian template to include apt method binary %q, got %v", want, targets)
		}
	}

	var treeTargets []string
	for _, runtimeTree := range template.RuntimeTrees {
		treeTargets = append(treeTargets, runtimeTree.TargetPath)
	}
	for _, want := range []string{"/etc/apt/keyrings", "/etc/apt/trusted.gpg.d"} {
		if !contains(treeTargets, want) {
			t.Fatalf("expected debian template to include runtime tree %q, got %v", want, treeTargets)
		}
	}

	var dirPaths []string
	for _, dir := range template.Directories {
		dirPaths = append(dirPaths, dir.Path)
	}
	for _, want := range []string{
		"/etc/apt/apt.conf.d",
		"/etc/apt/preferences.d",
		"/etc/apt/sources.list.d",
		"/var/lib/dpkg",
		"/var/lib/dpkg/info",
		"/var/lib/dpkg/triggers",
		"/var/lib/dpkg/updates",
	} {
		if !contains(dirPaths, want) {
			t.Fatalf("expected debian template to include directory %q, got %v", want, dirPaths)
		}
	}
}

func TestOpenclawChatOnlyTemplateIncludesRuntimeTrees(t *testing.T) {
	template, ok := LookupTemplate("openclaw-chat-only")
	if !ok {
		t.Fatal("expected openclaw-chat-only template to exist")
	}

	var treeTargets []string
	for _, runtimeTree := range template.RuntimeTrees {
		treeTargets = append(treeTargets, runtimeTree.TargetPath)
	}
	for _, want := range []string{"/usr/share/zoneinfo", "/usr/lib/locale", "/usr/share/locale"} {
		if !contains(treeTargets, want) {
			t.Fatalf("expected openclaw-chat-only template to include runtime tree %q, got %v", want, treeTargets)
		}
	}
}

func TestAppendUniqueRuntimeTreesDeduplicatesByTargetPath(t *testing.T) {
	got := appendUniqueRuntimeTrees(
		[]RuntimeTree{{HostPath: "/usr/lib/python3.11", TargetPath: "/usr/lib/python3"}},
		RuntimeTree{HostPath: "/usr/lib/python3.12", TargetPath: "/usr/lib/python3"},
	)
	if len(got) != 1 {
		t.Fatalf("expected one runtime tree after target-path dedupe, got %v", got)
	}
	if got[0].HostPath != "/usr/lib/python3.11" {
		t.Fatalf("expected first runtime tree to be kept, got %v", got)
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
