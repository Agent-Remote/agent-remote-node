//go:build linux

package runtimehelper

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/user"
	"sort"
	"strconv"

	"github.com/Agent-Remote/agent-remote-node/internal/workspace"
	"golang.org/x/sys/unix"
)

func (e Engine) prepareSyncDirectory(ctx context.Context, directory *workspace.Directory, userID string) error {
	identity, err := e.ensureIdentity(userID)
	if err != nil {
		return err
	}
	node, err := user.Lookup(e.config.NodeUser)
	if err != nil {
		return errors.New("workspace node identity is unavailable")
	}
	nodeUID, err := strconv.ParseUint(node.Uid, 10, 32)
	if err != nil {
		return errors.New("workspace node identity is invalid")
	}
	remaining := 1000000
	if err := normalizeSyncTree(ctx, directory.File, identity, uint32(nodeUID), 0, &remaining); err != nil {
		return err
	}
	for _, parent := range directory.Parents {
		if err := grantSyncTraverse(ctx, parent, uint32(identity.UID), uint32(nodeUID)); err != nil {
			return err
		}
	}
	return nil
}

func normalizeSyncTree(ctx context.Context, file *os.File, identity runtimeIdentity, nodeUID uint32, depth int, remaining *int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > 256 || *remaining <= 0 {
		return errors.New("workspace normalization limit exceeded")
	}
	*remaining--
	fd := int(file.Fd())
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	kind := stat.Mode & unix.S_IFMT
	if kind != unix.S_IFDIR && kind != unix.S_IFREG {
		return errors.New("workspace contains unsupported file type")
	}
	if kind == unix.S_IFREG && stat.Nlink != 1 {
		return errors.New("workspace contains a hard-linked file")
	}
	permissions := uint16(6)
	if kind == unix.S_IFDIR || stat.Mode&0100 != 0 {
		permissions = 7
	}
	if err := unix.Fchown(fd, identity.UID, identity.GID); err != nil {
		return err
	}
	if err := unix.Fchmod(fd, uint32(permissions<<6|permissions<<3)); err != nil {
		return err
	}
	if err := unix.Fsetxattr(fd, "system.posix_acl_access", syncDataACL(uint32(identity.UID), nodeUID, permissions), 0); err != nil {
		return err
	}
	if kind != unix.S_IFDIR {
		return nil
	}
	if err := unix.Fsetxattr(fd, "system.posix_acl_default", syncDataACL(uint32(identity.UID), nodeUID, 7), 0); err != nil {
		return err
	}
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}
	for {
		entries, err := file.ReadDir(128)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		for _, entry := range entries {
			var childStat unix.Stat_t
			if err := unix.Fstatat(fd, entry.Name(), &childStat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				return errors.New("workspace changed during permission preparation")
			}
			childKind := childStat.Mode & unix.S_IFMT
			if childKind == unix.S_IFLNK {
				continue
			}
			if childKind != unix.S_IFDIR && childKind != unix.S_IFREG {
				return errors.New("workspace contains unsupported file type")
			}
			flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
			if childKind == unix.S_IFDIR {
				flags |= unix.O_DIRECTORY
			}
			childFD, err := unix.Openat(fd, entry.Name(), flags, 0)
			if err != nil {
				return errors.New("workspace changed during permission preparation")
			}
			child := os.NewFile(uintptr(childFD), entry.Name())
			childErr := normalizeSyncTree(ctx, child, identity, nodeUID, depth+1, remaining)
			_ = child.Close()
			if childErr != nil {
				return childErr
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	return nil
}

func syncDataACL(runtimeUID, nodeUID uint32, permissions uint16) []byte {
	// Linux POSIX ACL xattrs contain version 2 and sorted fixed-width entries.
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, 2)
	entry := func(tag, permission uint16, id uint32) {
		item := make([]byte, 8)
		binary.LittleEndian.PutUint16(item, tag)
		binary.LittleEndian.PutUint16(item[2:], permission)
		binary.LittleEndian.PutUint32(item[4:], id)
		data = append(data, item...)
	}
	const undefined = ^uint32(0)
	entry(0x01, permissions, undefined)
	ids := []uint32{runtimeUID}
	if nodeUID != runtimeUID {
		ids = append(ids, nodeUID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		entry(0x02, permissions, id)
	}
	entry(0x04, permissions, undefined)
	entry(0x10, permissions, undefined)
	entry(0x20, 0, undefined)
	return data
}

func requireSyncPreparationPlatform() error { return nil }
