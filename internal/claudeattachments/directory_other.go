//go:build !linux && !darwin

package claudeattachments

import "errors"

// Prepare rejects unsupported runtime hosts; clients may use any platform.
func Prepare(accountPath, sessionID string, uid, gid int) error {
	return errors.New("safe attachment directory preparation is unsupported on this host")
}
