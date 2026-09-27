package runtimehelper

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// A fresh migration must not merge into a retained backup, follow a linked state
// ancestor, or place copied credentials beneath a nonprivate migrations directory.
func (e Engine) createMigrationBackupDirectory(ctx context.Context, task, backup string) error {
	statePath := filepath.Clean(e.config.StateRoot)
	if ctx.Err() != nil || backup != filepath.Join(statePath, "migrations", shortDigest(task, 32)) {
		return errMigrationWritersUnknown
	}
	state, err := openMigrationStateDirectory(ctx, statePath)
	if err != nil {
		return errMigrationWritersUnknown
	}
	defer unix.Close(state)
	if ctx.Err() != nil {
		return errMigrationWritersUnknown
	}
	if err := unix.Mkdirat(state, "migrations", 0700); err != nil && !errors.Is(err, unix.EEXIST) {
		return errMigrationWritersUnknown
	}
	parent, err := unix.Openat2(state, "migrations", &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_XDEV})
	if err != nil {
		return errMigrationWritersUnknown
	}
	defer unix.Close(parent)
	if requirePrivateMigrationDirectory(parent) != nil || ctx.Err() != nil {
		return errMigrationWritersUnknown
	}
	// EEXIST is deliberately terminal, including empty directories and dangling links.
	if err := unix.Mkdirat(parent, shortDigest(task, 32), 0700); err != nil {
		return errMigrationWritersUnknown
	}
	if unix.Fsync(parent) != nil || unix.Fsync(state) != nil {
		return errMigrationWritersUnknown
	}
	return nil
}

func openMigrationStateDirectory(ctx context.Context, path string) (int, error) {
	if ctx.Err() != nil || !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" || len(path) > migrationInventoryPath {
		return -1, errMigrationWritersUnknown
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) > migrationInventoryDepth {
		return -1, errMigrationWritersUnknown
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, errMigrationWritersUnknown
	}
	for _, part := range parts {
		if ctx.Err() != nil {
			_ = unix.Close(fd)
			return -1, errMigrationWritersUnknown
		}
		how := &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
			Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS}
		next, err := unix.Openat2(fd, part, how)
		if errors.Is(err, unix.ENOENT) {
			if err = unix.Mkdirat(fd, part, 0700); err == nil {
				err = unix.Fsync(fd)
			}
			if err == nil {
				next, err = unix.Openat2(fd, part, how)
			}
		}
		_ = unix.Close(fd)
		if err != nil {
			return -1, errMigrationWritersUnknown
		}
		fd = next
	}
	stat, err := migrationInventoryStat(fd)
	// The runtime root intentionally grants traversal to the Worker/session identities.
	// Only the migrations child contains backups and must be inaccessible to those users.
	if err != nil || stat.Uid != 0 || stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0700 != 0700 || stat.Mode&07022 != 0 {
		_ = unix.Close(fd)
		return -1, errMigrationWritersUnknown
	}
	return fd, nil
}

func requirePrivateMigrationDirectory(fd int) error {
	stat, err := migrationInventoryStat(fd)
	if err != nil || stat.Mode != unix.S_IFDIR|0700 || stat.Uid != 0 {
		return errMigrationWritersUnknown
	}
	return nil
}
