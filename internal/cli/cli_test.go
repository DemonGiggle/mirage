package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestPresetList(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	if err := Run([]string{"preset", "list"}, &out, &errBuf); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	for _, name := range []string{"offline", "github", "openai"} {
		if !strings.Contains(got, name) {
			t.Fatalf("expected preset list to contain %q, got %q", name, got)
		}
	}
}

func TestRunDryRun(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{
		"run",
		"--rootfs", "/srv/rootfs",
		"--preset", "openai",
		"--warn", "net",
		"--dry-run",
		"--",
		"echo", "hello",
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "preset: openai") {
		t.Fatalf("expected dry run output to mention preset, got %q", got)
	}
	if !strings.Contains(got, "execution: skipped (--dry-run)") {
		t.Fatalf("expected dry run output to mention skipped execution, got %q", got)
	}
}

func TestRunRejectsAllowRulesWithNetNone(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	err := Run([]string{
		"run",
		"--rootfs", "/srv/rootfs",
		"--net", "none",
		"--allow-host", "github.com:443",
		"--",
		"echo", "hello",
	}, &out, &errBuf)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "allow rules are incompatible with --net none") {
		t.Fatalf("unexpected error: %v", err)
	}
}
