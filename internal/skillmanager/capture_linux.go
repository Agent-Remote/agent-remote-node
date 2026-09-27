package skillmanager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// CaptureWorkTree durably hashes a complete, quiescent discovery directory without following links.
//
// The helper must first prove every managed writer exited. Capturing an active tree does not
// provide application consistency even if its metadata appears stable. This never validates
// SKILL.md format or removes content: invalid-format edits must remain exportable and recoverable.
// Runtime uploads must still revalidate each streamed object against the returned manifest.
func CaptureWorkTree(ctx context.Context, work *os.Root, baseline PermissionBaseline, options CaptureOptions) (Manifest, error) {
	return captureWorkTree(ctx, work, baseline, options, true)
}

func captureWorkTree(ctx context.Context, work *os.Root, baseline PermissionBaseline, options CaptureOptions, durable bool) (Manifest, error) {
	exclusions, dependencies, err := validateCaptureOptions(options)
	if err != nil {
		return Manifest{}, err
	}
	// Reject a corrupted baseline before touching work files, not after an expensive capture.
	if _, err := RestoreSourceModes(baseline.Materialized, baseline); err != nil {
		return Manifest{}, err
	}
	capture := treeCapture{
		durable: durable,
		options: options, exclusions: exclusions, dependencies: dependencies,
		observed: make(map[string]os.FileInfo), manifest: Manifest{Version: 1},
	}
	if err := capture.walk(ctx, work, "", 0); err != nil {
		return Manifest{}, err
	}
	if err := capture.verify(ctx, work, ""); err != nil {
		return Manifest{}, err
	}
	sort.Slice(capture.manifest.Entries, func(i, j int) bool { return capture.manifest.Entries[i].Path < capture.manifest.Entries[j].Path })
	if err := Validate(capture.manifest); err != nil {
		return Manifest{}, fmt.Errorf("portability_error: %w", err)
	}
	return RestoreSourceModes(capture.manifest, baseline)
}

func validateCaptureOptions(options CaptureOptions) (map[string]bool, map[string]string, error) {
	if len(options.RuntimeDependencies) > 64 {
		return nil, nil, errors.New("too many runtime dependencies")
	}
	if options.DirectoryBytes <= 0 || options.Entries <= 0 || options.Entries > 100_000 {
		return nil, nil, errors.New("invalid skill capture limits")
	}
	exclusions := make(map[string]bool)
	for _, path := range options.SystemPaths {
		if path != "ego-browser" && path != "agent-remote-device" || exclusions[path] {
			return nil, nil, errors.New("invalid or duplicate managed system exclusion")
		}
		exclusions[path] = true
	}
	dependencies := make(map[string]string)
	for dependency, target := range options.RuntimeDependencies {
		if !dependencyPattern.MatchString(dependency) || !strings.HasPrefix(target, "/") || ValidatePath(strings.TrimPrefix(target, "/")) != nil || dependencies[target] != "" {
			return nil, nil, errors.New("invalid or ambiguous runtime dependency mapping")
		}
		dependencies[target] = dependency
	}
	return exclusions, dependencies, nil
}

type treeCapture struct {
	durable      bool
	options      CaptureOptions
	exclusions   map[string]bool
	dependencies map[string]string
	observed     map[string]os.FileInfo
	manifest     Manifest
	bytes        int64
	buffer       []byte
}

func (c *treeCapture) walk(ctx context.Context, root *os.Root, prefix string, depth int) error {
	if depth > 128 {
		return errors.New("portability_error: skill tree exceeds supported directory depth")
	}
	before, err := root.Lstat(".")
	if err != nil {
		return err
	}
	c.observed[prefix] = before
	names, err := captureNames(root, c.options.Entries+len(c.exclusions))
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		if prefix == "" && c.exclusions[name] {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "/" + name
		}
		if err := ValidatePath(path); err != nil {
			return fmt.Errorf("portability_error: %w", err)
		}
		if prefix == "" && (name == "ego-browser" || name == "agent-remote-device") {
			return errors.New("portability_error: undeclared reserved system entry")
		}
		if len(c.manifest.Entries) >= c.options.Entries {
			return errors.New("quota_exceeded: runtime directory entry limit")
		}
		info, err := root.Lstat(name)
		if err != nil {
			return err
		}
		entry := Entry{Path: path, Mode: uint32(info.Mode().Perm())}
		switch {
		case info.Mode().IsRegular():
			entry, err = c.captureFile(ctx, root, name, entry, info)
		case info.IsDir():
			entry.Kind = "directory"
		case info.Mode()&os.ModeSymlink != 0:
			entry.Kind, entry.Mode = "symlink", 0o777
			entry.Target, err = root.Readlink(name)
			if dependency := c.dependencies[entry.Target]; dependency != "" {
				entry.Kind, entry.Dependency = "runtime_link", dependency
			}
		default:
			return errors.New("portability_error: special runtime filesystem entry")
		}
		if err != nil {
			return err
		}
		c.manifest.Entries = append(c.manifest.Entries, entry)
		if info.IsDir() {
			child, err := root.OpenRoot(name)
			if err != nil {
				return err
			}
			err = unchangedAt(child, ".", info)
			if err == nil {
				err = c.walk(ctx, child, path, depth+1)
			}
			_ = child.Close()
			if err != nil {
				return err
			}
		}
		if err := unchangedAt(root, name, info); err != nil {
			return err
		}
		c.observed[path] = info
	}
	if err := unchangedAt(root, ".", before); err != nil {
		return err
	}
	if c.durable {
		return syncDirectory(root, ".")
	}
	return nil
}

