//go:build linux

package rootfs

import (
	"errors"
	"fmt"
	"syscall"
)

const securityCapabilityXattr = "security.capability"

func preserveFileCapabilitiesWarning(sourcePath string, guestTargetPath string, hostTargetPath string) string {
	value, ok, err := readSecurityCapabilityXattr(sourcePath)
	if err != nil || !ok {
		return ""
	}
	if err := syscall.Setxattr(hostTargetPath, securityCapabilityXattr, value, 0); err == nil {
		return ""
	}
	return fmt.Sprintf(
		"file capability %q from %q could not be preserved on %q; binaries such as ping may not work from the generated rootfs without CAP_SETFCAP or elevated rootfs generation",
		securityCapabilityXattr,
		sourcePath,
		guestTargetPath,
	)
}

func readSecurityCapabilityXattr(path string) ([]byte, bool, error) {
	size, err := syscall.Getxattr(path, securityCapabilityXattr, nil)
	switch {
	case err == nil:
	case errors.Is(err, syscall.ENODATA), errors.Is(err, syscall.ENOTSUP), errors.Is(err, syscall.EOPNOTSUPP):
		return nil, false, nil
	default:
		return nil, false, err
	}
	if size == 0 {
		return nil, false, nil
	}
	value := make([]byte, size)
	readSize, err := syscall.Getxattr(path, securityCapabilityXattr, value)
	if err != nil {
		if errors.Is(err, syscall.ENODATA) || errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EOPNOTSUPP) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return value[:readSize], true, nil
}
