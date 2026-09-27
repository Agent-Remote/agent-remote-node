package runtimehelper

import (
	"context"
	"encoding/hex"
	"errors"
	"path/filepath"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"golang.org/x/sys/unix"
)

func (e Engine) migrationTraversalParents(accountPath string) []string {
	aclRoot := filepath.Dir(filepath.Clean(e.config.AccountRoot))
	var parents []string
	for _, parent := range parentDirectories(accountPath) {
		if parent == aclRoot || pathInside(aclRoot, parent) {
			parents = append(parents, parent)
		}
	}
	return parents
}

func (e Engine) migrationParentPermissions(ctx context.Context, accountPath string) ([]skillmanager.MigrationParentPermissions, error) {
	paths := e.migrationTraversalParents(accountPath)
	if len(paths) == 0 || len(paths) > migrationInventoryDepth {
		return nil, errMigrationInventory
	}
	parents := make([]skillmanager.MigrationParentPermissions, 0, len(paths))
	scanner := migrationInventoryScanner{ctx: ctx, xattrBuffer: make([]byte, 64<<10)}
	for _, path := range paths {
		parent, err := scanner.parentPermissions(path)
		if err != nil {
			return nil, errMigrationInventory
		}
		parents = append(parents, parent)
	}
	return parents, nil
}

func (s *migrationInventoryScanner) parentPermissions(path string) (skillmanager.MigrationParentPermissions, error) {
	var parent skillmanager.MigrationParentPermissions
	how := &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS}
	fd, err := unix.Openat2(unix.AT_FDCWD, path, how)
	if err != nil {
		return parent, errMigrationInventory
	}
	defer unix.Close(fd)
	before, err := migrationInventoryStat(fd)
	if err != nil || s.ctx.Err() != nil {
		return parent, errMigrationInventory
	}
	attrs, err := s.xattrs(fd, fd, ".", false)
	if err != nil {
		return parent, err
	}
	parent = skillmanager.MigrationParentPermissions{Path: path, Identity: migrationObjectIdentity(before), Mode: uint32(before.Mode), UID: before.Uid, GID: before.Gid, AttributeDigest: hex.EncodeToString(attrs.all[:])}
	for name, destination := range map[string]*[]byte{"system.posix_acl_access": &parent.AccessACL, "system.posix_acl_default": &parent.DefaultACL} {
		n, err := unix.Fgetxattr(fd, name, s.xattrBuffer)
		if errors.Is(err, unix.ENODATA) {
			continue
		}
		if err != nil {
			return parent, errMigrationInventory
		}
		*destination = append([]byte(nil), s.xattrBuffer[:n]...)
	}
	check, err := unix.Openat2(unix.AT_FDCWD, path, how)
	if err != nil {
		return parent, errMigrationInventory
	}
	defer unix.Close(check)
	after, err := migrationInventoryStat(check)
	if err != nil || after != before || s.ctx.Err() != nil {
		return parent, errMigrationInventory
	}
	return parent, nil
}

func migrationObjectIdentity(stat unix.Statx_t) skillmanager.MigrationObjectIdentity {
	return skillmanager.MigrationObjectIdentity{DeviceMajor: stat.Dev_major, DeviceMinor: stat.Dev_minor, Inode: stat.Ino}
}
