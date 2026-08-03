package rootfs

import (
	"archive/tar"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

type archiveTestEntry struct {
	header tar.Header
	data   string
}

func TestSelectBootstrapStrategy(t *testing.T) {
	tests := []struct {
		name string
		euid int
		want string
	}{
		{name: "root host", euid: 0, want: "root"},
		{name: "rootless host", euid: 1000, want: "rootless"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := string(selectBootstrapStrategy(tc.euid).hostEnvironment())
			if got != tc.want {
				t.Fatalf("selected host environment %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExtractRootlessArchiveNormalizesOwnershipAndPreservesModes(t *testing.T) {
	archivePath := writeArchiveFixture(t, []archiveTestEntry{
		{header: tar.Header{Name: "./", Typeflag: tar.TypeDir, Mode: 0o755}},
		{header: tar.Header{Name: "./bin", Typeflag: tar.TypeDir, Mode: 0o751}},
		{header: tar.Header{Name: "./bin/tool", Typeflag: tar.TypeReg, Mode: 0o4755, Size: 3, Uid: 42, Gid: 43}, data: "run"},
		{header: tar.Header{Name: "./bin/tool-hardlink", Typeflag: tar.TypeLink, Linkname: "./bin/tool"}},
		{header: tar.Header{Name: "./bin/tool-symlink", Typeflag: tar.TypeSymlink, Linkname: "tool"}},
		{header: tar.Header{Name: "./dev/null", Typeflag: tar.TypeChar, Mode: 0o666, Devmajor: 1, Devminor: 3}},
	})
	root := filepath.Join(t.TempDir(), "rootfs")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("create extraction root: %v", err)
	}

	if err := extractRootlessArchive(root, archivePath); err != nil {
		t.Fatalf("extractRootlessArchive returned error: %v", err)
	}

	tool := filepath.Join(root, "bin", "tool")
	data, err := os.ReadFile(tool)
	if err != nil {
		t.Fatalf("read extracted tool: %v", err)
	}
	if string(data) != "run" {
		t.Fatalf("unexpected extracted content %q", string(data))
	}
	info, err := os.Stat(tool)
	if err != nil {
		t.Fatalf("stat extracted tool: %v", err)
	}
	if got := info.Mode() & (os.ModePerm | os.ModeSetuid); got != 0o755|os.ModeSetuid {
		t.Fatalf("unexpected extracted mode %v", got)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("extracted stat does not contain syscall.Stat_t")
	}
	if int(stat.Uid) != os.Geteuid() || int(stat.Gid) != os.Getegid() {
		t.Fatalf("ownership was not normalized: got %d:%d want %d:%d", stat.Uid, stat.Gid, os.Geteuid(), os.Getegid())
	}

	hardlinkInfo, err := os.Stat(filepath.Join(root, "bin", "tool-hardlink"))
	if err != nil {
		t.Fatalf("stat hardlink: %v", err)
	}
	hardlinkStat := hardlinkInfo.Sys().(*syscall.Stat_t)
	if hardlinkStat.Ino != stat.Ino {
		t.Fatalf("hardlink inode %d does not match source inode %d", hardlinkStat.Ino, stat.Ino)
	}
	link, err := os.Readlink(filepath.Join(root, "bin", "tool-symlink"))
	if err != nil {
		t.Fatalf("read extracted symlink: %v", err)
	}
	if link != "tool" {
		t.Fatalf("unexpected symlink target %q", link)
	}
	if _, err := os.Lstat(filepath.Join(root, "dev", "null")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected managed /dev node to be omitted, got %v", err)
	}
	binInfo, err := os.Stat(filepath.Join(root, "bin"))
	if err != nil {
		t.Fatalf("stat extracted bin directory: %v", err)
	}
	if binInfo.Mode().Perm() != 0o751 {
		t.Fatalf("unexpected bin directory mode %o", binInfo.Mode().Perm())
	}
}

func TestExtractRootlessArchiveRejectsTraversal(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	archivePath := writeArchiveFixture(t, []archiveTestEntry{
		{header: tar.Header{Name: "../outside", Typeflag: tar.TypeReg, Mode: 0o644, Size: 3}, data: "bad"},
	})
	root := filepath.Join(base, "rootfs")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("create extraction root: %v", err)
	}

	err := extractRootlessArchive(root, archivePath)
	if err == nil || !strings.Contains(err.Error(), "escapes the rootfs") {
		t.Fatalf("expected traversal rejection, got %v", err)
	}
	if _, err := os.Stat(outside); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("archive traversal created an outside file: %v", err)
	}
}

func TestExtractRootlessArchiveRejectsSymlinkAncestor(t *testing.T) {
	outside := t.TempDir()
	archivePath := writeArchiveFixture(t, []archiveTestEntry{
		{header: tar.Header{Name: "escape", Typeflag: tar.TypeSymlink, Linkname: outside}},
		{header: tar.Header{Name: "escape/payload", Typeflag: tar.TypeReg, Mode: 0o644, Size: 3}, data: "bad"},
	})
	root := filepath.Join(t.TempDir(), "rootfs")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("create extraction root: %v", err)
	}

	err := extractRootlessArchive(root, archivePath)
	if err == nil || !strings.Contains(err.Error(), "is a symlink") {
		t.Fatalf("expected symlink ancestor rejection, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "payload")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("symlink ancestor escape created an outside file: %v", err)
	}
}

func TestExtractRootlessArchiveRejectsWrongFileTypeTarget(t *testing.T) {
	archivePath := writeArchiveFixture(t, []archiveTestEntry{
		{header: tar.Header{Name: "target", Typeflag: tar.TypeDir, Mode: 0o755}},
		{header: tar.Header{Name: "target", Typeflag: tar.TypeReg, Mode: 0o644, Size: 3}, data: "bad"},
	})
	root := filepath.Join(t.TempDir(), "rootfs")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("create extraction root: %v", err)
	}

	err := extractRootlessArchive(root, archivePath)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected existing target rejection, got %v", err)
	}
}

func TestRootlessStrategyRejectsSymlinkOutputBeforeOverwrite(t *testing.T) {
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	output := filepath.Join(t.TempDir(), "rootfs")
	if err := os.Symlink(outside, output); err != nil {
		t.Fatalf("create output symlink: %v", err)
	}

	err := (rootlessHostBootstrapStrategy{}).prepareOutput(output, true)
	if err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("expected symlink output rejection, got %v", err)
	}
	data, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("read sentinel after rejection: %v", err)
	}
	if string(data) != "keep" {
		t.Fatalf("sentinel changed to %q", string(data))
	}
}

func writeArchiveFixture(t *testing.T, entries []archiveTestEntry) string {
	t.Helper()
	archivePath := filepath.Join(t.TempDir(), "rootfs.tar")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("create archive fixture: %v", err)
	}
	writer := tar.NewWriter(file)
	for _, entry := range entries {
		header := entry.header
		if err := writer.WriteHeader(&header); err != nil {
			t.Fatalf("write archive header %q: %v", header.Name, err)
		}
		if entry.data != "" {
			if _, err := writer.Write([]byte(entry.data)); err != nil {
				t.Fatalf("write archive content %q: %v", header.Name, err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close archive writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close archive fixture: %v", err)
	}
	return archivePath
}
