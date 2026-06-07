package rootfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequiredNSSModulesIgnoresBracketedActions(t *testing.T) {
	rootfs := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rootfs, "etc"), 0o755); err != nil {
		t.Fatalf("create etc dir: %v", err)
	}
	content := "passwd: files systemd\nhosts: files mdns4_minimal [ NOTFOUND=return ] dns\n"
	if err := os.WriteFile(filepath.Join(rootfs, "etc", "nsswitch.conf"), []byte(content), 0o644); err != nil {
		t.Fatalf("write nsswitch.conf: %v", err)
	}

	modules, err := requiredNSSModules(rootfs)
	if err != nil {
		t.Fatalf("requiredNSSModules returned error: %v", err)
	}
	want := []string{"files", "systemd", "mdns4_minimal", "dns"}
	if len(modules) != len(want) {
		t.Fatalf("unexpected module count: got %d want %d (%v)", len(modules), len(want), modules)
	}
	for idx, module := range want {
		if modules[idx] != module {
			t.Fatalf("unexpected module at %d: got %q want %q", idx, modules[idx], module)
		}
	}
}

func TestRequiredNSSModulesIncludesIdentityDatabases(t *testing.T) {
	rootfs := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rootfs, "etc"), 0o755); err != nil {
		t.Fatalf("create etc dir: %v", err)
	}
	content := strings.Join([]string{
		"passwd: files systemd",
		"group: files systemd",
		"shadow: files",
		"hosts: files dns",
		"services: db files",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(rootfs, "etc", "nsswitch.conf"), []byte(content), 0o644); err != nil {
		t.Fatalf("write nsswitch.conf: %v", err)
	}

	modules, err := requiredNSSModules(rootfs)
	if err != nil {
		t.Fatalf("requiredNSSModules returned error: %v", err)
	}
	want := []string{"files", "systemd", "dns"}
	if len(modules) != len(want) {
		t.Fatalf("unexpected module count: got %d want %d (%v)", len(modules), len(want), modules)
	}
	for idx, module := range want {
		if modules[idx] != module {
			t.Fatalf("unexpected module at %d: got %q want %q", idx, modules[idx], module)
		}
	}
}

func TestResolveLDConfigPathFindsFallbackOutsidePATH(t *testing.T) {
	path, err := resolveLDConfigPath()
	if err != nil {
		t.Skipf("ldconfig unavailable in this environment: %v", err)
	}
	if !strings.HasSuffix(path, "ldconfig") {
		t.Fatalf("expected ldconfig path, got %q", path)
	}

	t.Setenv("PATH", "")
	path, err = resolveLDConfigPath()
	if err != nil {
		t.Skipf("ldconfig fallback unavailable in this environment: %v", err)
	}
	if path != "/sbin/ldconfig" && path != "/usr/sbin/ldconfig" {
		t.Fatalf("expected sbin fallback path, got %q", path)
	}
}
