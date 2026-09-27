//go:build linux || darwin

package runtimehelper

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func (e Engine) checkLegacyAccountPath(userID, accountID string) error {
	root, err := filepath.EvalSymlinks(e.config.AccountRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("runtime account root cannot be inspected")
	}
	current, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return errors.New("runtime account root cannot be opened")
	}
	for _, part := range []string{userID, "tool-accounts", "claude", accountID, ".claude", "skills"} {
		next, err := unix.Openat(current, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(current)
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
		if err != nil {
			return errors.New("unsafe runtime account directory alias")
		}
		current = next
	}
	return unix.Close(current)
}
