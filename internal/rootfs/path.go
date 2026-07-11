package rootfs

import (
	"path/filepath"
	"strings"
)

// normalizeGuestPath converts any guest path to an absolute, cleaned path.
// Prefixing with / before cleaning prevents relative traversal from escaping
// the guest namespace.
func normalizeGuestPath(path string) string {
	return filepath.Clean("/" + strings.TrimPrefix(path, "/"))
}

// pathInsideRoot maps a guest path into a host-side rootfs directory.
func pathInsideRoot(root string, guestPath string) string {
	return filepath.Join(root, strings.TrimPrefix(normalizeGuestPath(guestPath), "/"))
}
