package skillmanager

import (
	"context"
	"errors"
	"io"
	"os"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

func (r *reclamationRoot) walk(ctx context.Context, directory *os.File, prefix string, remove bool, verify func(context.Context) error) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.Count(prefix, "/") > 128 {
		return errors.New("reclamation tree exceeds supported depth")
	}
	if remove {
		defer func() { err = errors.Join(err, directory.Sync()) }()
		if err := verify(ctx); err != nil {
			return err
		}
	}
	// A new file description avoids sharing a consumed directory offset between the two passes.
	fd, err := unix.Openat(int(directory.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	listing := os.NewFile(uintptr(fd), "reclamation-list")
	names, err := listing.Readdirnames(len(r.entries) + 1)
	_ = listing.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(names) > len(r.entries) {
		return errors.New("reclamation found unrecorded entries")
	}
	sort.Strings(names)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := name
		if prefix != "" {
			path = prefix + "/" + name
		}
		value, known := r.entries[path]
		if !known {
			return errors.New("reclamation preserves an unrecorded entry")
		}
		var before unix.Stat_t
		if err := unix.Fstatat(int(directory.Fd()), name, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
		if err := r.checkEntry(int(directory.Fd()), name, before); err != nil {
			return err
		}
		if err := value.verifyMode(before); err != nil {
			return err
		}
		flags := 0
		switch value.entry.Kind {
		case "directory":
			if err := r.walkChild(ctx, directory, name, path, before, remove, verify); err != nil {
				return err
			}
			flags = unix.AT_REMOVEDIR
			// Our own child unlinks legitimately update directory timestamps and link count.
			var after unix.Stat_t
			if err := unix.Fstatat(int(directory.Fd()), name, &after, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				return err
			}
			if after.Dev != before.Dev || after.Ino != before.Ino || after.Mode != before.Mode || after.Uid != before.Uid || after.Gid != before.Gid {
				return errors.New("reclamation directory changed during traversal")
			}
			before = after
		case "file":
			if err := r.verifyFile(ctx, directory, name, before, value.entry); err != nil {
				return err
			}
		case "symlink", "runtime_link":
			buffer := make([]byte, len(value.entry.Target)+1)
			n, err := unix.Readlinkat(int(directory.Fd()), name, buffer)
			if err != nil || string(buffer[:n]) != value.entry.Target {
				return errors.New("reclamation link target changed")
			}
		default:
			return errors.New("reclamation entry type is unsupported")
		}
		if remove {
			if err := verify(ctx); err != nil {
				return err
			}
			if err := r.checkOriginal(); err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.checkEntry(int(directory.Fd()), name, before); err != nil {
			return err
		}
		if remove {
			if err := unix.Unlinkat(int(directory.Fd()), name, flags); err != nil {
				return err
			}
		}
	}
	return nil
}

func (value reclamationEntry) verifyMode(stat unix.Stat_t) error {
	kind := uint32(unix.S_IFREG)
	switch value.entry.Kind {
	case "directory":
		kind = unix.S_IFDIR
	case "symlink", "runtime_link":
		kind = unix.S_IFLNK
	}
	if stat.Mode&unix.S_IFMT != kind {
		return errors.New("reclamation entry type changed")
	}
	mode := stat.Mode & 0777
	if !value.ignoreMode && mode != value.entry.Mode && (!value.allowMode || mode != value.otherMode) {
		return errors.New("reclamation entry permissions changed")
	}
	return nil
}

func (r *reclamationRoot) walkChild(ctx context.Context, parent *os.File, name, path string, before unix.Stat_t, remove bool, verify func(context.Context) error) error {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	child := os.NewFile(uintptr(fd), name)
	defer child.Close()
	var actual unix.Stat_t
	if err := unix.Fstat(fd, &actual); err != nil {
		return err
	}
	if !sameReclamationStat(actual, before) {
		return errors.New("reclamation directory replaced before open")
	}
	return r.walk(ctx, child, path, remove, verify)
}

func (r *reclamationRoot) verifyFile(ctx context.Context, parent *os.File, name string, before unix.Stat_t, entry Entry) error {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	if r.private {
		if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
			return errors.New("finalization object has an active reader")
		}
	}
	var actual unix.Stat_t
	if err := unix.Fstat(fd, &actual); err != nil {
		return err
	}
	if !sameReclamationStat(before, actual) || actual.Size != entry.Size ||
		r.private && (actual.Uid != uint32(os.Geteuid()) || actual.Mode&07777 != 0600 || actual.Nlink != 1) {
		return errors.New("reclamation file changed or lost private ownership")
	}
	verifier, err := NewContentVerifier(entry)
	if err != nil {
		return err
	}
	buffer := make([]byte, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := file.Read(buffer)
		if _, err := verifier.Write(buffer[:n]); err != nil {
			return err
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if err := verifier.Finish(); err != nil {
		return err
	}
	if err := unix.Fstat(fd, &actual); err != nil {
		return err
	}
	if !sameReclamationStat(before, actual) {
		return errors.New("reclamation file changed during hashing")
	}
	return nil
}
