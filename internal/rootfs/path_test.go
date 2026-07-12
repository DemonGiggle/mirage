package rootfs

import (
	"path/filepath"
	"testing"
)

func TestPathInsideRootConfinesGuestTraversal(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "tmp", "rootfs")
	tests := map[string]string{
		"/usr/bin/sh":       filepath.Join(root, "usr", "bin", "sh"),
		"usr/bin/sh":        filepath.Join(root, "usr", "bin", "sh"),
		"../../etc/passwd":  filepath.Join(root, "etc", "passwd"),
		"/../../etc/passwd": filepath.Join(root, "etc", "passwd"),
	}

	for guestPath, want := range tests {
		t.Run(guestPath, func(t *testing.T) {
			if got := pathInsideRoot(root, guestPath); got != want {
				t.Fatalf("pathInsideRoot(%q, %q) = %q, want %q", root, guestPath, got, want)
			}
		})
	}
}
