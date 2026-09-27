package skillmanager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func copyObject(ctx context.Context, root *os.Root, entry Entry, open ObjectOpener, options MaterializeOptions, buffer []byte) error {
	source, err := open(ctx, entry.SHA256)
	if err != nil {
		return err
	}
	if source == nil {
		return errors.New("authorized skill object opener returned no reader")
	}
	defer source.Close()
	target, err := root.OpenFile(entry.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	defer target.Close()
	hash := sha256.New()
	classifier := textClassifier{}
	var count int64
	stalls := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		read, readErr := source.Read(buffer)
		if read < 0 || read > len(buffer) {
			return errors.New("invalid skill content reader result")
		}
		if int64(read) > entry.Size-count {
			return errors.New("skill object exceeds declared byte count")
		}
		if read > 0 {
			stalls = 0
			count += int64(read)
			content := buffer[:read]
			_, _ = hash.Write(content)
			classifier.update(content)
			if _, err := target.Write(content); err != nil {
				return err
			}
		} else {
			stalls++
			if stalls > 100 {
				return io.ErrNoProgress
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if count != entry.Size || hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
		return errors.New("skill content digest or size mismatch")
	}
	if (entry.ContentKind == "text") != classifier.isText() {
		return errors.New("skill content classification mismatch")
	}
	if err := target.Chown(options.UID, options.GID); err != nil {
		return err
	}
	if err := target.Chmod(os.FileMode(entry.Mode)); err != nil {
		return err
	}
	if err := verifyMaterializedOwnership(target, os.FileMode(entry.Mode), options); err != nil {
		return err
	}
	return target.Sync()
}
