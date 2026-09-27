package runtimehelper

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"io"
	"os"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	migrationInventoryEntries = 1_000_000
	migrationInventoryDepth   = 128
	migrationInventoryPath    = 4096
	migrationInventoryXattrs  = 64 << 20
)

var errMigrationInventory = errors.New("account migration inventory is unavailable or changed")

// Inventories stay private. Only the versioned baseline persists historical content/permission
// digests; live observations and inode indexes never enter task results or logs.
type migrationInventory struct {
	content, permissions, observation [sha256.Size]byte
	root                              migrationInode
}

type migrationInode struct {
	deviceMajor, deviceMinor uint32
	inode                    uint64
}
type migrationHardlink struct {
	first          string
	expected, seen uint32
}

type migrationInventoryScanner struct {
	ctx                               context.Context
	content, permissions, observation hash.Hash
	mount                             uint64
	entries, pathBytes, xattrBytes    int
	links                             map[migrationInode]*migrationHardlink
	buffer                            []byte
	xattrBuffer                       []byte
}

// scanMigrationInventory requires caller-owned exclusion of account writers. It neither
// follows symbolic links nor opens devices, and rejects hard links outside this tree.
func scanMigrationInventory(ctx context.Context, path string) (migrationInventory, error) {
	result, _, err := scanMigrationInventoryWithLinks(ctx, path)
	return result, err
}

func scanMigrationInventoryWithLinks(ctx context.Context, path string) (migrationInventory, map[migrationInode]*migrationHardlink, error) {
	var result migrationInventory
	if ctx.Err() != nil || !strings.HasPrefix(path, "/") {
		return result, nil, errMigrationInventory
	}
	fd, err := unix.Openat2(unix.AT_FDCWD, path, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW,
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil {
		return result, nil, errMigrationInventory
	}
	root := os.NewFile(uintptr(fd), "migration inventory")
	defer root.Close()
	before, err := migrationInventoryStat(fd)
	if err != nil {
		return result, nil, errMigrationInventory
	}
	s := migrationInventoryScanner{ctx: ctx, content: sha256.New(), permissions: sha256.New(), observation: sha256.New(), mount: before.Mnt_id,
		links: make(map[migrationInode]*migrationHardlink), buffer: make([]byte, 128<<10), xattrBuffer: make([]byte, 64<<10)}
	if err := s.entry(root, fd, ".", ".", before, 0); err != nil {
		return result, nil, errMigrationInventory
	}
	for _, link := range s.links {
		if link.seen != link.expected {
			return result, nil, errMigrationInventory
		}
	}
	// Reopen through the original no-link path to reject root/ancestor substitution.
	check, err := unix.Openat2(unix.AT_FDCWD, path, &unix.OpenHow{Flags: unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS})
	if err != nil {
		return result, nil, errMigrationInventory
	}
	defer unix.Close(check)
	after, err := migrationInventoryStat(check)
	if err != nil || after != before || ctx.Err() != nil {
		return result, nil, errMigrationInventory
	}
	copy(result.content[:], s.content.Sum(nil))
	copy(result.permissions[:], s.permissions.Sum(nil))
	copy(result.observation[:], s.observation.Sum(nil))
	result.root = migrationInode{before.Dev_major, before.Dev_minor, before.Ino}
	return result, s.links, nil
}

func migrationInventoryStat(fd int) (unix.Statx_t, error) {
	var stat unix.Statx_t
	err := unix.Statx(fd, "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BASIC_STATS|unix.STATX_MNT_ID, &stat)
	if err != nil || stat.Mask&(unix.STATX_BASIC_STATS|unix.STATX_MNT_ID) != unix.STATX_BASIC_STATS|unix.STATX_MNT_ID {
		return stat, errMigrationInventory
	}
	// Reading may update atime. Every content/permission-relevant field remains checked.
	stat.Atime = unix.StatxTimestamp{}
	return stat, nil
}

func migrationInventoryField(h hash.Hash, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = h.Write(size[:])
	_, _ = io.WriteString(h, value)
}

func (s *migrationInventoryScanner) directory(file *os.File, path string, depth int) error {
	var names []string
	for {
		if s.ctx.Err() != nil {
			return errMigrationInventory
		}
		batch, err := file.Readdirnames(256)
		for _, name := range batch {
			s.entries++
			s.pathBytes += len(path) + 1 + len(name)
			if s.entries > migrationInventoryEntries || s.pathBytes > 64<<20 || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") {
				return errMigrationInventory
			}
			names = append(names, name)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return errMigrationInventory
		}
	}
	sort.Strings(names)
	for _, name := range names {
		childPath := name
		if path != "." {
			childPath = path + "/" + name
		}
		if depth >= migrationInventoryDepth || len(childPath) > migrationInventoryPath || s.ctx.Err() != nil {
			return errMigrationInventory
		}
		if err := s.child(int(file.Fd()), name, childPath, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func compareMigrationInventory(ctx context.Context, account, backup string) ([2]migrationInventory, error) {
	var pair [2]migrationInventory
	for index, path := range []string{account, backup} {
		inventory, err := scanMigrationInventory(ctx, path)
		if err != nil {
			return pair, errMigrationWritersUnknown
		}
		pair[index] = inventory
	}
	// A bind alias of the account is not an independent retained backup, even
	// when it has a different kernel mount ID and every byte presently matches.
	if pair[0].root == pair[1].root || pair[0].content != pair[1].content {
		return pair, errMigrationWritersUnknown
	}
	return pair, nil
}
