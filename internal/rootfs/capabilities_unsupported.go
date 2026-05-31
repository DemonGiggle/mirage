//go:build !linux

package rootfs

func preserveFileCapabilitiesWarning(sourcePath string, guestTargetPath string, hostTargetPath string) string {
	return ""
}
