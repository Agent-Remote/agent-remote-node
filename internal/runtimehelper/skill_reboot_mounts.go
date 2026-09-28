package runtimehelper

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type rebootMount struct {
	device string
	root   string
	point  string
}

func requireNoRebootMounts(ctx context.Context, paths ...string) error {
	for _, path := range paths {
		if err := requireUnaliasedRebootPath(path); err != nil {
			return err
		}
	}
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	defer file.Close()
	return verifyNoRebootMounts(ctx, file, paths...)
}

func requireUnaliasedRebootPath(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return errors.New("invalid previous-boot resource path")
	}
	current := "/"
	for _, component := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("previous-boot resource path is aliased or not a directory")
		}
	}
	return nil
}

// A changed boot excludes old processes, but not mounts recreated by another privileged actor.
// Check both visible targets and bind aliases of their filesystem subtrees in the Helper namespace.
func verifyNoRebootMounts(ctx context.Context, reader io.Reader, paths ...string) error {
	mounts, err := readRebootMounts(ctx, reader)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
			return errors.New("invalid previous-boot resource path")
		}
		var containing *rebootMount
		for i := range mounts {
			mount := &mounts[i]
			if mountPathWithin(mount.point, path) {
				return errors.New("previous-boot resource has a current mount")
			}
			if mountPathWithin(path, mount.point) && (containing == nil || len(mount.point) > len(containing.point)) {
				containing = mount
			}
		}
		if containing == nil || !filepath.IsAbs(containing.root) {
			return errors.New("previous-boot resource filesystem cannot be located")
		}
		relative, err := filepath.Rel(containing.point, path)
		if err != nil {
			return err
		}
		root := filepath.Join(containing.root, relative)
		for _, mount := range mounts {
			if mount.device == containing.device && !mountPathWithin(path, mount.point) &&
				(mountPathWithin(mount.root, root) || mountPathWithin(root, mount.root)) {
				return errors.New("previous-boot resource has a current filesystem alias")
			}
		}
	}
	return ctx.Err()
}

func mountPathWithin(path, root string) bool {
	return path == root || strings.HasPrefix(path, strings.TrimSuffix(root, "/")+"/")
}

func readRebootMounts(ctx context.Context, reader io.Reader) ([]rebootMount, error) {
	bounded := &io.LimitedReader{R: reader, N: 8<<20 + 1}
	scanner := bufio.NewScanner(bounded)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	seen := make(map[uint64]bool)
	var mounts []rebootMount
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 || len(mounts) >= 65536 {
			return nil, errors.New("previous-boot mount inventory is malformed or excessive")
		}
		separator := 6
		for separator < len(fields) && fields[separator] != "-" {
			separator++
		}
		if separator+4 != len(fields) {
			return nil, errors.New("previous-boot mount inventory separator is invalid")
		}
		id, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil || id == 0 || seen[id] {
			return nil, errors.New("previous-boot mount identity is invalid or duplicate")
		}
		seen[id] = true
		if _, err := strconv.ParseUint(fields[1], 10, 64); err != nil {
			return nil, errors.New("previous-boot mount parent is invalid")
		}
		major, minor, ok := strings.Cut(fields[2], ":")
		_, majorErr := strconv.ParseUint(major, 10, 32)
		_, minorErr := strconv.ParseUint(minor, 10, 32)
		if !ok || majorErr != nil || minorErr != nil {
			return nil, errors.New("previous-boot mount device is invalid")
		}
		root, err := unescapeMountRoot(fields[3], fields[separator+1])
		if err != nil {
			return nil, err
		}
		point, err := unescapeMountPath(fields[4])
		if err != nil {
			return nil, err
		}
		mounts = append(mounts, rebootMount{device: fields[2], root: root, point: point})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(mounts) == 0 || bounded.N == 0 {
		return nil, errors.New("previous-boot mount inventory is empty or truncated")
	}
	return mounts, ctx.Err()
}

func unescapeMountPath(value string) (string, error) {
	var result strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			result.WriteByte(value[i])
			continue
		}
		if i+3 >= len(value) {
			return "", errors.New("truncated kernel mount path escape")
		}
		switch value[i+1 : i+4] {
		case "040":
			result.WriteByte(' ')
		case "011":
			result.WriteByte('\t')
		case "012":
			result.WriteByte('\n')
		case "134":
			result.WriteByte('\\')
		default:
			return "", errors.New("invalid kernel mount path escape")
		}
		i += 3
	}
	path := result.String()
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, '\x00') {
		return "", errors.New("kernel mount path is not canonical")
	}
	return path, nil
}

// Namespace filesystems identify their roots by kernel inode, not by an absolute path.
// Keep their mountpoints in the inventory so a namespace mounted onto protected content still blocks deletion.
func unescapeMountRoot(value, filesystem string) (string, error) {
	if filesystem == "nsfs" {
		kind, inode, ok := strings.Cut(value, ":[")
		switch kind {
		case "net", "mnt", "uts", "ipc", "pid", "pid_for_children", "user", "cgroup", "time", "time_for_children":
			if ok && strings.HasSuffix(inode, "]") {
				number := strings.TrimSuffix(inode, "]")
				parsed, err := strconv.ParseUint(number, 10, 64)
				if err == nil && parsed > 0 && strconv.FormatUint(parsed, 10) == number {
					return value, nil
				}
			}
		}
	}
	return unescapeMountPath(value)
}
