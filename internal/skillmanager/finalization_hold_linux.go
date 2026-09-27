package skillmanager

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// HoldFinalizationContent protects the complete frozen-input read lifetime until the returned file closes.
// The caller may transfer this read-only descriptor with SCM_RIGHTS; its kernel lock survives Helper restart.
func HoldFinalizationContent(bundle *os.Root, binding SnapshotBinding) (*os.File, FinalizationRecord, error) {
	file, err := lockFinalizationContent(bundle, binding, false)
	if err != nil {
		return nil, FinalizationRecord{}, err
	}
	record, err := ReadFinalization(bundle, binding)
	if err == nil {
		err = requireUnreclaimedWork(bundle)
	}
	if err != nil || record.ObjectsVersion != 1 {
		_ = file.Close()
		return nil, FinalizationRecord{}, errors.New("finalization content cannot be held for reading")
	}
	return file, record, nil
}

func lockFinalizationContent(bundle *os.Root, binding SnapshotBinding, exclusive bool) (*os.File, error) {
	if _, err := ReadFinalization(bundle, binding); err != nil {
		return nil, err
	}
	journal, err := openPrivateRetainedDirectory(bundle, "finalization")
	if err != nil {
		return nil, err
	}
	defer journal.Close()
	// The frozen manifest inode is immutable and survives reclamation. Reusing it keeps
	// frozen export read-only and available even when no new metadata can be allocated.
	file, err := openPrivateRetainedFile(journal, "manifest.json", -1)
	if err != nil {
		return nil, err
	}
	operation := unix.LOCK_SH
	if exclusive {
		operation = unix.LOCK_EX
	}
	if err := unix.Flock(int(file.Fd()), operation|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, errors.New("finalization content is in use")
	}
	return file, nil
}
