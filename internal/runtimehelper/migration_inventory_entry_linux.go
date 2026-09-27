package runtimehelper

import (
	"crypto/sha256"
	"encoding/binary"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func (s *migrationInventoryScanner) child(parent int, name, path string, depth int) error {
	fd, err := unix.Openat2(parent, name, &unix.OpenHow{Flags: unix.O_PATH | unix.O_NOFOLLOW | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_XDEV | unix.RESOLVE_NO_MAGICLINKS})
	if err != nil {
		return errMigrationInventory
	}
	defer unix.Close(fd)
	before, err := migrationInventoryStat(fd)
	if err != nil || before.Mnt_id != s.mount {
		return errMigrationInventory
	}
	var file *os.File
	switch before.Mode & unix.S_IFMT {
	case unix.S_IFREG, unix.S_IFDIR:
		flags := uint64(unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK)
		if before.Mode&unix.S_IFMT == unix.S_IFDIR {
			flags |= unix.O_DIRECTORY
		}
		readFD, err := unix.Openat2(parent, name, &unix.OpenHow{Flags: flags, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_XDEV | unix.RESOLVE_NO_SYMLINKS})
		if err != nil {
			return errMigrationInventory
		}
		file = os.NewFile(uintptr(readFD), "migration inventory")
		defer file.Close()
		opened, err := migrationInventoryStat(readFD)
		if err != nil || opened != before {
			return errMigrationInventory
		}
	case unix.S_IFLNK:
		// A duplicate gives os.File an independent lifetime.
		duplicate, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 0)
		if err != nil {
			return errMigrationInventory
		}
		file = os.NewFile(uintptr(duplicate), "migration inventory link")
		defer file.Close()
	default:
		return errMigrationInventory
	}
	if err := s.entry(file, parent, name, path, before, depth); err != nil {
		return err
	}
	var after unix.Statx_t
	if err := unix.Statx(parent, name, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BASIC_STATS|unix.STATX_MNT_ID, &after); err != nil {
		return errMigrationInventory
	}
	after.Atime = unix.StatxTimestamp{}
	if after != before {
		return errMigrationInventory
	}
	return nil
}

func (s *migrationInventoryScanner) entry(file *os.File, parent int, name, path string, before unix.Statx_t, depth int) error {
	if s.ctx.Err() != nil {
		return errMigrationInventory
	}
	kind := before.Mode & unix.S_IFMT
	migrationInventoryField(s.content, path)
	_ = binary.Write(s.content, binary.BigEndian, kind)
	migrationInventoryField(s.permissions, path)
	for _, value := range []uint32{uint32(before.Mode), before.Uid, before.Gid} {
		_ = binary.Write(s.permissions, binary.BigEndian, value)
	}
	attrs, err := s.xattrs(int(file.Fd()), parent, name, kind == unix.S_IFLNK)
	if err != nil {
		return err
	}
	_, _ = s.permissions.Write(attrs.all[:])
	_, _ = s.content.Write(attrs.content[:])
	migrationInventoryField(s.observation, path)
	_ = binary.Write(s.observation, binary.BigEndian, before)
	_, _ = s.observation.Write(attrs.all[:])
	switch kind {
	case unix.S_IFDIR:
		if err := s.directory(file, path, depth); err != nil {
			return err
		}
	case unix.S_IFREG, unix.S_IFLNK:
		key := migrationInode{before.Dev_major, before.Dev_minor, before.Ino}
		link := s.links[key]
		if link == nil {
			link = &migrationHardlink{first: path, expected: before.Nlink}
			s.links[key] = link
		}
		link.seen++
		if link.expected != before.Nlink || link.seen > link.expected {
			return errMigrationInventory
		}
		migrationInventoryField(s.content, link.first)
		if kind == unix.S_IFLNK {
			buffer := make([]byte, migrationInventoryPath+1)
			n, err := unix.Readlinkat(int(file.Fd()), "", buffer)
			if err != nil || n > migrationInventoryPath {
				return errMigrationInventory
			}
			migrationInventoryField(s.content, string(buffer[:n]))
		} else if err := s.regular(file, before.Size); err != nil {
			return err
		}
	default:
		return errMigrationInventory
	}
	after, err := migrationInventoryStat(int(file.Fd()))
	if err != nil || after != before || s.ctx.Err() != nil {
		return errMigrationInventory
	}
	return nil
}

func (s *migrationInventoryScanner) regular(file *os.File, size uint64) error {
	if size > 1<<63-1 {
		return errMigrationInventory
	}
	h := sha256.New()
	remaining := int64(size)
	for remaining > 0 {
		if s.ctx.Err() != nil {
			return errMigrationInventory
		}
		buffer := s.buffer
		if remaining < int64(len(buffer)) {
			buffer = buffer[:remaining]
		}
		n, err := io.ReadFull(file, buffer)
		if err != nil {
			return errMigrationInventory
		}
		_, _ = h.Write(buffer[:n])
		remaining -= int64(n)
	}
	if n, err := file.Read(s.buffer[:1]); n != 0 || err != io.EOF {
		return errMigrationInventory
	}
	_ = binary.Write(s.content, binary.BigEndian, size)
	_, _ = s.content.Write(h.Sum(nil))
	return nil
}
