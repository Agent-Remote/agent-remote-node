package runtimehelper

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

type migrationPermissionRestorer struct {
	ctx     context.Context
	links   [2]map[migrationInode]*migrationHardlink
	mounts  [2]uint64
	buffer  []byte
	entries int
}

func restoreMigrationTreePermissions(ctx context.Context, account, backup string, expected [2]migrationInventory) error {
	r := migrationPermissionRestorer{ctx: ctx, buffer: make([]byte, 64<<10)}
	var roots [2]*os.File
	var stats [2]unix.Statx_t
	for index, path := range []string{account, backup} {
		observed, links, err := scanMigrationInventoryWithLinks(ctx, path)
		if err != nil || observed != expected[index] {
			return errMigrationInventory
		}
		r.links[index] = links
		fd, err := unix.Openat2(unix.AT_FDCWD, path, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
			Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS})
		if err != nil {
			return errMigrationInventory
		}
		roots[index] = os.NewFile(uintptr(fd), "migration permission root")
		defer roots[index].Close()
		stats[index], err = migrationInventoryStat(fd)
		if err != nil || migrationStatInode(stats[index]) != expected[index].root {
			return errMigrationInventory
		}
		r.mounts[index] = stats[index].Mnt_id
	}
	if expected[0].root == expected[1].root || expected[0].content != expected[1].content {
		return errMigrationInventory
	}
	return r.entry(roots, stats, 0)
}

func (r *migrationPermissionRestorer) entry(files [2]*os.File, stats [2]unix.Statx_t, depth int) error {
	if r.ctx.Err() != nil || depth > migrationInventoryDepth || stats[0].Mode&unix.S_IFMT != stats[1].Mode&unix.S_IFMT {
		return errMigrationInventory
	}
	kind := stats[0].Mode & unix.S_IFMT
	if kind == unix.S_IFDIR {
		if err := r.directory(files, depth); err != nil {
			return err
		}
	} else {
		// Only inodes whose complete link set was inspected may receive ownership writes.
		// Comparing canonical first paths also rejects swaps between already-known inodes.
		left, right := r.links[0][migrationStatInode(stats[0])], r.links[1][migrationStatInode(stats[1])]
		if left == nil || right == nil || left.first != right.first || left.expected != stats[0].Nlink || right.expected != stats[1].Nlink {
			return errMigrationInventory
		}
	}
	if r.ctx.Err() != nil {
		return errMigrationInventory
	}
	if kind == unix.S_IFLNK {
		return unix.Fchownat(int(files[0].Fd()), "", int(stats[1].Uid), int(stats[1].Gid), unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW)
	}
	fd := int(files[0].Fd())
	if err := unix.Fchown(fd, int(stats[1].Uid), int(stats[1].Gid)); err != nil {
		return err
	}
	if err := unix.Fchmod(fd, uint32(stats[1].Mode)&07777); err != nil {
		return err
	}
	// chown clears set-ID bits and file capabilities. Restore mode first, then ACLs,
	// and capabilities last; ordinary data xattrs and file contents are never rewritten.
	for _, name := range []string{"system.posix_acl_access", "system.posix_acl_default", "security.capability"} {
		n, err := unix.Fgetxattr(int(files[1].Fd()), name, r.buffer)
		present := err == nil
		if err != nil && !errors.Is(err, unix.ENODATA) {
			return err
		}
		if !present {
			n = 0
		}
		if err := restoreMigrationAttribute(r.ctx, fd, name, r.buffer[:n], present); err != nil {
			return err
		}
	}
	return r.ctx.Err()
}

func (r *migrationPermissionRestorer) directory(files [2]*os.File, depth int) error {
	for {
		if r.ctx.Err() != nil {
			return errMigrationInventory
		}
		names, err := files[1].Readdirnames(256)
		for _, name := range names {
			r.entries++
			if r.entries > migrationInventoryEntries || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") {
				return errMigrationInventory
			}
			if err := r.child(files, name, depth+1); err != nil {
				return err
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (r *migrationPermissionRestorer) child(parents [2]*os.File, name string, depth int) error {
	var files [2]*os.File
	var stats [2]unix.Statx_t
	for index, parent := range parents {
		file, stat, err := openMigrationPermissionEntry(int(parent.Fd()), name, r.mounts[index])
		if err != nil {
			return err
		}
		files[index], stats[index] = file, stat
		defer file.Close()
	}
	return r.entry(files, stats, depth)
}

func openMigrationPermissionEntry(parent int, name string, mount uint64) (*os.File, unix.Statx_t, error) {
	var stat unix.Statx_t
	fd, err := unix.Openat2(parent, name, &unix.OpenHow{Flags: unix.O_PATH | unix.O_NOFOLLOW | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_XDEV | unix.RESOLVE_NO_MAGICLINKS})
	if err != nil {
		return nil, stat, errMigrationInventory
	}
	stat, err = migrationInventoryStat(fd)
	kind := stat.Mode & unix.S_IFMT
	if err != nil || stat.Mnt_id != mount || kind != unix.S_IFDIR && kind != unix.S_IFREG && kind != unix.S_IFLNK {
		_ = unix.Close(fd)
		return nil, stat, errMigrationInventory
	}
	if kind == unix.S_IFLNK {
		return os.NewFile(uintptr(fd), "migration permission link"), stat, nil
	}
	defer unix.Close(fd)
	flags := uint64(unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK)
	if kind == unix.S_IFDIR {
		flags |= unix.O_DIRECTORY
	}
	read, err := unix.Openat2(parent, name, &unix.OpenHow{Flags: flags,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_XDEV | unix.RESOLVE_NO_SYMLINKS})
	if err != nil {
		return nil, stat, errMigrationInventory
	}
	checked, err := migrationInventoryStat(read)
	if err != nil || checked != stat {
		_ = unix.Close(read)
		return nil, stat, errMigrationInventory
	}
	return os.NewFile(uintptr(read), "migration permission entry"), stat, nil
}

func restoreMigrationAttribute(ctx context.Context, fd int, name string, value []byte, present bool) error {
	if ctx.Err() != nil {
		return errMigrationInventory
	}
	if present {
		return unix.Fsetxattr(fd, name, value, 0)
	}
	err := unix.Fremovexattr(fd, name)
	if errors.Is(err, unix.ENODATA) {
		return nil
	}
	return err
}

func migrationStatInode(stat unix.Statx_t) migrationInode {
	return migrationInode{stat.Dev_major, stat.Dev_minor, stat.Ino}
}
