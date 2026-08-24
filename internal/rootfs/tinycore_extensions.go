package rootfs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const tinyCoreMaximumExtensionSize = 16 << 20

type tinyCoreBootstrapExtension struct {
	name   string
	url    string
	sha256 string
}

var tinyCoreBootstrapExtensions = map[string][]tinyCoreBootstrapExtension{
	"16.1": {
		{name: "liblzma.tcz", url: "http://tinycorelinux.net/16.x/x86_64/tcz/liblzma.tcz", sha256: "b550c00b318a89885f2a9d4f7d6a15b8d0834e50aab9094d50d417dba66828e7"},
		{name: "lzo.tcz", url: "http://tinycorelinux.net/16.x/x86_64/tcz/lzo.tcz", sha256: "648ad2e21273415852d2602e9c7874010bbbb83a8a634d4b51a5c6a56d02fc5c"},
		{name: "libzstd.tcz", url: "http://tinycorelinux.net/16.x/x86_64/tcz/libzstd.tcz", sha256: "b784fb89fcea06abd605e9e14b226dc68ea0cb98bd420ab4d098a1286e85340b"},
		{name: "liblz4.tcz", url: "http://tinycorelinux.net/16.x/x86_64/tcz/liblz4.tcz", sha256: "b0e9e8f055bb3c6eb49c2128e66699a3952c289eb3d8f64d79a9327e42dd5759"},
		{name: "squashfs-tools.tcz", url: "http://tinycorelinux.net/16.x/x86_64/tcz/squashfs-tools.tcz", sha256: "5a637f7d9699d25d3d202dbede0b9219bda0ed6cf0eec27bd245cc158e2633c0"},
	},
}

var findUnsquashfs = func() (string, error) {
	return exec.LookPath("unsquashfs")
}

func installTinyCoreExtensionSupport(root, release string, logOutput io.Writer) error {
	artifacts, ok := tinyCoreBootstrapExtensions[release]
	if !ok || len(artifacts) == 0 {
		return fmt.Errorf("Tiny Core release %q has no extension support definition", release)
	}
	unsquashfs, err := findUnsquashfs()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return errors.New("unsquashfs is required on the host to initialize a Tiny Core rootfs; install squashfs-tools")
		}
		return fmt.Errorf("resolve host unsquashfs: %w", err)
	}

	for _, artifact := range artifacts {
		archivePath, err := downloadTinyCoreArtifact(filepath.Dir(root), artifact.name, artifact.url, artifact.sha256, tinyCoreMaximumExtensionSize, logOutput)
		if err != nil {
			return err
		}
		if err := extractTinyCoreBootstrapExtension(unsquashfs, archivePath, root, artifact.name, logOutput); err != nil {
			_ = os.Remove(archivePath)
			return err
		}
		if err := os.Remove(archivePath); err != nil {
			return fmt.Errorf("remove temporary Tiny Core extension %q: %w", artifact.name, err)
		}
	}

	if err := patchTinyCoreTCELoad(root); err != nil {
		return err
	}
	return prepareTinyCoreTCEDirectory(root)
}

func prepareTinyCoreTCEDirectory(root string) error {
	tceDir := filepath.Join(root, "etc", "sysconfig", "tcedir")
	if err := os.MkdirAll(tceDir, 0o755); err != nil {
		return fmt.Errorf("create Tiny Core extension directory %q: %w", tceDir, err)
	}
	for _, dir := range []string{filepath.Join(tceDir, "optional"), filepath.Join(tceDir, "ondemand")} {
		if err := os.MkdirAll(dir, 0o777); err != nil {
			return fmt.Errorf("create Tiny Core extension directory %q: %w", dir, err)
		}
		if err := os.Chmod(dir, 0o777); err != nil {
			return fmt.Errorf("set Tiny Core extension directory mode for %q: %w", dir, err)
		}
	}
	onBootPath := filepath.Join(tceDir, "onboot.lst")
	if err := os.WriteFile(onBootPath, nil, 0o666); err != nil {
		return fmt.Errorf("create Tiny Core onboot list: %w", err)
	}
	if err := os.Chmod(onBootPath, 0o666); err != nil {
		return fmt.Errorf("set Tiny Core onboot list mode: %w", err)
	}
	flagPath := filepath.Join(tceDir, "copy2fs.flg")
	if err := os.WriteFile(flagPath, nil, 0o644); err != nil {
		return fmt.Errorf("enable Tiny Core copy-to-filesystem extension mode: %w", err)
	}
	return nil
}

func extractTinyCoreBootstrapExtension(unsquashfs, archivePath, root, name string, logOutput io.Writer) error {
	logCommand(logOutput, unsquashfs, "-no-progress", "-f", "-d", root, archivePath)
	cmd := exec.Command(unsquashfs, "-no-progress", "-f", "-d", root, archivePath)
	output := logWriterOrDiscard(logOutput)
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("extract Tiny Core bootstrap extension %q: %w", name, err)
	}
	return nil
}

func patchTinyCoreTCELoad(root string) error {
	path := filepath.Join(root, "usr", "bin", "tce-load")
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read Tiny Core tce-load: %w", err)
	}
	const startMarker = "copyInstall() {"
	const endMarker = "\n}\n\nupdate_system()"
	start := strings.Index(string(content), startMarker)
	if start < 0 {
		return errors.New("Tiny Core tce-load does not contain copyInstall function")
	}
	relativeEnd := strings.Index(string(content[start:]), endMarker)
	if relativeEnd < 0 {
		return errors.New("Tiny Core tce-load copyInstall function has an unsupported layout")
	}
	end := start + relativeEnd + len("\n}")
	patched := string(content[:start]) + tinyCoreCopyInstallFunction + string(content[end:])
	if strings.Count(patched, startMarker) != 1 {
		return errors.New("Tiny Core tce-load patch produced an unexpected copyInstall function count")
	}
	if err := os.WriteFile(path, []byte(patched), 0o755); err != nil {
		return fmt.Errorf("write patched Tiny Core tce-load: %w", err)
	}
	return nil
}

const tinyCoreCopyInstallFunction = `copyInstall() {
	COPYDIR="$(sudo /bin/mktemp -d /tmp/tce-copy.XXXXXX)" || abort_to_saved_dir
	sudo /usr/bin/env LD_LIBRARY_PATH=/usr/local/lib /usr/local/bin/unsquashfs -no-progress -f -d "$COPYDIR" "$1" >/dev/null 2>&1 || {
		sudo /bin/rm -rf "$COPYDIR"
		abort_to_saved_dir
	}
	if [ "$(ls -A "$COPYDIR")" ]; then
		yes "$FORCE" | sudo /bin/cp -ai "$COPYDIR"/. / 2>/dev/null || {
			sudo /bin/rm -rf "$COPYDIR"
			abort_to_saved_dir
		}
		[ -n "$(find "$COPYDIR" -type d -name modules)" ] && MODULES=TRUE
	fi
	sudo /bin/rm -rf "$COPYDIR"
}`