func (c *treeCapture) captureFile(ctx context.Context, root *os.Root, name string, entry Entry, before os.FileInfo) (Entry, error) {
	return c.captureFileTo(ctx, root, name, entry, before, nil)
}

func (c *treeCapture) captureFileTo(ctx context.Context, root *os.Root, name string, entry Entry, before os.FileInfo, output io.Writer) (Entry, error) {
	if before.Size() < 0 || before.Size() > c.options.DirectoryBytes-c.bytes {
		return Entry{}, errors.New("quota_exceeded: runtime directory byte limit")
	}
	file, err := root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return Entry{}, err
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil {
		return Entry{}, err
	}
	if !actual.Mode().IsRegular() || !sameSourceInfo(before, actual) {
		return Entry{}, errors.New("state_unstable: runtime file replaced before capture")
	}
	hash := sha256.New()
	classifier := textClassifier{}
	if c.buffer == nil {
		c.buffer = make([]byte, 64*1024)
	}
	buffer := c.buffer
	var count int64
	for {
		if err := ctx.Err(); err != nil {
			return Entry{}, err
		}
		read, readErr := file.Read(buffer)
		if int64(read) > before.Size()-count {
			return Entry{}, errors.New("state_unstable: runtime file grew during capture")
		}
		count += int64(read)
		_, _ = hash.Write(buffer[:read])
		classifier.update(buffer[:read])
		if output != nil && read > 0 {
			written, err := output.Write(buffer[:read])
			if err != nil {
				return Entry{}, err
			}
			if written != read {
				return Entry{}, io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return Entry{}, readErr
		}
	}
	actual, err = file.Stat()
	if err != nil {
		return Entry{}, err
	}
	if count != before.Size() || !sameSourceInfo(before, actual) {
		return Entry{}, errors.New("state_unstable: runtime file changed during capture")
	}
	if c.durable {
		if err := file.Sync(); err != nil {
			return Entry{}, err
		}
	}
	entry.Kind, entry.Size, entry.SHA256 = "file", count, hex.EncodeToString(hash.Sum(nil))
	entry.ContentKind = "binary"
	if classifier.isText() {
		entry.ContentKind = "text"
	}
	c.bytes += count
	return entry, nil
}

func (c *treeCapture) verify(ctx context.Context, root *os.Root, prefix string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := unchangedAt(root, ".", c.observed[prefix]); err != nil {
		return err
	}
	names, err := captureNames(root, c.options.Entries+len(c.exclusions))
	if err != nil {
		return err
	}
	for _, name := range names {
		if prefix == "" && c.exclusions[name] {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "/" + name
		}
		before, exists := c.observed[path]
		if !exists {
			return errors.New("state_unstable: runtime gained an entry during capture")
		}
		if err := unchangedAt(root, name, before); err != nil {
			return err
		}
		if before.IsDir() {
			child, err := root.OpenRoot(name)
			if err != nil {
				return err
			}
			err = c.verify(ctx, child, path)
			_ = child.Close()
			if err != nil {
				return err
			}
		}
	}
	return unchangedAt(root, ".", c.observed[prefix])
}

func captureNames(root *os.Root, limit int) ([]string, error) {
	file, err := root.OpenFile(".", os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	names, err := file.Readdirnames(limit + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(names) > limit {
		return nil, errors.New("quota_exceeded: runtime directory entry limit")
	}
	sort.Strings(names)
	return names, nil
}

func unchangedAt(root *os.Root, path string, before os.FileInfo) error {
	after, err := root.Lstat(path)
	if err != nil {
		return err
	}
	if before == nil || !sameSourceInfo(before, after) {
		return errors.New("state_unstable: runtime tree changed during capture")
	}
	return nil
}

func sameSourceInfo(before, after os.FileInfo) bool {
	if !os.SameFile(before, after) || before.Mode() != after.Mode() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return false
	}
	oldStat, oldOK := before.Sys().(*syscall.Stat_t)
	newStat, newOK := after.Sys().(*syscall.Stat_t)
	return oldOK && newOK && oldStat.Ctim == newStat.Ctim && oldStat.Uid == newStat.Uid && oldStat.Gid == newStat.Gid
}
