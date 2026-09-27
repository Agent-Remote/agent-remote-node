package skillmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// ReadFinalizationReclamation returns immutable intent and completion without requiring deleted bytes.
// Absence returns nil, false, nil. Corrupt or incomplete evidence never counts as absence.
func ReadFinalizationReclamation(bundle *os.Root, binding SnapshotBinding) (*FinalizationReclamation, bool, error) {
	record, err := ReadFinalization(bundle, binding)
	if err != nil {
		return nil, false, err
	}
	journal, err := bundle.OpenRoot("finalization")
	if err != nil {
		return nil, false, err
	}
	defer journal.Close()
	return readReclamationIntent(bundle, journal, record)
}

func readReclamationIntent(bundle, journal *os.Root, capture FinalizationRecord) (*FinalizationReclamation, bool, error) {
	var intent FinalizationReclamation
	if err := readPrivateJSON(journal, "reclamation.json", 1<<20, &intent); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, false, err
		}
		if _, err := journal.Lstat("reclaimed.json"); !errors.Is(err, os.ErrNotExist) {
			return nil, false, errors.New("reclamation completion lacks original intent")
		}
		return nil, false, nil
	}
	if intent.Version != 1 || intent.Authorization.PublicationStatus == "conflicted" || intent.Capture != capture || !capture.CanDeleteSession() || capture.ObjectsVersion != 1 ||
		intent.Work.Inode == 0 || intent.Objects.Inode == 0 || intent.Work == intent.Objects ||
		!filepath.IsAbs(intent.SessionRoot) || filepath.Clean(intent.SessionRoot) != intent.SessionRoot || intent.SessionRoot == "/" {
		return nil, false, errors.New("reclamation intent changed original input")
	}
	ack, err := readFinalizationAcknowledgement(journal, capture)
	if err != nil {
		return nil, false, err
	}
	state, err := ack.State()
	if err != nil || state != capture.State || intent.Authorization.Match(ack) != nil {
		return nil, false, errors.New("reclamation intent lacks original terminal acknowledgement")
	}
	var cleanup finalizationRuntimeCleanup
	if err := readPrivateJSON(journal, "runtime-cleanup.json", 1<<20, &cleanup); err != nil {
		return nil, false, err
	}
	if cleanup.Version != 1 || cleanup.Capture != capture || cleanup.SessionRoot != intent.SessionRoot {
		return nil, false, errors.New("reclamation intent lacks original runtime cleanup")
	}
	var completion FinalizationReclamation
	err = readPrivateJSON(journal, "reclaimed.json", 1<<20, &completion)
	complete := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	if complete && completion != intent {
		return nil, false, errors.New("reclamation completion changed original intent")
	}
	if err := checkReclamationDirectory(bundle, "work", intent.Work, complete); err != nil {
		return nil, false, err
	}
	if err := checkReclamationDirectory(journal, "objects", intent.Objects, complete); err != nil {
		return nil, false, err
	}
	return &intent, complete, nil
}

