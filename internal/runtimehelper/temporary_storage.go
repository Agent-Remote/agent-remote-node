package runtimehelper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// The image remains root-only outside the runtime mounts. Its filesystem size
// bounds temporary writes without charging toolchain archives to tmpfs memory.
const temporaryImageName = "temporary.ext4"

func (e Engine) setupDiskTemp(parent context.Context, spec SessionSpec) (result error) {
	if spec.Policy.TemporarySizeBytes < 64<<20 || spec.Policy.TemporarySizeBytes > 16<<30 {
		return errors.New("disk temporary space is outside local limits")
	}
	if !diskAvailableAt(spec.SessionRoot, 2<<30) {
		return errors.New("insufficient host disk space for session temporary storage")
	}
	tempPath := filepath.Join(spec.SessionRoot, "tmp")
	if err := ensureRootDirectory(tempPath, 0o700); err != nil {
		return err
	}
	if commandSucceeds(e.config.MountpointPath, "--quiet", tempPath) {
		return errors.New("session temporary storage is already mounted")
	}
	imagePath := filepath.Join(spec.SessionRoot, temporaryImageName)
	image, err := os.OpenFile(imagePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create private temporary image: %w", err)
	}
	mounted := false
	defer func() {
		if result == nil {
			return
		}
		// Cancellation must not leave a failed launch mounted, but a failed
		// unmount must preserve the backing file for subsequent cleanup.
		if mounted {
			cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := e.cleanupTemp(cleanup, spec); err != nil {
				result = errors.Join(result, err)
				return
			}
		}
		result = errors.Join(result, os.Remove(imagePath))
	}()
	err = image.Truncate(spec.Policy.TemporarySizeBytes)
	err = errors.Join(err, image.Close())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	if err := runCommand(ctx, e.config.MkfsExt4Path, "-q", "-F", "-m", "0", "-E", "lazy_itable_init=0,lazy_journal_init=0", imagePath); err != nil {
		return fmt.Errorf("format bounded temporary filesystem: %w", err)
	}
	if err := runCommand(ctx, e.config.MountPath, "-t", "ext4", "-o", "loop,nosuid,nodev", imagePath, tempPath); err != nil {
		// mount may have completed immediately before its context was cancelled.
		mounted = commandSucceeds(e.config.MountpointPath, "--quiet", tempPath)
		return fmt.Errorf("mount bounded temporary filesystem: %w", err)
	}
	mounted = true
	if err := os.Chown(tempPath, spec.RuntimeUID, spec.RuntimeGID); err != nil {
		return err
	}
	return os.Chmod(tempPath, 0o700)
}
