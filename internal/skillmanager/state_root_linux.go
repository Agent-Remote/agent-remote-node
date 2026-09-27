package skillmanager

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// OpenStateStore creates or opens a root-owned private store without traversing any symlink.
// Non-root-owned or non-sticky writable ancestors are rejected before any descendant is created.
func OpenStateStore(path string, occupied ...string) (*os.Root, error) {
	return openStateStore(path, true, occupied...)
}

// OpenExistingStateStore verifies an existing private store without creating missing ancestors.
func OpenExistingStateStore(path string, occupied ...string) (*os.Root, error) {
	return openStateStore(path, false, occupied...)
}

func openStateStore(path string, create bool, occupied ...string) (*os.Root, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("skill state store requires the privileged Linux helper")
	}
	if err := ValidateStateRoot(path, occupied...); err != nil {
		return nil, err
	}
	for _, other := range occupied {
		if other == "" {
			continue
		}
		resolved, err := resolveExistingAncestors(other)
		if err != nil {
			return nil, err
		}
		if err := ValidateStateRoot(path, resolved); err != nil {
			return nil, err
		}
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = unix.Close(fd) }()
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for index, part := range parts {
		if err := validateStoreAncestor(fd); err != nil {
			return nil, err
		}
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if create && errors.Is(err, unix.ENOENT) {
			if err := unix.Mkdirat(fd, part, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
				return nil, err
			}
			if err := unix.Fsync(fd); err != nil {
				return nil, err
			}
			next, err = unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		}
		if err != nil {
			if !create && errors.Is(err, unix.ENOENT) {
				return nil, ErrStateStoreAbsent
			}
			return nil, errors.New("skill state path contains an inaccessible or linked directory")
		}
		_ = unix.Close(fd)
		fd = next
		if index == len(parts)-1 {
			var stat unix.Stat_t
			if err := unix.Fstat(fd, &stat); err != nil {
				return nil, err
			}
			if stat.Uid != 0 || stat.Mode&0o7777 != 0o700 {
				return nil, errors.New("skill state store must be root-owned mode 0700")
			}
		}
	}
	// /proc/self/fd refers to this already-validated descriptor, not an attacker-supplied path.
	return os.OpenRoot(fmt.Sprintf("/proc/self/fd/%d", fd))
}

func validateStoreAncestor(fd int) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	if stat.Uid != 0 || stat.Mode&0o022 != 0 && stat.Mode&unix.S_ISVTX == 0 {
		return errors.New("skill state ancestor must be root-owned and not writable by other identities")
	}
	return nil
}

func resolveExistingAncestors(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		return resolved, nil
	}
	if !errors.Is(err, os.ErrNotExist) || filepath.Dir(absolute) == absolute {
		return "", err
	}
	parent, err := resolveExistingAncestors(filepath.Dir(absolute))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(absolute)), nil
}
