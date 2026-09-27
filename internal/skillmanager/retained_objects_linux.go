package skillmanager

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"strings"
)

func copyRetainedObjects(ctx context.Context, stage, source *os.Root, manifest Manifest) error {
	if err := stage.Mkdir("objects", 0o700); err != nil {
		return err
	}
	objects, err := stage.OpenRoot("objects")
	if err != nil {
		return err
	}
	defer objects.Close()
	seen := make(map[string]bool)
	buffer := make([]byte, 64*1024)
	for _, entry := range manifest.Entries {
		if entry.Kind != "file" || seen[entry.SHA256] {
			continue
		}
		path := entry.Path
		open := func(ctx context.Context, digest string) (io.ReadCloser, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return openRetainedSourceFile(source, path)
		}
		entry.Path, entry.Mode = entry.SHA256, 0o600
		if err := copyObject(ctx, objects, entry, open, MaterializeOptions{UID: os.Geteuid(), GID: os.Getegid()}, buffer); err != nil {
			return err
		}
		seen[entry.SHA256] = true
	}
	return syncDirectory(objects, ".")
}

func openRetainedSourceFile(source *os.Root, path string) (*os.File, error) {
	if source == nil || ValidatePath(path) != nil {
		return nil, errors.New("invalid captured source path")
	}
	anchor, err := source.Open(".")
	if err != nil {
		return nil, err
	}
	current := anchor
	parts := strings.Split(path, "/")
	for index, part := range parts {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
		if index < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		fd, err := unix.Openat(int(current.Fd()), part, flags, 0)
		_ = current.Close()
		if err != nil {
			return nil, err
		}
		current = os.NewFile(uintptr(fd), part)
	}
	info, err := current.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = current.Close()
		return nil, errors.New("captured source is not an ordinary file")
	}
	return current, nil
}
