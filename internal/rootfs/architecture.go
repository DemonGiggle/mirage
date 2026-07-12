package rootfs

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

var supportedRootfsArchitectures = []string{"x86_64", "arm64", "arm32", "riscv64"}
var detectBootstrapHostArchitecture = defaultDetectBootstrapHostArchitecture

func SupportedArchitectures() []string {
	return append([]string(nil), supportedRootfsArchitectures...)
}

func NormalizeArchitecture(raw string) (string, error) {
	return normalizeRootfsArchitecture(raw)
}

func GoBuildTargetForArchitecture(architecture string) (string, string, error) {
	switch architecture {
	case "x86_64":
		return "amd64", "", nil
	case "arm64":
		return "arm64", "", nil
	case "arm32":
		return "arm", "7", nil
	case "riscv64":
		return "riscv64", "", nil
	default:
		return "", "", fmt.Errorf("unsupported architecture %q (supported: %s)", architecture, strings.Join(supportedRootfsArchitectures, ", "))
	}
}

func resolveRootfsArchitecture(requested string) (string, error) {
	value := strings.TrimSpace(requested)
	if value == "" {
		hostArchitecture, err := detectBootstrapHostArchitecture()
		if err != nil {
			return "", err
		}
		value = hostArchitecture
	}
	return normalizeRootfsArchitecture(value)
}

func normalizeRootfsArchitecture(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "x86_64", "arm64", "arm32", "riscv64":
		return value, nil
	default:
		return "", fmt.Errorf("unsupported architecture %q (supported: %s)", raw, strings.Join(supportedRootfsArchitectures, ", "))
	}
}

func debianArchitectureForRootfsArch(architecture string) (string, error) {
	switch architecture {
	case "x86_64":
		return "amd64", nil
	case "arm64":
		return "arm64", nil
	case "arm32":
		return "armhf", nil
	case "riscv64":
		return "riscv64", nil
	default:
		return "", fmt.Errorf("unsupported architecture %q (supported: %s)", architecture, strings.Join(supportedRootfsArchitectures, ", "))
	}
}

func defaultDetectBootstrapHostArchitecture() (string, error) {
	if dpkgPath, err := exec.LookPath("dpkg"); err == nil {
		output, err := exec.Command(dpkgPath, "--print-architecture").Output()
		if err == nil {
			switch strings.TrimSpace(string(output)) {
			case "amd64":
				return "x86_64", nil
			case "arm64":
				return "arm64", nil
			case "armhf":
				return "arm32", nil
			case "riscv64":
				return "riscv64", nil
			}
		}
	}
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64", nil
	case "arm64":
		return "arm64", nil
	case "arm":
		return "arm32", nil
	case "riscv64":
		return "riscv64", nil
	default:
		return "", fmt.Errorf("could not detect a supported host architecture from GOARCH=%q; pass --arch explicitly (supported: %s)", runtime.GOARCH, strings.Join(supportedRootfsArchitectures, ", "))
	}
}
