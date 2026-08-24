package rootfs

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

type testNewcEntry struct {
	name     string
	mode     uint64
	data     string
	inode    uint64
	links    uint64
	devMajor uint64
	devMinor uint64
}

func TestExtractNewcArchive(t *testing.T) {
	archive := buildTestNewcArchive(t, []testNewcEntry{
		{name: ".", mode: syscall.S_IFDIR | 0o755},
		{name: "bin", mode: syscall.S_IFDIR | 0o555},
		{name: "bin/tool", mode: syscall.S_IFREG | 0o4755, data: "hello\n"},
		{name: "bin/tool-link", mode: syscall.S_IFLNK | 0o777, data: "tool"},
		{name: "tmp", mode: syscall.S_IFDIR | 0o1777},
	})
	root := t.TempDir()
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "bin"), 0o755) })
	if err := extractNewcArchive(root, bytes.NewReader(archive)); err != nil {
		t.Fatalf("extractNewcArchive returned error: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, "bin", "tool"))
	if err != nil || string(content) != "hello\n" {
		t.Fatalf("unexpected extracted file content %q, err=%v", content, err)
	}
	info, err := os.Stat(filepath.Join(root, "bin", "tool"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 || info.Mode()&os.ModeSetuid == 0 {
		t.Fatalf("unexpected extracted file mode %v", info.Mode())
	}
	dirInfo, err := os.Stat(filepath.Join(root, "bin"))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o555 {
		t.Fatalf("unexpected deferred directory mode %v", dirInfo.Mode())
	}
	target, err := os.Readlink(filepath.Join(root, "bin", "tool-link"))
	if err != nil || target != "tool" {
		t.Fatalf("unexpected symlink target %q, err=%v", target, err)
	}
	tmpInfo, err := os.Stat(filepath.Join(root, "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if tmpInfo.Mode()&os.ModeSticky == 0 || tmpInfo.Mode().Perm() != 0o777 {
		t.Fatalf("unexpected sticky directory mode %v", tmpInfo.Mode())
	}
}

func TestExtractNewcArchivePreservesHardlinks(t *testing.T) {
	archive := buildTestNewcArchive(t, []testNewcEntry{
		{name: ".", mode: syscall.S_IFDIR | 0o755},
		{name: "first", mode: syscall.S_IFREG | 0o644, inode: 42, links: 2},
		{name: "second", mode: syscall.S_IFREG | 0o644, data: "shared", inode: 42, links: 2},
	})
	root := t.TempDir()
	if err := extractNewcArchive(root, bytes.NewReader(archive)); err != nil {
		t.Fatalf("extractNewcArchive returned error: %v", err)
	}
	first, err := os.Stat(filepath.Join(root, "first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.Stat(filepath.Join(root, "second"))
	if err != nil {
		t.Fatal(err)
	}
	firstStat := first.Sys().(*syscall.Stat_t)
	secondStat := second.Sys().(*syscall.Stat_t)
	if firstStat.Ino != secondStat.Ino || firstStat.Nlink != 2 {
		t.Fatalf("expected shared hardlink inode, got first=%d second=%d links=%d", firstStat.Ino, secondStat.Ino, firstStat.Nlink)
	}
}

func TestExtractNewcArchiveRejectsHostilePaths(t *testing.T) {
	tests := []struct {
		name    string
		entries []testNewcEntry
		want    string
	}{
		{
			name: "traversal",
			entries: []testNewcEntry{
				{name: ".", mode: syscall.S_IFDIR | 0o755},
				{name: "../escape", mode: syscall.S_IFREG | 0o644, data: "bad"},
			},
			want: "escapes the rootfs",
		},
		{
			name: "symlink ancestor",
			entries: []testNewcEntry{
				{name: ".", mode: syscall.S_IFDIR | 0o755},
				{name: "escape", mode: syscall.S_IFLNK | 0o777, data: "../outside"},
				{name: "escape/file", mode: syscall.S_IFREG | 0o644, data: "bad"},
			},
			want: "is a symlink",
		},
		{
			name: "device outside dev",
			entries: []testNewcEntry{
				{name: ".", mode: syscall.S_IFDIR | 0o755},
				{name: "host-device", mode: syscall.S_IFCHR | 0o600},
			},
			want: "device node is outside managed /dev",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			archive := buildTestNewcArchive(t, tc.entries)
			err := extractNewcArchive(t.TempDir(), bytes.NewReader(archive))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func buildTestNewcArchive(t *testing.T, entries []testNewcEntry) []byte {
	t.Helper()
	var archive bytes.Buffer
	for idx, entry := range append(entries, testNewcEntry{name: "TRAILER!!!", mode: syscall.S_IFREG}) {
		inode := entry.inode
		if inode == 0 {
			inode = uint64(idx + 1)
		}
		links := entry.links
		if links == 0 {
			links = 1
		}
		name := entry.name + "\x00"
		header := fmt.Sprintf(
			"070701%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x",
			inode, entry.mode, 0, 0, links, 0, len(entry.data), entry.devMajor,
			entry.devMinor, 0, 0, len(name), 0,
		)
		if len(header) != newcHeaderSize {
			t.Fatalf("test header has unexpected size %d", len(header))
		}
		archive.WriteString(header)
		archive.WriteString(name)
		writeTestNewcPadding(&archive, len(header)+len(name))
		archive.WriteString(entry.data)
		writeTestNewcPadding(&archive, len(entry.data))
	}
	return archive.Bytes()
}

func writeTestNewcPadding(buffer *bytes.Buffer, size int) {
	buffer.Write(make([]byte, (4-size%4)%4))
}
