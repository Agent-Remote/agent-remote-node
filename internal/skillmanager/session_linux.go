package skillmanager

import (
	"context"
	"errors"
	"math"
	"os"
	"reflect"
	"regexp"

	"golang.org/x/sys/unix"
)

// RuntimeBinding retains helper-selected execution identity independently of transient specs.
// It is never populated from an unvalidated task-supplied host path or process identifier.
type RuntimeBinding struct {
	Backend    string `json:"backend"`
	ResourceID string `json:"resource_id"`
	BootID     string `json:"boot_id"`
	UID        int    `json:"uid"`
	GID        int    `json:"gid"`
}

// SessionSnapshot is the complete durable preparation receipt used for lifecycle recovery.
type SessionSnapshot struct {
	Snapshot PreparedSnapshot `json:"snapshot"`
	Runtime  RuntimeBinding   `json:"runtime"`
}

var runtimeResourcePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

// PrepareSessionSnapshot atomically publishes the writable tree and its complete ownership binding.
// Identical retries return the original bundle without replacing runtime modifications. The caller
// must serialize disk admission and lifecycle operations, and authorize all content before calling.
func PrepareSessionSnapshot(ctx context.Context, store *os.Root, source Manifest, open ObjectOpener, receipt SessionSnapshot, options MaterializeOptions) error {
	if err := validateSessionSnapshot(receipt); err != nil {
		return err
	}
	if options.UID != receipt.Runtime.UID || options.GID != receipt.Runtime.GID {
		return errors.New("skill runtime ownership differs from the prepared identity")
	}
	if _, err := validatePreparation("session", source, options); err != nil {
		return err
	}
	digest, err := Digest(source)
	if err != nil || digest != receipt.Snapshot.Binding.InitialTreeDigest {
		return errors.New("skill source differs from the reserved session tree")
	}
	if receipt.Snapshot.Capture.DirectoryBytes != options.Policy.DirectoryBytes || receipt.Snapshot.Capture.Entries != options.Policy.Entries ||
		!reflect.DeepEqual(receipt.Snapshot.Capture.RuntimeDependencies, options.RuntimeDependencies) {
		return errors.New("skill capture and materialization policies differ")
	}
	name := sessionBundleName(receipt.Snapshot.Binding.SessionID)
	if _, err := store.Lstat(name); err == nil {
		bundle, previous, err := OpenSessionSnapshot(store, receipt.Snapshot.Binding.SessionID)
		if err != nil {
			return err
		}
		defer bundle.Close()
		if !reflect.DeepEqual(previous, receipt) {
			return errors.New("skill session preparation conflicts with the retained snapshot")
		}
		return ctx.Err()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, err = materialize(ctx, store, name, source, open, options, func(bundle *os.Root) error {
		if err := SealPreparedSnapshot(bundle, receipt.Snapshot); err != nil {
			return err
		}
		directory, err := privateBundleFile(bundle)
		if err != nil {
			return err
		}
		defer directory.Close()
		return writePrivateJSON(bundle, directory, "runtime.json", receipt.Runtime, true)
	})
	return err
}

// OpenSessionSnapshot resolves only a validated session ID and checks its retained helper identity.
// Missing/corrupt sealed records in an existing bundle fail closed; they are never recreated.
func OpenSessionSnapshot(store *os.Root, sessionID string) (*os.Root, SessionSnapshot, error) {
	if !validSnapshotID(sessionID) {
		return nil, SessionSnapshot{}, errors.New("invalid skill session identity")
	}
	storeFile, err := privateBundleFile(store)
	if err != nil {
		return nil, SessionSnapshot{}, err
	}
	_ = storeFile.Close()
	name := sessionBundleName(sessionID)
	info, err := store.Lstat(name)
	if err != nil {
		return nil, SessionSnapshot{}, err
	}
	if !info.IsDir() {
		return nil, SessionSnapshot{}, errors.New("skill session bundle is not a directory")
	}
	bundle, err := store.OpenRoot(name)
	if err != nil {
		return nil, SessionSnapshot{}, err
	}
	receipt, err := readSessionSnapshot(bundle, sessionID)
	if err != nil {
		_ = bundle.Close()
		// Missing metadata inside an existing bundle must not look like an absent session.
		return nil, SessionSnapshot{}, errors.New("retained skill session binding is invalid or incomplete")
	}
	return bundle, receipt, nil
}

func readSessionSnapshot(bundle *os.Root, sessionID string) (SessionSnapshot, error) {
	directory, err := privateBundleFile(bundle)
	if err != nil {
		return SessionSnapshot{}, err
	}
	defer directory.Close()
	var receipt SessionSnapshot
	if err := readPrivateJSON(bundle, "snapshot.json", 1<<20, &receipt.Snapshot); err != nil {
		return SessionSnapshot{}, err
	}
	if err := readPrivateJSON(bundle, "runtime.json", 1<<20, &receipt.Runtime); err != nil {
		return SessionSnapshot{}, err
	}
	if err := validateSessionSnapshot(receipt); err != nil {
		return SessionSnapshot{}, err
	}
	if receipt.Snapshot.Binding.SessionID != sessionID {
		return SessionSnapshot{}, errors.New("skill session bundle belongs to another session")
	}
	var baseline PermissionBaseline
	if err := readPrivateJSON(bundle, "baseline.json", maxBaselineBytes, &baseline); err != nil {
		return SessionSnapshot{}, err
	}
	if _, err := RestoreSourceModes(baseline.Materialized, baseline); err != nil {
		return SessionSnapshot{}, err
	}
	digest, err := Digest(baseline.Source)
	if err != nil || digest != receipt.Snapshot.Binding.InitialTreeDigest {
		return SessionSnapshot{}, errors.New("skill session baseline no longer matches the reserved tree")
	}
	return receipt, nil
}

func validateSessionSnapshot(receipt SessionSnapshot) error {
	if err := validateSnapshot(receipt.Snapshot); err != nil {
		return err
	}
	runtime := receipt.Runtime
	if runtime.Backend != "native" && runtime.Backend != "docker_sandbox" || !runtimeResourcePattern.MatchString(runtime.ResourceID) ||
		!validSnapshotID(runtime.BootID) || runtime.UID <= 0 || runtime.GID <= 0 || runtime.UID > math.MaxInt32 || runtime.GID > math.MaxInt32 {
		return errors.New("invalid prepared skill runtime identity")
	}
	return nil
}

func validSnapshotID(value string) bool {
	return snapshotIDPattern.MatchString(value) && value != "00000000-0000-0000-0000-000000000000"
}

func sessionBundleName(sessionID string) string {
	return "session-" + sessionID
}

// OpenSessionWork opens only the retained work directory, rejecting a replaced symlink.
// The returned descriptor is for privileged mount/capture operations, never worker path access.
func OpenSessionWork(bundle *os.Root, runtime RuntimeBinding) (*os.File, error) {
	if runtime.UID <= 0 || runtime.GID <= 0 || runtime.UID > math.MaxInt32 || runtime.GID > math.MaxInt32 {
		return nil, errors.New("skill work directory requires a non-root runtime identity")
	}
	if err := requireUnreclaimedWork(bundle); err != nil {
		return nil, err
	}
	parent, err := privateBundleFile(bundle)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	fd, err := unix.Openat(int(parent.Fd()), "work", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "work")
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		_ = file.Close()
		return nil, err
	}
	if stat.Uid != uint32(runtime.UID) || stat.Gid != uint32(runtime.GID) {
		_ = file.Close()
		return nil, errors.New("skill work directory has unexpected runtime ownership")
	}
	return file, nil
}
