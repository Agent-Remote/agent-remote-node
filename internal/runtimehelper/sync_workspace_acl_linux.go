//go:build linux

package runtimehelper

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"sort"

	"golang.org/x/sys/unix"
)

type syncACLEntry struct {
	tag, permissions uint16
	id               uint32
}

func grantSyncTraverse(ctx context.Context, directory *os.File, runtimeUID, nodeUID uint32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fd := int(directory.Fd())
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	entries, err := readSyncACL(fd, stat.Mode)
	if err != nil {
		return err
	}
	// Widening the mask must not expose previously masked rights of other principals.
	oldMask := uint16((stat.Mode >> 3) & 7)
	for _, entry := range entries {
		if entry.tag == 0x10 {
			oldMask = entry.permissions
		}
	}
	for i := range entries {
		if entries[i].tag == 0x02 || entries[i].tag == 0x04 || entries[i].tag == 0x08 {
			entries[i].permissions &= oldMask
		}
	}
	ids := []uint32{runtimeUID}
	if nodeUID != runtimeUID {
		ids = append(ids, nodeUID)
	}
	for _, id := range ids {
		found := false
		for i := range entries {
			if (entries[i].tag == 0x01 && id == stat.Uid) || (entries[i].tag == 0x02 && entries[i].id == id) {
				entries[i].permissions |= 1
				found = true
			}
		}
		if !found {
			entries = append(entries, syncACLEntry{0x02, 1, id})
		}
	}
	mask := false
	for i := range entries {
		if entries[i].tag == 0x10 {
			entries[i].permissions |= 1
			mask = true
		}
	}
	if !mask {
		entries = append(entries, syncACLEntry{0x10, uint16((stat.Mode>>3)&7) | 1, ^uint32(0)})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].tag != entries[j].tag {
			return entries[i].tag < entries[j].tag
		}
		return entries[i].id < entries[j].id
	})
	data := make([]byte, 4+8*len(entries))
	binary.LittleEndian.PutUint32(data, 2)
	for i, entry := range entries {
		offset := 4 + 8*i
		binary.LittleEndian.PutUint16(data[offset:], entry.tag)
		binary.LittleEndian.PutUint16(data[offset+2:], entry.permissions)
		binary.LittleEndian.PutUint32(data[offset+4:], entry.id)
	}
	return unix.Fsetxattr(fd, "system.posix_acl_access", data, 0)
}

func readSyncACL(fd int, mode uint32) ([]syncACLEntry, error) {
	const name = "system.posix_acl_access"
	size, err := unix.Fgetxattr(fd, name, nil)
	if errors.Is(err, unix.ENODATA) {
		return []syncACLEntry{{0x01, uint16((mode >> 6) & 7), ^uint32(0)}, {0x04, uint16((mode >> 3) & 7), ^uint32(0)}, {0x20, uint16(mode & 7), ^uint32(0)}}, nil
	}
	if err != nil {
		return nil, err
	}
	if size < 4 || size > 65536 || (size-4)%8 != 0 {
		return nil, errors.New("workspace ancestor ACL is invalid")
	}
	data := make([]byte, size)
	count, err := unix.Fgetxattr(fd, name, data)
	if err != nil {
		return nil, err
	}
	if count != size || binary.LittleEndian.Uint32(data) != 2 {
		return nil, errors.New("workspace ancestor ACL changed")
	}
	entries := make([]syncACLEntry, 0, (size-4)/8)
	for i := 4; i < size; i += 8 {
		entries = append(entries, syncACLEntry{binary.LittleEndian.Uint16(data[i:]), binary.LittleEndian.Uint16(data[i+2:]), binary.LittleEndian.Uint32(data[i+4:])})
	}
	return entries, nil
}
