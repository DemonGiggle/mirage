package runner

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type sandboxIdentity struct {
	UID  int
	GID  int
	Home string
	User string
}

const (
	sandboxSudoBinaryPath  = "/usr/bin/sudo"
	sandboxSudoersPath     = "/etc/sudoers"
	sandboxSudoersFileMode = 0o440
)

func buildSandboxEnv(items []string, identity sandboxIdentity) ([]string, error) {
	env := []string{
		"PATH=" + defaultSandboxPath,
		"HOME=" + identity.Home,
		"USER=" + identity.User,
		"LOGNAME=" + identity.User,
	}
	for _, item := range items {
		key, _, ok := strings.Cut(item, "=")
		if !ok || key == "" {
			continue
		}
		env = setEnvValue(env, key, item)
	}
	return env, nil
}

func prepareSandboxIdentity(rootfs string, runAsRoot bool, enableSudo bool, hostname string) (sandboxIdentity, error) {
	if enableSudo && runAsRoot {
		return sandboxIdentity{}, errors.New("guest sudo and run-as-root cannot both be enabled")
	}
	if enableSudo && (rootfs == "" || rootfs == "/") {
		return sandboxIdentity{}, errors.New("guest sudo requires a dedicated non-/ rootfs")
	}
	identity := defaultSandboxIdentity(rootfs, runAsRoot)
	if runAsRoot {
		if rootfs != "" && rootfs != "/" {
			if err := ensureDir(bindMountTargetPath(rootfs, identity.Home), 0o755); err != nil {
				return sandboxIdentity{}, fmt.Errorf("prepare root home %q: %w", identity.Home, err)
			}
		}
		return identity, nil
	}
	if err := ensureOwnedDir(bindMountTargetPath(rootfs, identity.Home), 0o755, identity.UID, identity.GID); err != nil {
		return sandboxIdentity{}, fmt.Errorf("prepare sandbox home %q: %w", identity.Home, err)
	}
	if err := applySandboxIdentityFiles(rootfs, identity); err != nil {
		return sandboxIdentity{}, err
	}
	if enableSudo {
		if err := applySandboxSudoPolicy(rootfs, identity, hostname); err != nil {
			return sandboxIdentity{}, err
		}
	}
	return identity, nil
}

func defaultSandboxIdentity(rootfs string, runAsRoot bool) sandboxIdentity {
	if runAsRoot {
		return sandboxIdentity{
			UID:  0,
			GID:  0,
			Home: defaultRootHome,
			User: defaultRootUser,
		}
	}
	home := defaultSandboxHome
	if rootfs == "/" {
		home = hostSandboxHome()
	}
	return sandboxIdentity{
		UID:  sandboxUID,
		GID:  sandboxGID,
		Home: home,
		User: defaultSandboxUser,
	}
}

func hostSandboxHome() string {
	suffix := strconv.Itoa(os.Getuid())
	if currentUser, err := user.Current(); err == nil {
		if username := sanitizePathComponent(currentUser.Username); username != "" {
			suffix = username
		}
	}
	return filepath.Join("/tmp", "mirage-home-"+suffix)
}

