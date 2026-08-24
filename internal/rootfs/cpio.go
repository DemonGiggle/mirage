package rootfs

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const (
	newcHeaderSize            = 110
	newcMagic                 = "070701"
	newcMaximumEntryCount     = 100_000
	newcMaximumEntrySize      = 256 << 20
	newcMaximumExtractedBytes = 512 << 20
)

type newcHeader struct {
	inode     uint64
	mode      uint64
	links     uint64
	fileSize  uint64
	devMajor  uint64
	devMinor  uint64
	rdevMajor uint64
	rdevMinor uint64
	nameSize  uint64
}

type newcHardlinkKey struct {
	deviceMajor uint64
	deviceMinor uint64
	inode       uint64
}

type newcHardlinkGroup struct {
	canonical string
	pending   []string
	mode      os.FileMode
}

// extractNewcArchive extracts a Linux newc initramfs without following archive
// symlinks or materializing device nodes. Tiny Core ships its base rootfs in
// this format.
func extractNewcArchive(root string, input io.Reader) error {
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("lstat extraction root %q: %w", root, err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return fmt.Errorf("extraction root %q must be a real directory", root)
	}

	reader := bufio.NewReader(input)
	hardlinks := make(map[newcHardlinkKey]*newcHardlinkGroup)
	var directories []deferredArchiveDirectory
	var extractedBytes uint64
	entryCount := 0
	for {
		entryCount++
		if entryCount > newcMaximumEntryCount {
			return fmt.Errorf("newc archive exceeds the %d-entry limit", newcMaximumEntryCount)
		}
		headerBytes := make([]byte, newcHeaderSize)
		if _, err := io.ReadFull(reader, headerBytes); err != nil {
			if errors.Is(err, io.EOF) {
				return errors.New("newc archive is missing TRAILER!!!")
			}
			return fmt.Errorf("read newc header: %w", err)
		}
		header, err := parseNewcHeader(headerBytes)
		if err != nil {
			return err
		}
		if header.nameSize == 0 || header.nameSize > 1<<20 {
			return fmt.Errorf("newc entry has invalid name size %d", header.nameSize)
		}
		nameBytes := make([]byte, header.nameSize)
		if _, err := io.ReadFull(reader, nameBytes); err != nil {
			return fmt.Errorf("read newc entry name: %w", err)
		}
		if nameBytes[len(nameBytes)-1] != 0 || strings.IndexByte(string(nameBytes[:len(nameBytes)-1]), 0) >= 0 {
			return errors.New("newc entry name is not a single NUL-terminated string")
		}
		rawName := string(nameBytes[:len(nameBytes)-1])
		if err := discardNewcPadding(reader, newcHeaderSize+int(header.nameSize)); err != nil {
			return fmt.Errorf("read newc name padding for %q: %w", rawName, err)
		}
		if rawName == "TRAILER!!!" {
			if header.fileSize != 0 {
				return errors.New("newc TRAILER!!! entry contains data")
			}
			if err := materializeEmptyNewcHardlinks(hardlinks); err != nil {
				return err
			}
			for idx := len(directories) - 1; idx >= 0; idx-- {
				if err := os.Chmod(directories[idx].path, directories[idx].mode); err != nil {
					return fmt.Errorf("set archive directory mode for %q: %w", directories[idx].path, err)
				}
			}
			return nil
		}

		name, err := cleanArchivePath(rawName)
		if err != nil {
			return err
		}
		if header.fileSize > newcMaximumEntrySize {
			return fmt.Errorf("newc entry %q exceeds the %d MiB size limit", rawName, newcMaximumEntrySize>>20)
		}
		extractedBytes += header.fileSize
		if extractedBytes > newcMaximumExtractedBytes {
			return fmt.Errorf("newc archive exceeds the %d MiB extracted-size limit", newcMaximumExtractedBytes>>20)
		}
		if err := extractNewcEntry(root, name, header, reader, hardlinks, &directories); err != nil {
			return fmt.Errorf("extract newc entry %q: %w", rawName, err)
		}
		if err := discardNewcPadding(reader, int(header.fileSize)); err != nil {
			return fmt.Errorf("read newc data padding for %q: %w", rawName, err)
		}
	}
}

func parseNewcHeader(data []byte) (newcHeader, error) {
	if len(data) != newcHeaderSize {
		return newcHeader{}, fmt.Errorf("newc header has size %d, want %d", len(data), newcHeaderSize)
	}
	magic := string(data[:6])
	if magic != newcMagic {
		return newcHeader{}, fmt.Errorf("unsupported cpio magic %q", magic)
	}
	fields := make([]uint64, 13)
	for idx := range fields {
		start := 6 + idx*8
		field := data[start : start+8]
		if _, err := hex.Decode(make([]byte, 4), field); err != nil {
			return newcHeader{}, fmt.Errorf("parse newc header field %d: %w", idx, err)
		}
		value, err := strconv.ParseUint(string(field), 16, 32)
		if err != nil {
			return newcHeader{}, fmt.Errorf("parse newc header field %d: %w", idx, err)
		}
		fields[idx] = value
	}
	return newcHeader{
		inode: fields[0], mode: fields[1], links: fields[4], fileSize: fields[6],
		devMajor: fields[7], devMinor: fields[8], rdevMajor: fields[9],
		rdevMinor: fields[10], nameSize: fields[11],
	}, nil
}

