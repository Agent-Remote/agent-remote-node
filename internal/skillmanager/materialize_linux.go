package skillmanager

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// ObjectOpener opens already-authorized content by digest, never an arbitrary host path.
type ObjectOpener func(context.Context, string) (io.ReadCloser, error)

// CopyPolicy bounds expanded runtime trees independently from installation package limits.
type CopyPolicy struct {
	DirectoryBytes   int64
	Entries          int
	MinimumFreeBytes uint64
	ReservePercent   uint64
}

// DefaultCopyPolicy returns the published initial runtime-copy limits.
func DefaultCopyPolicy() CopyPolicy {
	return CopyPolicy{DirectoryBytes: 10 << 30, Entries: 100_000, MinimumFreeBytes: 2 << 30, ReservePercent: 5}
}

// MaterializeOptions contains helper-selected identity, quotas, and validated adapter dependencies.
type MaterializeOptions struct {
	UID                 int
	GID                 int
	Policy              CopyPolicy
	RuntimeDependencies map[string]string
}

// Materialize prepares and atomically publishes one private bundle beneath a trusted store root.
//
// The helper must authorize the manifest and content opener, serialize preparation with its disk
// reservations, and mount only bundle/work. Store ancestors must deny runtime writes. This function
// never reuses, replaces or cleans an existing bundle, including one containing unsaved session data.
func Materialize(ctx context.Context, store *os.Root, bundle string, source Manifest, open ObjectOpener, options MaterializeOptions) (PermissionBaseline, error) {
	return materialize(ctx, store, bundle, source, open, options, nil)
}

func materialize(ctx context.Context, store *os.Root, bundle string, source Manifest, open ObjectOpener, options MaterializeOptions, seal func(*os.Root) error) (PermissionBaseline, error) {
	baseline, err := validatePreparation(bundle, source, options)
	if err != nil {
		return PermissionBaseline{}, err
	}
	return materializeValidated(ctx, store, bundle, baseline, open, options, seal)
}

// Only validated session or independent deployment entry points may select ownership and names.
func materializeValidated(ctx context.Context, store *os.Root, bundle string, baseline PermissionBaseline, open ObjectOpener, options MaterializeOptions, seal func(*os.Root) error) (PermissionBaseline, error) {
	if open == nil {
		return PermissionBaseline{}, errors.New("missing authorized skill object opener")
	}
	if err := ctx.Err(); err != nil {
		return PermissionBaseline{}, err
	}
	storeFile, err := store.OpenFile(".", os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return PermissionBaseline{}, err
	}
	defer storeFile.Close()
	if err := verifyPrivateStore(storeFile); err != nil {
		return PermissionBaseline{}, err
	}
	if _, err := store.Lstat(bundle); !errors.Is(err, os.ErrNotExist) {
		return PermissionBaseline{}, errors.New("skill bundle already exists or is inaccessible")
	}
	if err := checkDiskSpace(storeFile, baseline.Source, options.Policy); err != nil {
		return PermissionBaseline{}, err
	}
	staging := ".prepare-" + rand.Text()
	if err := store.Mkdir(staging, 0o700); err != nil {
		return PermissionBaseline{}, err
	}
	published := false
	defer func() {
		if !published {
			_ = store.RemoveAll(staging)
		}
	}()
	stage, err := store.OpenRoot(staging)
	if err != nil {
		return PermissionBaseline{}, err
	}
	defer stage.Close()
	if err := prepareBundle(ctx, stage, baseline, open, options); err != nil {
		return PermissionBaseline{}, err
	}
	if seal != nil {
		if err := seal(stage); err != nil {
			return PermissionBaseline{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return PermissionBaseline{}, err
	}
	if err := unix.Renameat2(int(storeFile.Fd()), staging, int(storeFile.Fd()), bundle, unix.RENAME_NOREPLACE); err != nil {
		return PermissionBaseline{}, fmt.Errorf("publish skill bundle: %w", err)
	}
	published = true
	if err := storeFile.Sync(); err != nil {
		// The complete bundle survives for inspection/recovery; never remove after publication.
		return PermissionBaseline{}, fmt.Errorf("sync published skill bundle: %w", err)
	}
	return baseline, nil
}

func prepareBundle(ctx context.Context, stage *os.Root, baseline PermissionBaseline, open ObjectOpener, options MaterializeOptions) error {
	if err := stage.Mkdir("work", 0o700); err != nil {
		return err
	}
	work, err := stage.OpenRoot("work")
	if err != nil {
		return err
	}
	defer work.Close()
	// Objects are copied sequentially; one buffer avoids per-file allocation at the entry limit.
	buffer := make([]byte, 64*1024)
	for _, entry := range baseline.Materialized.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch entry.Kind {
		case "directory":
			err = work.Mkdir(entry.Path, 0o700)
		case "file":
			err = copyObject(ctx, work, entry, open, options, buffer)
		}
		if err != nil {
			return err
		}
	}
	// Links are installed only after all ordinary objects exist, never used as write paths.
	for _, entry := range baseline.Materialized.Entries {
		if entry.Kind == "symlink" || entry.Kind == "runtime_link" {
			if err := work.Symlink(entry.Target, entry.Path); err != nil {
				return err
			}
			if err := work.Lchown(entry.Path, options.UID, options.GID); err != nil {
				return err
			}
		}
	}
	for index := len(baseline.Materialized.Entries) - 1; index >= 0; index-- {
		entry := baseline.Materialized.Entries[index]
		if entry.Kind == "directory" {
			if err := finishDirectory(work, entry.Path, os.FileMode(entry.Mode), options); err != nil {
				return err
			}
		}
	}
	if err := finishDirectory(work, ".", 0o700, options); err != nil {
		return err
	}
	file, err := stage.OpenFile("baseline.json", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	writeErr := writePermissionBaseline(file, baseline)
	err = errors.Join(writeErr, file.Sync(), file.Close())
	if err != nil {
		return err
	}
	return syncDirectory(stage, ".")
}

func finishDirectory(root *os.Root, path string, mode os.FileMode, options MaterializeOptions) error {
	file, err := root.OpenFile(path, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Chown(options.UID, options.GID); err != nil {
		return err
	}
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if err := verifyMaterializedOwnership(file, mode, options); err != nil {
		return err
	}
	return file.Sync()
}

func verifyMaterializedOwnership(file *os.File, mode os.FileMode, options MaterializeOptions) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return err
	}
	if info.Mode().Perm() != mode || stat.Uid != uint32(options.UID) || stat.Gid != uint32(options.GID) {
		return errors.New("skill filesystem did not preserve materialized permissions and ownership")
	}
	return nil
}

func syncDirectory(root *os.Root, path string) error {
	file, err := root.OpenFile(path, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}