func sanitizePathComponent(value string) string {
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-', r == '_', r == '.':
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func applySandboxIdentityFiles(rootfs string, identity sandboxIdentity) error {
	contents := sandboxIdentityFileContents(identity)
	for target, content := range contents {
		sourcePath, err := writeRuntimeIdentityFile(content, sandboxIdentityFileMode(target))
		if err != nil {
			return fmt.Errorf("prepare sandbox identity file %q: %w", target, err)
		}
		defer func(path string) {
			_ = os.Remove(path)
		}(sourcePath)
		if err := applyBindMount(rootfs, bindMount{Source: sourcePath, Target: target, ReadOnly: true}); err != nil {
			return fmt.Errorf("install sandbox identity file %q: %w", target, err)
		}
	}
	return nil
}

func sandboxIdentityFileMode(target string) os.FileMode {
	switch target {
	case "/etc/shadow", "/etc/gshadow":
		return 0o640
	default:
		return 0o644
	}
}

func sandboxIdentityFileContents(identity sandboxIdentity) map[string]string {
	return map[string]string{
		"/etc/passwd":  fmt.Sprintf("root:x:0:0:root:%s:/bin/sh\n%s:x:%d:%d:%s:%s:/bin/sh\n", defaultRootHome, identity.User, identity.UID, identity.GID, identity.User, identity.Home),
		"/etc/group":   fmt.Sprintf("root:x:0:\n%s:x:%d:\n", identity.User, identity.GID),
		"/etc/shadow":  fmt.Sprintf("root:!:1:0:99999:7:::\n%s:!:1:0:99999:7:::\n", identity.User),
		"/etc/gshadow": fmt.Sprintf("root:!::\n%s:!::\n", identity.User),
		"/etc/nsswitch.conf": strings.Join([]string{
			"passwd: files",
			"group: files",
			"shadow: files",
			"gshadow: files",
			"hosts: files dns",
			"networks: files",
			"protocols: files",
			"services: files",
			"ethers: files",
			"rpc: files",
			"netgroup: files",
			"",
		}, "\n"),
	}
}

func applySandboxSudoPolicy(rootfs string, identity sandboxIdentity, hostname string) error {
	if err := validateSandboxSudoBinary(rootfs, 0); err != nil {
		return err
	}
	if err := validateSandboxPathComponents(rootfs, sandboxSudoersPath, true); err != nil {
		return err
	}
	if err := installSandboxRuntimeFile(rootfs, sandboxSudoersPath, sandboxSudoersFileContents(identity), sandboxSudoersFileMode); err != nil {
		return fmt.Errorf("install sandbox sudoers policy: %w", err)
	}
	if err := validateSandboxPathComponents(rootfs, "/etc/hosts", true); err != nil {
		return err
	}
	hostsContent, err := sandboxSudoHostsFileContents(rootfs, hostname)
	if err != nil {
		return err
	}
	if err := installSandboxRuntimeFile(rootfs, "/etc/hosts", hostsContent, 0o644); err != nil {
		return fmt.Errorf("install sandbox sudo hosts file: %w", err)
	}
	return nil
}

func installSandboxRuntimeFile(rootfs string, target string, content string, mode os.FileMode) error {
	sourcePath, err := writeRuntimeIdentityFile(content, mode)
	if err != nil {
		return fmt.Errorf("prepare runtime file %q: %w", target, err)
	}
	defer func() {
		_ = os.Remove(sourcePath)
	}()
	if err := applyBindMount(rootfs, bindMount{Source: sourcePath, Target: target, ReadOnly: true}); err != nil {
		return err
	}
	return nil
}

func validateSandboxSudoBinary(rootfs string, rootUID uint32) error {
	if err := validateSandboxPathComponents(rootfs, sandboxSudoBinaryPath, true); err != nil {
		return err
	}
	path := bindMountTargetPath(rootfs, sandboxSudoBinaryPath)
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("guest sudo requires %s inside the rootfs; rebuild it with mirage rootfs init --sudo", sandboxSudoBinaryPath)
		}
		return fmt.Errorf("inspect guest sudo binary %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("guest sudo binary %q must not be a symlink", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("guest sudo binary %q is not a regular file", path)
	}
	if info.Mode()&os.ModeSetuid == 0 {
		return fmt.Errorf("guest sudo binary %q is not setuid", path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("inspect guest sudo binary %q ownership: unsupported stat result", path)
	}
	if stat.Uid != rootUID {
		return fmt.Errorf("guest sudo binary %q is owned by uid %d, want namespace root uid %d", path, stat.Uid, rootUID)
	}
	return nil
}

func validateSandboxPathComponents(rootfs string, guestPath string, allowMissingFinal bool) error {
	cleanGuestPath := filepath.Clean("/" + strings.TrimPrefix(guestPath, "/"))
	components := strings.Split(strings.TrimPrefix(cleanGuestPath, "/"), "/")
	current := rootfs
	for index, component := range components {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			if allowMissingFinal && index == len(components)-1 && errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("inspect guest path component %q: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("guest path component %q must not be a symlink", current)
		}
		if index < len(components)-1 && !info.IsDir() {
			return fmt.Errorf("guest path component %q is not a directory", current)
		}
	}
	return nil
}

func sandboxSudoersFileContents(identity sandboxIdentity) string {
	return strings.Join([]string{
		"Defaults env_reset",
		"Defaults secure_path=\"" + defaultSandboxPath + "\"",
		"root ALL=(ALL:ALL) ALL",
		fmt.Sprintf("%s ALL=(ALL:ALL) NOPASSWD: ALL", identity.User),
		"",
	}, "\n")
}

func sandboxSudoHostsFileContents(rootfs string, hostname string) (string, error) {
	if hostname == "" {
		hostname = defaultSandboxHost
	}
	if strings.ContainsAny(hostname, " \t\r\n#") {
		return "", fmt.Errorf("guest sudo hostname %q cannot be represented safely in /etc/hosts", hostname)
	}
	target := bindMountTargetPath(rootfs, "/etc/hosts")
	data, err := os.ReadFile(target)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read guest hosts file %q: %w", target, err)
	}
	content := string(data)
	for _, line := range strings.Split(content, "\n") {
		line, _, _ = strings.Cut(line, "#")
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		for _, name := range fields[1:] {
			if name == hostname {
				return content, nil
			}
		}
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content + "127.0.1.1\t" + hostname + "\n", nil
}

func writeRuntimeIdentityFile(content string, mode os.FileMode) (string, error) {
	file, err := os.CreateTemp("", "mirage-identity-*")
	if err != nil {
		return "", err
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return "", err
	}
	if err := os.Chmod(file.Name(), mode); err != nil {
		_ = os.Remove(file.Name())
		return "", err
	}
	return file.Name(), nil
}

func prepareHostRootRuntimeLayout() error {
	if err := mountTmpfs("/run", "mode=0755"); err != nil {
		return fmt.Errorf("mount runtime /run tmpfs: %w", err)
	}
	return nil
}

func stageSandboxCommand(command []string, rootfs string, sandboxEnv []string, identity sandboxIdentity) ([]string, error) {
	if rootfs != "/" || identity.UID == 0 || len(command) == 0 {
		return command, nil
	}

	binary, err := resolveCommandBinary(command[0], rootfs, sandboxEnv)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(binary, "/tmp/") {
		return command, nil
	}

	stageDir := "/run/mirage-bin"
	if err := ensureDir(stageDir, 0o755); err != nil {
		return nil, fmt.Errorf("prepare staged command dir: %w", err)
	}
	source, err := os.Open(binary)
	if err != nil {
		return nil, fmt.Errorf("open staged command source %q: %w", binary, err)
	}
	defer source.Close()

	stagedPath := filepath.Join(stageDir, filepath.Base(binary))
	target, err := os.OpenFile(stagedPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return nil, fmt.Errorf("create staged command %q: %w", stagedPath, err)
	}
	if _, err := io.Copy(target, source); err != nil {
		_ = target.Close()
		return nil, fmt.Errorf("copy staged command %q: %w", stagedPath, err)
	}
	if err := target.Close(); err != nil {
		return nil, fmt.Errorf("close staged command %q: %w", stagedPath, err)
	}
	if err := os.Chown(stageDir, identity.UID, identity.GID); err != nil {
		return nil, fmt.Errorf("chown staged command dir %q: %w", stageDir, err)
	}
	if err := os.Chown(stagedPath, identity.UID, identity.GID); err != nil {
		return nil, fmt.Errorf("chown staged command %q: %w", stagedPath, err)
	}

	stagedCommand := append([]string(nil), command...)
	stagedCommand[0] = stagedPath
	return stagedCommand, nil
}

func applySandboxIdentity(identity sandboxIdentity) error {
	if identity.UID == 0 && identity.GID == 0 {
		return nil
	}
	if err := clearInheritedSupplementaryGroups(); err != nil {
		return fmt.Errorf("clear supplementary groups: %w", err)
	}
	if err := setgidFunc(identity.GID); err != nil {
		return fmt.Errorf("drop sandbox gid to %d: %w", identity.GID, err)
	}
	if err := setuidFunc(identity.UID); err != nil {
		return fmt.Errorf("drop sandbox uid to %d: %w", identity.UID, err)
	}
	return nil
}

func applyMappedRootIdentity() error {
	if err := clearInheritedSupplementaryGroups(); err != nil {
		return fmt.Errorf("clear supplementary groups before mapped-root handoff: %w", err)
	}
	if err := setgidFunc(0); err != nil {
		return fmt.Errorf("become mapped root gid: %w", err)
	}
	if err := setuidFunc(0); err != nil {
		return fmt.Errorf("become mapped root uid: %w", err)
	}
	return nil
}

func clearInheritedSupplementaryGroups() error {
	if err := setgroupsFunc(nil); err != nil {
		if errors.Is(err, syscall.EPERM) {
			groups, groupsErr := currentGroups()
			if groupsErr == nil && len(groups) == 0 {
				return nil
			}
		}
		return err
	}
	return nil
}

func envValue(items []string, key string, fallback string) string {
	prefix := key + "="
	for idx := len(items) - 1; idx >= 0; idx-- {
		if strings.HasPrefix(items[idx], prefix) {
			return strings.TrimPrefix(items[idx], prefix)
		}
	}
	return fallback
}

func setEnvValue(items []string, key string, value string) []string {
	prefix := key + "="
	for idx, item := range items {
		if strings.HasPrefix(item, prefix) {
			items[idx] = value
			return items
		}
	}
	return append(items, value)
}

func lookPathInEnv(file string, searchPath string) (string, error) {
	if strings.ContainsRune(file, os.PathSeparator) {
		return exec.LookPath(file)
	}
	for _, dir := range filepath.SplitList(searchPath) {
		if dir == "" {
			dir = "."
		}
		candidate := filepath.Join(dir, file)
		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if info.IsDir() || info.Mode().Perm()&0o111 == 0 {
			continue
		}
		return candidate, nil
	}
	return "", exec.ErrNotFound
}