func extractNewcEntry(root, name string, header newcHeader, reader io.Reader, hardlinks map[newcHardlinkKey]*newcHardlinkGroup, directories *[]deferredArchiveDirectory) error {
	mode := newcFileMode(header.mode)
	fileType := header.mode & syscall.S_IFMT
	if name == "." {
		if fileType != syscall.S_IFDIR {
			return errors.New("archive root entry is not a directory")
		}
		*directories = append(*directories, deferredArchiveDirectory{path: root, mode: mode})
		return nil
	}

	if fileType == syscall.S_IFCHR || fileType == syscall.S_IFBLK {
		if name != "dev" && !strings.HasPrefix(filepath.ToSlash(name), "dev/") {
			return errors.New("device node is outside managed /dev")
		}
		_, err := io.CopyN(io.Discard, reader, int64(header.fileSize))
		return err
	}

	target, err := prepareArchiveTarget(root, name)
	if err != nil {
		return err
	}
	switch fileType {
	case syscall.S_IFDIR:
		if header.fileSize != 0 {
			return errors.New("directory entry contains data")
		}
		if err := os.Mkdir(target, 0o755); err != nil {
			return err
		}
		*directories = append(*directories, deferredArchiveDirectory{path: target, mode: mode})
		return nil
	case syscall.S_IFREG:
		return extractNewcRegularFile(target, header, mode, reader, hardlinks)
	case syscall.S_IFLNK:
		linkBytes := make([]byte, header.fileSize)
		if _, err := io.ReadFull(reader, linkBytes); err != nil {
			return err
		}
		if len(linkBytes) == 0 || strings.IndexByte(string(linkBytes), 0) >= 0 {
			return errors.New("symlink has an invalid target")
		}
		return os.Symlink(string(linkBytes), target)
	case syscall.S_IFIFO:
		if header.fileSize != 0 {
			return errors.New("FIFO entry contains data")
		}
		return syscall.Mkfifo(target, uint32(mode.Perm()))
	case syscall.S_IFSOCK:
		return errors.New("socket entries are not supported")
	default:
		return fmt.Errorf("unsupported file mode %#o", header.mode)
	}
}

func newcFileMode(raw uint64) os.FileMode {
	mode := os.FileMode(raw & 0o777)
	if raw&syscall.S_ISUID != 0 {
		mode |= os.ModeSetuid
	}
	if raw&syscall.S_ISGID != 0 {
		mode |= os.ModeSetgid
	}
	if raw&syscall.S_ISVTX != 0 {
		mode |= os.ModeSticky
	}
	return mode
}

func extractNewcRegularFile(target string, header newcHeader, mode os.FileMode, reader io.Reader, hardlinks map[newcHardlinkKey]*newcHardlinkGroup) error {
	if header.links <= 1 {
		return createNewcRegularFile(target, mode, reader, header.fileSize)
	}
	key := newcHardlinkKey{deviceMajor: header.devMajor, deviceMinor: header.devMinor, inode: header.inode}
	group := hardlinks[key]
	if group == nil {
		group = &newcHardlinkGroup{mode: mode}
		hardlinks[key] = group
	}
	if header.fileSize == 0 && group.canonical == "" {
		group.pending = append(group.pending, target)
		return nil
	}
	if group.canonical != "" {
		if header.fileSize != 0 {
			return errors.New("hardlink group contains multiple data entries")
		}
		return os.Link(group.canonical, target)
	}
	if err := createNewcRegularFile(target, mode, reader, header.fileSize); err != nil {
		return err
	}
	group.canonical = target
	for _, pending := range group.pending {
		if err := os.Link(target, pending); err != nil {
			return err
		}
	}
	group.pending = nil
	return nil
}

func materializeEmptyNewcHardlinks(groups map[newcHardlinkKey]*newcHardlinkGroup) error {
	for _, group := range groups {
		if group.canonical != "" || len(group.pending) == 0 {
			continue
		}
		canonical := group.pending[0]
		if err := createNewcRegularFile(canonical, group.mode, strings.NewReader(""), 0); err != nil {
			return err
		}
		for _, target := range group.pending[1:] {
			if err := os.Link(canonical, target); err != nil {
				return err
			}
		}
	}
	return nil
}

func createNewcRegularFile(target string, mode os.FileMode, reader io.Reader, size uint64) error {
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.CopyN(file, reader, int64(size)); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Chmod(target, mode)
}

func discardNewcPadding(reader io.Reader, size int) error {
	padding := (4 - size%4) % 4
	_, err := io.CopyN(io.Discard, reader, int64(padding))
	return err
}
