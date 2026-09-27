//go:build linux || darwin

package toolaccounts

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func writeImportBatch(root string, files []preparedImportFile, owner *ImportOwnership) error {
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("open account import root: %w", err)
	}
	anchor, err := unix.Open(canonical, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(anchor)
	paths := make([]string, len(files))
	for index, file := range files {
		relative, err := filepath.Rel(root, file.path)
		if err != nil || !filepath.IsLocal(relative) {
			return errors.New("config import target is outside account root")
		}
		paths[index] = relative
		parent, err := openImportParent(anchor, relative, false, owner)
		if errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil {
			return err
		}
		err = validateImportLeaf(parent, filepath.Base(relative))
		unix.Close(parent)
		if err != nil {
			return err
		}
	}
	for index, file := range files {
		parent, err := openImportParent(anchor, paths[index], true, owner)
		if err != nil {
			return err
		}
		err = writeImportFile(parent, filepath.Base(paths[index]), file, owner)
		unix.Close(parent)
		if err != nil {
			return err
		}
	}
	return nil
}

func openImportParent(anchor int, relative string, create bool, owner *ImportOwnership) (int, error) {
	current, err := unix.Openat(anchor, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	for _, component := range strings.Split(filepath.Dir(relative), string(filepath.Separator)) {
		if component == "." {
			continue
		}
		next, err := unix.Openat(current, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		created := false
		if create && errors.Is(err, unix.ENOENT) {
			mkdirErr := unix.Mkdirat(current, component, 0o700)
			created = mkdirErr == nil
			if mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				unix.Close(current)
				return -1, mkdirErr
			}
			if syncErr := unix.Fsync(current); syncErr != nil {
				unix.Close(current)
				return -1, syncErr
			}
			next, err = unix.Openat(current, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		}
		unix.Close(current)
		if err != nil {
			return -1, fmt.Errorf("unsafe config import directory: %w", err)
		}
		if created && owner != nil {
			if err := unix.Fchown(next, owner.UID, owner.GID); err != nil {
				unix.Close(next)
				return -1, err
			}
			if err := unix.Fsync(next); err != nil {
				unix.Close(next)
				return -1, err
			}
		}
		current = next
	}
	return current, nil
}

func validateImportLeaf(parent int, name string) error {
	var stat unix.Stat_t
	err := unix.Fstatat(parent, name, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return errors.New("config import target must be a regular file, not a link or special file")
	}
	return nil
}

func writeImportFile(parent int, name string, file preparedImportFile, owner *ImportOwnership) error {
	if err := validateImportLeaf(parent, name); err != nil {
		return err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	temporary := ".agent-remote-import-" + hex.EncodeToString(nonce[:])
	fd, err := unix.Openat(parent, temporary, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}
	defer unix.Unlinkat(parent, temporary, 0)
	output := os.NewFile(uintptr(fd), temporary)
	if output == nil {
		unix.Close(fd)
		return errors.New("config import file descriptor is invalid")
	}
	defer output.Close()
	written, err := output.Write(file.content)
	if err != nil {
		return err
	}
	if written != len(file.content) {
		return io.ErrShortWrite
	}
	if owner != nil {
		if err := output.Chown(owner.UID, owner.GID); err != nil {
			return err
		}
	}
	if err := output.Chmod(file.mode); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	if err := unix.Renameat(parent, temporary, parent, name); err != nil {
		return err
	}
	return unix.Fsync(parent)
}