func checkReclamationDirectory(parent *os.Root, name string, expected ReclamationDirectory, absent bool) error {
	identity, err := reclamationDirectory(parent, name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if absent || identity != expected {
		return errors.New("reclamation content root reappeared or was replaced")
	}
	return nil
}

func reclamationDirectory(parent *os.Root, name string) (ReclamationDirectory, error) {
	directory, err := privateBundleFile(parent)
	if err != nil {
		return ReclamationDirectory{}, err
	}
	defer directory.Close()
	fd, err := unix.Openat(int(directory.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return ReclamationDirectory{}, err
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return ReclamationDirectory{}, err
	}
	if name == "objects" && (stat.Uid != uint32(os.Geteuid()) || stat.Mode&0o7777 != 0o700) {
		return ReclamationDirectory{}, errors.New("reclamation object directory lost private ownership or mode")
	}
	return ReclamationDirectory{Device: uint64(stat.Dev), Inode: stat.Ino}, nil
}

// MarkFinalizationReclamation saves intent after checking fresh authority and unchanged work.
// The caller must hold lifecycle exclusion and independently prove no writers, mounts/aliases or
// local consumers reference either content root. This function never removes data. Deadline must
// be the original process-local monotonic deadline, never reconstructed from saved JSON.
func MarkFinalizationReclamation(ctx context.Context, bundle *os.Root, capture FinalizationRecord, authority ReclamationAuthorization, deadline time.Time, sessionRoot string) (FinalizationReclamation, error) {
	if err := ctx.Err(); err != nil {
		return FinalizationReclamation{}, err
	}
	lock, err := lockFinalizationContent(bundle, capture.Binding, true)
	if err != nil {
		return FinalizationReclamation{}, err
	}
	defer lock.Close()
	previous, _, err := ReadFinalizationReclamation(bundle, capture.Binding)
	if err != nil {
		return FinalizationReclamation{}, err
	}
	if previous != nil {
		if previous.Capture != capture || previous.SessionRoot != sessionRoot || !sameReclamationAuthorization(previous.Authorization, authority) {
			return FinalizationReclamation{}, errors.New("reclamation retry changed immutable intent")
		}
		return *previous, syncDirectory(bundle, "finalization")
	}
	if authority.PublicationStatus == "conflicted" {
		return FinalizationReclamation{}, errors.New("reclamation preserves unresolved conflict content")
	}
	if err := reclamationBudget(ctx, deadline); err != nil {
		return FinalizationReclamation{}, err
	}
	ack, err := ReadFinalizationAcknowledgement(bundle, capture.Binding)
	if err != nil || authority.Match(ack) != nil {
		return FinalizationReclamation{}, errors.New("reclamation lacks matching fresh authorization")
	}
	cleaned, err := FinalizationRuntimeCleaned(bundle, capture, sessionRoot)
	if err != nil || !cleaned {
		return FinalizationReclamation{}, errors.New("reclamation requires original completed runtime cleanup")
	}
	journal, err := bundle.OpenRoot("finalization")
	if err != nil {
		return FinalizationReclamation{}, err
	}
	defer journal.Close()
	intent := FinalizationReclamation{Version: 1, Capture: capture, Authorization: authority, SessionRoot: sessionRoot}
	intent.Work, err = reclamationDirectory(bundle, "work")
	if err != nil {
		return FinalizationReclamation{}, err
	}
	intent.Objects, err = reclamationDirectory(journal, "objects")
	if err != nil {
		return FinalizationReclamation{}, err
	}
	if intent.Work == intent.Objects {
		return FinalizationReclamation{}, errors.New("reclamation content roots are aliased")
	}
	if err := verifyReclamationWork(ctx, bundle, capture); err != nil {
		return FinalizationReclamation{}, err
	}
	// Hashing may outlive the HTTP budget; check again immediately before durable marking.
	if err := reclamationBudget(ctx, deadline); err != nil {
		return FinalizationReclamation{}, err
	}
	if err := checkReclamationDirectory(bundle, "work", intent.Work, false); err != nil {
		return FinalizationReclamation{}, err
	}
	directory, err := privateBundleFile(journal)
	if err != nil {
		return FinalizationReclamation{}, err
	}
	defer directory.Close()
	if err := writePrivateJSON(journal, directory, "reclamation.json", intent, true); err != nil {
		return FinalizationReclamation{}, err
	}
	return intent, nil
}

func reclamationBudget(ctx context.Context, deadline time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Round strips the monotonic component. Reject a deserialized wall-clock-only deadline.
	remaining := time.Until(deadline)
	if deadline == deadline.Round(0) || remaining <= 0 || remaining > time.Minute {
		return errors.New("reclamation requires a fresh monotonic authorization budget")
	}
	return nil
}

func sameReclamationAuthorization(first, second ReclamationAuthorization) bool {
	if !first.VerifiedAt.Equal(second.VerifiedAt) || !first.ExpiresAt.Equal(second.ExpiresAt) {
		return false
	}
	first.VerifiedAt, first.ExpiresAt = time.Time{}, time.Time{}
	second.VerifiedAt, second.ExpiresAt = time.Time{}, time.Time{}
	return first == second
}

func verifyReclamationWork(ctx context.Context, bundle *os.Root, capture FinalizationRecord) error {
	snapshot, err := loadPreparedSnapshot(bundle, capture.Binding)
	if err != nil {
		return err
	}
	var baseline PermissionBaseline
	if err := readPrivateJSON(bundle, "baseline.json", maxBaselineBytes, &baseline); err != nil {
		return err
	}
	before, err := bundle.Lstat("work")
	if err != nil || !before.IsDir() {
		return errors.New("reclamation work is not an original directory")
	}
	work, err := bundle.OpenRoot("work")
	if err != nil {
		return err
	}
	defer work.Close()
	if err := unchangedAt(work, ".", before); err != nil {
		return err
	}
	if err := verifyEmptyReclamationExclusions(work, snapshot.Capture.SystemPaths); err != nil {
		return err
	}
	manifest, err := captureWorkTree(ctx, work, baseline, snapshot.Capture, false)
	if err != nil {
		return err
	}
	digest, err := Digest(manifest)
	if err != nil || digest != capture.TreeDigest {
		return errors.New("reclamation would lose work changed after finalization")
	}
	return unchangedAt(bundle, "work", before)
}

func verifyEmptyReclamationExclusions(work *os.Root, exclusions []string) error {
	for _, name := range exclusions {
		before, err := work.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !before.IsDir() {
			return errors.New("reclamation preserves unrecorded system-path content")
		}
		directory, err := work.OpenRoot(name)
		if err != nil {
			return err
		}
		names, err := captureNames(directory, 1)
		if err == nil {
			err = unchangedAt(directory, ".", before)
		}
		_ = directory.Close()
		if err != nil {
			return err
		}
		if len(names) != 0 {
			return errors.New("reclamation preserves unrecorded system-path content")
		}
		if err := unchangedAt(work, name, before); err != nil {
			return err
		}
	}
	return nil
}
