//go:build linux || darwin

package claudeattachments

import (
	"errors"
	"fmt"
	"regexp"

	"golang.org/x/sys/unix"
)

var sessionName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

// Prepare creates the directory before Claude validates --add-dir. The caller
// supplies a validated managed account path. Descendants are opened relative to
// directory descriptors so account-controlled symlinks cannot redirect root.
// Newly created directories inherit the account's default runtime/sync ACLs.
func Prepare(accountPath, sessionID string, uid, gid int) error {
	if !sessionName.MatchString(sessionID) {
		return errors.New("invalid attachment session identity")
	}
	const flags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	current, err := unix.Open(accountPath, flags, 0)
	if err != nil {
		return fmt.Errorf("open attachment account: %w", err)
	}
	defer func() { _ = unix.Close(current) }()
	for _, part := range []string{".agent-remote-attachments", sessionID} {
		err := unix.Mkdirat(current, part, 0o770)
		created := err == nil
		if err != nil && !errors.Is(err, unix.EEXIST) {
			return fmt.Errorf("create attachment directory: %w", err)
		}
		next, err := unix.Openat(current, part, flags, 0)
		if err != nil {
			return fmt.Errorf("open attachment directory without following links: %w", err)
		}
		_ = unix.Close(current)
		current = next
		if created && uid > 0 && gid > 0 {
			if err := unix.Fchown(current, uid, gid); err != nil {
				return fmt.Errorf("own attachment directory: %w", err)
			}
		}
	}
	return nil
}
