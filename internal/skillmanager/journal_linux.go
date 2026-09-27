package skillmanager

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"reflect"
	"regexp"

	"golang.org/x/sys/unix"
)

var snapshotIDPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

// SealPreparedSnapshot durably binds a private bundle to its authorized session before launch.
// A retry must supply the same binding and capture policy; it cannot repurpose existing work.
func SealPreparedSnapshot(bundle *os.Root, snapshot PreparedSnapshot) error {
	if err := validateSnapshot(snapshot); err != nil {
		return err
	}
	file, err := privateBundleFile(bundle)
	if err != nil {
		return err
	}
	defer file.Close()
	var previous PreparedSnapshot
	if err := readPrivateJSON(bundle, "snapshot.json", 1<<20, &previous); err == nil {
		if !reflect.DeepEqual(previous, snapshot) {
			return errors.New("skill snapshot binding conflict")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var baseline PermissionBaseline
	if err := readPrivateJSON(bundle, "baseline.json", maxBaselineBytes, &baseline); err != nil {
		return err
	}
	if _, err := RestoreSourceModes(baseline.Materialized, baseline); err != nil {
		return err
	}
	digest, err := Digest(baseline.Source)
	if err != nil {
		return err
	}
	if digest != snapshot.Binding.InitialTreeDigest {
		return errors.New("prepared skill tree does not match the reserved snapshot")
	}
	for _, entry := range baseline.Source.Entries {
		if entry.Kind == "runtime_link" && snapshot.Capture.RuntimeDependencies[entry.Dependency] != entry.Target {
			return errors.New("capture policy cannot restore the prepared runtime dependencies")
		}
	}
	return writePrivateJSON(bundle, file, "snapshot.json", snapshot, true)
}

// FinalizeWorkTree captures once after proven writer exit, retaining unclean recovery as detached input.
// The caller must serialize this with preparation, uploads and lifecycle operations for the bundle.
// A durable journal is returned on retry without consulting a removed runtime spec or recapturing.
func FinalizeWorkTree(ctx context.Context, bundle *os.Root, binding SnapshotBinding, unclean bool) (FinalizationRecord, error) {
	return FinalizeWorkTreeWithPolicy(ctx, bundle, binding, unclean, CopyPolicy{})
}

// FinalizeWorkTreeWithPolicy retains private objects with the caller's configured disk reserve.
// Old manifest-only receipts retain their exact input while verified objects are added for transfer.
func FinalizeWorkTreeWithPolicy(ctx context.Context, bundle *os.Root, binding SnapshotBinding, unclean bool, policy CopyPolicy) (FinalizationRecord, error) {
	file, err := privateBundleFile(bundle)
	if err != nil {
		return FinalizationRecord{}, err
	}
	defer file.Close()
	snapshot, err := loadPreparedSnapshot(bundle, binding)
	if err != nil {
		return FinalizationRecord{}, err
	}
	if record, err := ReadFinalization(bundle, binding); err == nil {
		if err := requireUnreclaimedWork(bundle); err != nil {
			return FinalizationRecord{}, err
		}
		if record.ObjectsVersion == 0 {
			return retainLegacyFinalizationObjects(ctx, bundle, record, policy)
		}
		return record, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return FinalizationRecord{}, err
	}
	termination, err := retainTermination(bundle, binding, unclean)
	if err != nil {
		return FinalizationRecord{}, err
	}
	var baseline PermissionBaseline
	if err := readPrivateJSON(bundle, "baseline.json", maxBaselineBytes, &baseline); err != nil {
		return FinalizationRecord{}, err
	}
	digest, err := Digest(baseline.Source)
	if err != nil || digest != binding.InitialTreeDigest {
		return FinalizationRecord{}, errors.New("skill baseline no longer matches the reserved snapshot")
	}
	work, err := bundle.OpenRoot("work")
	if err != nil {
		return FinalizationRecord{}, err
	}
	defer work.Close()
	manifest, err := CaptureWorkTree(ctx, work, baseline, snapshot.Capture)
	if err != nil {
		return FinalizationRecord{}, err
	}
	treeDigest, err := Digest(manifest)
	if err != nil {
		return FinalizationRecord{}, err
	}
	if policy.ReservePercent > 100 {
		return FinalizationRecord{}, errors.New("invalid finalization disk reserve")
	}
	if err := checkDiskSpace(file, manifest, policy); err != nil {
		return FinalizationRecord{}, err
	}
	record := FinalizationRecord{Version: 1, ObjectsVersion: 1, Binding: binding, TreeDigest: treeDigest, Unclean: termination.Unclean, State: "local_durable"}
	stageName := ".finalize-" + rand.Text()
	if err := bundle.Mkdir(stageName, 0o700); err != nil {
		return FinalizationRecord{}, err
	}
	published := false
	defer func() {
		if !published {
			_ = bundle.RemoveAll(stageName)
		}
	}()
	stage, err := bundle.OpenRoot(stageName)
	if err != nil {
		return FinalizationRecord{}, err
	}
	defer stage.Close()
	if err := copyRetainedObjects(ctx, stage, work, manifest); err != nil {
		return FinalizationRecord{}, err
	}
	verified, err := CaptureWorkTree(ctx, work, baseline, snapshot.Capture)
	if err != nil {
		return FinalizationRecord{}, err
	}
	checked, err := Digest(verified)
	if err != nil || checked != treeDigest {
		return FinalizationRecord{}, errors.New("skill work changed while freezing finalization objects")
	}
	manifestFile, err := stage.OpenFile("manifest.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return FinalizationRecord{}, err
	}
	err = errors.Join(writeManifest(manifestFile, manifest), manifestFile.Sync(), manifestFile.Close())
	if err != nil {
		return FinalizationRecord{}, err
	}
	stageFile, err := privateBundleFile(stage)
	if err != nil {
		return FinalizationRecord{}, err
	}
	err = writePrivateJSON(stage, stageFile, "record.json", record, true)
	_ = stageFile.Close()
	if err != nil {
		return FinalizationRecord{}, err
	}
	if err := ctx.Err(); err != nil {
		return FinalizationRecord{}, err
	}
	if err := unix.Renameat2(int(file.Fd()), stageName, int(file.Fd()), "finalization", unix.RENAME_NOREPLACE); err != nil {
		return FinalizationRecord{}, err
	}
	published = true
	if err := file.Sync(); err != nil {
		return FinalizationRecord{}, err
	}
	return record, nil
}

// ReadFinalization revalidates identity and the complete persisted manifest before reporting state.
func ReadFinalization(bundle *os.Root, binding SnapshotBinding) (FinalizationRecord, error) {
	if _, err := loadPreparedSnapshot(bundle, binding); err != nil {
		return FinalizationRecord{}, err
	}
	directory, err := openPrivateRetainedDirectory(bundle, "finalization")
	if err != nil {
		return FinalizationRecord{}, err
	}
	_ = directory.Close()
	journal, err := bundle.OpenRoot("finalization")
	if err != nil {
		return FinalizationRecord{}, err
	}
	defer journal.Close()
	file, err := privateBundleFile(journal)
	if err != nil {
		return FinalizationRecord{}, err
	}
	defer file.Close()
	var record FinalizationRecord
	if err := readPrivateJSON(journal, "record.json", 1<<20, &record); err != nil {
		return FinalizationRecord{}, errors.New("skill finalization record is missing or invalid")
	}
	if record.Validate() != nil || record.Binding != binding {
		return FinalizationRecord{}, errors.New("invalid skill finalization record")
	}
	var manifest Manifest
	if err := readPrivateJSON(journal, "manifest.json", maxManifestBytes, &manifest); err != nil {
		return FinalizationRecord{}, errors.New("skill finalization manifest is missing or invalid")
	}
	digest, err := Digest(manifest)
	if err != nil || digest != record.TreeDigest {
		return FinalizationRecord{}, errors.New("skill finalization manifest integrity mismatch")
	}
	intent, _, err := readReclamationIntent(bundle, journal, record)
	if err != nil {
		return FinalizationRecord{}, err
	}
	if record.ObjectsVersion == 1 && intent == nil {
		objects, err := openPrivateRetainedDirectory(journal, "objects")
		if err != nil {
			return FinalizationRecord{}, errors.New("skill finalization objects are missing or unsafe")
		}
		_ = objects.Close()
	}
	return record, nil
}

// AdvanceFinalization applies a matching Server acknowledgement after authenticated transfer.
// Persisted means the Server retained the complete snapshot reference, not merely an uploaded file.
func AdvanceFinalization(bundle *os.Root, binding SnapshotBinding, expected, next string) (FinalizationRecord, error) {
	record, err := ReadFinalization(bundle, binding)
	if err != nil {
		return FinalizationRecord{}, err
	}
	if record.State == next {
		return record, nil
	}
	if record.State != expected || !allowedFinalizationTransition(record.State, next, record.Unclean) {
		return FinalizationRecord{}, errors.New("skill finalization state conflict")
	}
	journal, err := bundle.OpenRoot("finalization")
	if err != nil {
		return FinalizationRecord{}, err
	}
	defer journal.Close()
	file, err := privateBundleFile(journal)
	if err != nil {
		return FinalizationRecord{}, err
	}
	defer file.Close()
	record.State = next
	if err := writePrivateJSON(journal, file, "record.json", record, false); err != nil {
		return FinalizationRecord{}, err
	}
	return record, nil
}
