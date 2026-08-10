package rootfs

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestKeepIDEntriesMapCallerToDefaultSandboxUser(t *testing.T) {
	got := keepIDEntries(1000, 165536)
	want := [][3]int{
		{0, 165536, 1000},
		{1000, 1000, 1},
		{1001, 166536, 64535},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected keep-ID map: got %#v want %#v", got, want)
	}
}

func TestKeepIDOwnershipMarkerRoundTrip(t *testing.T) {
	root := t.TempDir()
	if err := WriteKeepIDOwnershipMarker(root); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	got, err := HasKeepIDOwnership(root)
	if err != nil || !got {
		t.Fatalf("detect marker: got %t err=%v", got, err)
	}
}

func TestKeepIDOwnershipMarkerRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "marker")
	if err := os.WriteFile(target, []byte(keepIDOwnershipValue), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(root, keepIDOwnershipMarker)); err != nil {
		t.Fatalf("create marker symlink: %v", err)
	}
	if writeErr := WriteKeepIDOwnershipMarker(root); writeErr == nil {
		t.Fatal("expected marker creation to reject existing symlink")
	}
	_, err := HasKeepIDOwnership(root)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestOwnershipHelperRejectsUnexpectedOwnerBeforeUsingFD(t *testing.T) {
	err := RunOwnershipHelper([]string{"--owner", "42"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "unsupported rootfs ownership target") {
		t.Fatalf("expected owner rejection, got %v", err)
	}
}

func TestSudoPackagesRetainLegacyRootlessOwnership(t *testing.T) {
	tests := []struct {
		name    string
		options GenerateOptions
		want    bool
	}{
		{name: "ordinary rootfs", options: GenerateOptions{}, want: false},
		{name: "sudo option", options: GenerateOptions{IncludeSudo: true}, want: true},
		{name: "explicit sudo package", options: GenerateOptions{ExtraPackages: []string{"curl", "sudo"}}, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := requiresLegacyRootlessOwnership(tc.options); got != tc.want {
				t.Fatalf("requiresLegacyRootlessOwnership() = %t, want %t", got, tc.want)
			}
		})
	}
}
