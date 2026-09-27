package skillmanager

import (
	"context"
	"errors"
	"os"
	"strings"
)

func recoveryMetadata(root *os.Root, name, path string, info os.FileInfo, dependencies map[string]string) (Entry, error) {
	entry := Entry{Path: path, Mode: uint32(info.Mode().Perm())}
	switch {
	case info.Mode().IsRegular():
		entry.Kind = "file"
	case info.IsDir():
		entry.Kind = "directory"
	case info.Mode()&os.ModeSymlink != 0:
		entry.Kind, entry.Mode = "symlink", 0o777
		var err error
		entry.Target, err = root.Readlink(name)
		if err != nil {
			return Entry{}, err
		}
		if dependency := dependencies[entry.Target]; dependency != "" {
			entry.Kind, entry.Dependency = "runtime_link", dependency
		}
		if err := validateEntry(entry); err != nil {
			return Entry{}, err
		}
	default:
		return Entry{}, errors.New("portability_error: special recovery filesystem entry")
	}
	return entry, nil
}

// Resolve only link metadata. Absolute runtime dependencies never become readable source paths.
func (s *recoveryScan) lookup(ctx context.Context, path string) (Entry, error) {
	if err := ValidatePath(path); err != nil {
		return Entry{}, err
	}
	parts := strings.Split(path, "/")
	if s.capture.exclusions[parts[0]] || parts[0] == "ego-browser" || parts[0] == "agent-remote-device" {
		return Entry{}, errors.New("recovery link targets an excluded system tree")
	}
	root, err := s.work.OpenRoot(".")
	if err != nil {
		return Entry{}, err
	}
	defer func() { _ = root.Close() }()
	for index, part := range parts {
		if err := ctx.Err(); err != nil {
			return Entry{}, err
		}
		before, err := root.Lstat(part)
		if err != nil {
			return Entry{}, err
		}
		if index == len(parts)-1 {
			entry, err := recoveryMetadata(root, part, path, before, s.capture.dependencies)
			if err == nil {
				err = unchangedAt(root, part, before)
			}
			return entry, err
		}
		if !before.IsDir() {
			return Entry{}, errors.New("recovery link traverses a non-directory entry")
		}
		child, err := root.OpenRoot(part)
		if err != nil {
			return Entry{}, err
		}
		err = unchangedAt(child, ".", before)
		_ = root.Close()
		root = child
		if err != nil {
			return Entry{}, err
		}
	}
	return Entry{}, errors.New("recovery link has no target")
}
