package runner

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/DemonGiggle/mirage/internal/spec"
)

func Execute(cfg spec.Config, stdout, stderr io.Writer) error {
	if runtimeUnsupported() {
		return errors.New("sandbox backend currently supports Linux only")
	}
	if len(cfg.ROBind) > 0 || len(cfg.RWBind) > 0 {
		return errors.New("bind mounts are not implemented in the backend yet")
	}

	unshareArgs, err := buildUnshareArgs(cfg)
	if err != nil {
		return err
	}

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve mirage executable: %w", err)
	}

	backendArgs := []string{self, "__backend-exec", "--rootfs", cfg.RootFS, "--net", string(cfg.NetworkMode)}
	if cfg.Cwd != "" {
		backendArgs = append(backendArgs, "--cwd", cfg.Cwd)
	}
	if cfg.Hostname != "" {
		backendArgs = append(backendArgs, "--hostname", cfg.Hostname)
	}
	backendArgs = append(backendArgs, "--")
	backendArgs = append(backendArgs, cfg.Command...)

	cmd := exec.Command("unshare", append(unshareArgs, backendArgs...)...)

	stdoutCloser, stdoutTarget, err := prepareLogWriter(cfg.StdoutLog, stdout)
	if err != nil {
		return err
	}
	defer closeQuietly(stdoutCloser)

	stderrCloser, stderrTarget, err := prepareLogWriter(cfg.StderrLog, stderr)
	if err != nil {
		return err
	}
	defer closeQuietly(stderrCloser)

	cmd.Stdout = stdoutTarget
	cmd.Stderr = stderrTarget
	cmd.Env = append(os.Environ(), cfg.Env...)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("backend command failed: %w", err)
	}
	return nil
}

func RunBackendHelper(args []string, stdout, stderr io.Writer) error {
	_ = stdout
	_ = stderr

	fs := flag.NewFlagSet("__backend-exec", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var rootfs string
	var cwd string
	var hostname string
	var netMode string

	fs.StringVar(&rootfs, "rootfs", "", "backend rootfs")
	fs.StringVar(&cwd, "cwd", "", "backend cwd")
	fs.StringVar(&hostname, "hostname", "", "backend hostname")
	fs.StringVar(&netMode, "net", "", "backend network mode")

	if err := fs.Parse(args); err != nil {
		return err
	}
	command := fs.Args()
	if len(command) == 0 {
		return errors.New("backend helper requires a command")
	}
	if netMode != string(spec.NetworkHost) && netMode != string(spec.NetworkNone) && netMode != string(spec.NetworkIsolated) {
		return fmt.Errorf("unsupported backend network mode %q", netMode)
	}

	if hostname != "" {
		if err := syscall.Sethostname([]byte(hostname)); err != nil {
			return fmt.Errorf("set hostname: %w", err)
		}
	}

	if rootfs != "" && rootfs != "/" {
		if err := syscall.Chroot(rootfs); err != nil {
			return fmt.Errorf("chroot to %q: %w", rootfs, err)
		}
		if err := os.Chdir("/"); err != nil {
			return fmt.Errorf("chdir after chroot: %w", err)
		}
	}

	if cwd != "" {
		if err := os.Chdir(cwd); err != nil {
			return fmt.Errorf("chdir to %q: %w", cwd, err)
		}
	}

	binary, err := exec.LookPath(command[0])
	if err != nil {
		return fmt.Errorf("resolve command %q: %w", command[0], err)
	}
	return syscall.Exec(binary, command, os.Environ())
}

func buildUnshareArgs(cfg spec.Config) ([]string, error) {
	args := []string{
		"--user",
		"--map-root-user",
		"--fork",
		"--pid",
		"--mount-proc",
		"--mount",
		"--uts",
		"--ipc",
	}

	switch cfg.NetworkMode {
	case spec.NetworkHost:
	case spec.NetworkNone, spec.NetworkIsolated:
		args = append(args, "--net")
	default:
		return nil, fmt.Errorf("unsupported network mode %q", cfg.NetworkMode)
	}

	return args, nil
}

func prepareLogWriter(path string, fallback io.Writer) (io.Closer, io.Writer, error) {
	if path == "" {
		return nil, fallback, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, fmt.Errorf("create log directory for %q: %w", path, err)
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file %q: %w", path, err)
	}
	return file, io.MultiWriter(fallback, file), nil
}

func closeQuietly(closer io.Closer) {
	if closer != nil {
		_ = closer.Close()
	}
}

func PlanNotes(cfg spec.Config) []string {
	var notes []string
	notes = append(notes, "execution backend: linux namespace runner")
	notes = append(notes, "one sandbox = one isolated process tree")
	switch cfg.NetworkMode {
	case spec.NetworkHost:
		notes = append(notes, "network backend: host namespace")
	case spec.NetworkNone:
		notes = append(notes, "network backend: dedicated net namespace without host network")
	case spec.NetworkIsolated:
		notes = append(notes, "network backend: dedicated net namespace (allow rules not enforced yet)")
	}
	if cfg.StdoutLog != "" || cfg.StderrLog != "" {
		var exports []string
		if cfg.StdoutLog != "" {
			exports = append(exports, "stdout")
		}
		if cfg.StderrLog != "" {
			exports = append(exports, "stderr")
		}
		notes = append(notes, fmt.Sprintf("host log export: %s", strings.Join(exports, "+")))
	}
	if len(cfg.ROBind) > 0 || len(cfg.RWBind) > 0 {
		notes = append(notes, "bind mounts: parsed but not enforced yet")
	}
	if cfg.RootFS == "/" {
		notes = append(notes, "rootfs backend: host root")
	} else {
		notes = append(notes, "rootfs backend: chroot inside user namespace")
	}
	return notes
}

func runtimeUnsupported() bool {
	return runtime.GOOS != "linux"
}
