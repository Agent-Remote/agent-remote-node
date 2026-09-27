package runtimehelper

import (
	"bytes"
	"encoding/binary"
	"sort"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

const (
	migrationACLUserObject  = 1
	migrationACLUser        = 2
	migrationACLGroupObject = 4
	migrationACLGroup       = 8
	migrationACLMask        = 16
	migrationACLOther       = 32
	migrationACLUndefinedID = ^uint32(0)
)

type migrationACLEntry struct {
	tag, permissions uint16
	id               uint32
}

type migrationParentAccess struct {
	mode uint32
	acl  []byte
}

// A directory's traversal command inserts named users with --x and recalculates
// the group-class mask. It cannot explain changes to other ACL entries or mode bits.
func migrationParentAccessStates(parent skillmanager.MigrationParentPermissions, target, source, worker uint32, targetTraversal, rollbackTraversal, restorationStarted bool) ([]migrationParentAccess, error) {
	original, err := decodeMigrationAccessACL(parent.AccessACL, parent.Mode)
	if err != nil {
		return nil, err
	}
	states := []migrationParentAccess{{mode: parent.Mode, acl: parent.AccessACL}}
	var traversals [][]migrationACLEntry
	if targetTraversal {
		traversals = append(traversals, migrationTraversalACL(original, target, worker))
	}
	if rollbackTraversal {
		traversals = append(traversals, migrationTraversalACL(original, source, worker))
		if targetTraversal {
			traversals = append(traversals, migrationTraversalACL(traversals[0], source, worker))
		}
	}
	for _, entries := range traversals {
		if entries == nil {
			return nil, errMigrationInventory
		}
		mode := parent.Mode
		for _, entry := range entries {
			if entry.tag == migrationACLMask {
				mode = mode&^0070 | uint32(entry.permissions)<<3
			}
		}
		states = append(states, migrationParentAccess{mode: mode, acl: encodeMigrationACL(entries)})
		if restorationStarted {
			// Original restoration writes chmod before replacing the ACL. A crash in
			// between may retain named entries with the original group-class mask.
			masked := append([]migrationACLEntry(nil), entries...)
			for index := range masked {
				if masked[index].tag == migrationACLMask {
					masked[index].permissions = uint16(parent.Mode >> 3 & 7)
				}
			}
			states = append(states, migrationParentAccess{mode: parent.Mode, acl: encodeMigrationACL(masked)})
		}
	}
	return states, nil
}

func matchesMigrationParentAccess(parent skillmanager.MigrationParentPermissions, states []migrationParentAccess) bool {
	for _, state := range states {
		if parent.Mode == state.mode && bytes.Equal(parent.AccessACL, state.acl) {
			return true
		}
	}
	return false
}

func decodeMigrationAccessACL(data []byte, mode uint32) ([]migrationACLEntry, error) {
	if data == nil {
		return []migrationACLEntry{
			{migrationACLUserObject, uint16(mode >> 6 & 7), migrationACLUndefinedID},
			{migrationACLGroupObject, uint16(mode >> 3 & 7), migrationACLUndefinedID},
			{migrationACLOther, uint16(mode & 7), migrationACLUndefinedID},
		}, nil
	}
	if len(data) < 28 || len(data) > 64<<10 || (len(data)-4)%8 != 0 || binary.LittleEndian.Uint32(data) != 2 {
		return nil, errMigrationInventory
	}
	var entries []migrationACLEntry
	var user, group, mask, other *migrationACLEntry
	named := false
	for offset := 4; offset < len(data); offset += 8 {
		entry := migrationACLEntry{binary.LittleEndian.Uint16(data[offset:]), binary.LittleEndian.Uint16(data[offset+2:]), binary.LittleEndian.Uint32(data[offset+4:])}
		if entry.permissions > 7 || len(entries) > 0 && (entry.tag < entries[len(entries)-1].tag || entry.tag == entries[len(entries)-1].tag && entry.id <= entries[len(entries)-1].id) {
			return nil, errMigrationInventory
		}
		if entry.tag == migrationACLUser || entry.tag == migrationACLGroup {
			if entry.id == migrationACLUndefinedID {
				return nil, errMigrationInventory
			}
			named = true
		} else {
			if entry.id != migrationACLUndefinedID {
				return nil, errMigrationInventory
			}
			switch entry.tag {
			case migrationACLUserObject:
				user = &entry
			case migrationACLGroupObject:
				group = &entry
			case migrationACLMask:
				mask = &entry
			case migrationACLOther:
				other = &entry
			default:
				return nil, errMigrationInventory
			}
		}
		entries = append(entries, entry)
	}
	if user == nil || group == nil || other == nil || named && mask == nil {
		return nil, errMigrationInventory
	}
	groupClass := group
	if mask != nil {
		groupClass = mask
	}
	if uint32(user.permissions) != mode>>6&7 || uint32(groupClass.permissions) != mode>>3&7 || uint32(other.permissions) != mode&7 {
		return nil, errMigrationInventory
	}
	return entries, nil
}

func migrationTraversalACL(original []migrationACLEntry, runtime, worker uint32) []migrationACLEntry {
	if runtime == 0 || worker == 0 || runtime == migrationACLUndefinedID || worker == migrationACLUndefinedID {
		return nil
	}
	entries := append([]migrationACLEntry(nil), original...)
	for _, uid := range []uint32{runtime, worker} {
		found := false
		for index := range entries {
			if entries[index].tag == migrationACLUser && entries[index].id == uid {
				entries[index].permissions, found = 1, true
			}
		}
		if !found {
			entries = append(entries, migrationACLEntry{migrationACLUser, 1, uid})
		}
	}
	var permissions uint16
	mask := -1
	for index, entry := range entries {
		if entry.tag == migrationACLMask {
			mask = index
		}
		if entry.tag == migrationACLUser || entry.tag == migrationACLGroup || entry.tag == migrationACLGroupObject {
			permissions |= entry.permissions
		}
	}
	if mask == -1 {
		entries = append(entries, migrationACLEntry{migrationACLMask, permissions, migrationACLUndefinedID})
	} else {
		entries[mask].permissions = permissions
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].tag < entries[j].tag || entries[i].tag == entries[j].tag && entries[i].id < entries[j].id
	})
	return entries
}

func encodeMigrationACL(entries []migrationACLEntry) []byte {
	data := make([]byte, 4+8*len(entries))
	binary.LittleEndian.PutUint32(data, 2)
	for index, entry := range entries {
		offset := 4 + index*8
		binary.LittleEndian.PutUint16(data[offset:], entry.tag)
		binary.LittleEndian.PutUint16(data[offset+2:], entry.permissions)
		binary.LittleEndian.PutUint32(data[offset+4:], entry.id)
	}
	return data
}
