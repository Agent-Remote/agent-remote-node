package runtimehelper

import (
	"crypto/sha256"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func (s *migrationInventoryScanner) xattrs(fd, parent int, name string, symlink bool) (migrationAttributeDigests, error) {
	return s.readXattrs(fd, parent, name, symlink, nil)
}

// An optional original access ACL projects parent attributes back to the saved
// baseline without changing the filesystem; unrelated attributes still participate.
func (s *migrationInventoryScanner) readXattrs(fd, parent int, name string, symlink bool, originalAccessACL *[]byte) (migrationAttributeDigests, error) {
	var result migrationAttributeDigests
	list := func(buffer []byte) (int, error) { return unix.Flistxattr(fd, buffer) }
	get := func(key string, buffer []byte) (int, error) { return unix.Fgetxattr(fd, key, buffer) }
	if symlink {
		// Only the owned directory descriptor is followed. The final single-component
		// name is inspected with l* calls and its inode/ctime is rechecked by the caller.
		path := "/proc/self/fd/" + strconv.Itoa(parent) + "/" + name
		list = func(buffer []byte) (int, error) { return unix.Llistxattr(path, buffer) }
		get = func(key string, buffer []byte) (int, error) { return unix.Lgetxattr(path, key, buffer) }
	}
	buffer := s.xattrBuffer
	n, err := list(buffer)
	if err != nil || s.ctx.Err() != nil {
		return result, errMigrationInventory
	}
	var keys []string
	if n > 0 {
		if buffer[n-1] != 0 {
			return result, errMigrationInventory
		}
		keys = strings.Split(string(buffer[:n-1]), "\x00")
	}
	if originalAccessACL != nil {
		filtered := make([]string, 0, len(keys)+1)
		for _, key := range keys {
			if key != "system.posix_acl_access" {
				filtered = append(filtered, key)
			}
		}
		if *originalAccessACL != nil {
			filtered = append(filtered, "system.posix_acl_access")
		}
		keys = filtered
	}
	sort.Strings(keys)
	h := sha256.New()
	content := sha256.New()
	entryBytes := n
	for index, key := range keys {
		if key == "" || index > 0 && keys[index-1] == key || s.ctx.Err() != nil {
			return result, errMigrationInventory
		}
		var value []byte
		if originalAccessACL != nil && key == "system.posix_acl_access" {
			value = *originalAccessACL
		} else {
			count, err := get(key, buffer)
			if err != nil {
				return result, errMigrationInventory
			}
			value = buffer[:count]
		}
		entryBytes += len(value)
		if entryBytes > 1<<20 {
			return result, errMigrationInventory
		}
		migrationInventoryField(h, key)
		migrationInventoryField(h, string(value))
		// chown/setfacl can change these permission attributes. All other xattrs
		// must survive the backup and therefore also participate in content equality.
		if key != "system.posix_acl_access" && key != "system.posix_acl_default" && key != "security.capability" {
			migrationInventoryField(content, key)
			migrationInventoryField(content, string(value))
		}
	}
	s.xattrBytes += entryBytes
	if s.xattrBytes > migrationInventoryXattrs {
		return result, errMigrationInventory
	}
	copy(result.all[:], h.Sum(nil))
	copy(result.content[:], content.Sum(nil))
	return result, nil
}

type migrationAttributeDigests struct {
	all, content [sha256.Size]byte
}
