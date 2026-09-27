package skillmanager

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// OpenFinalizationManifest opens the original frozen manifest without consulting the work directory.
// Manifest-only legacy records remain readable through ReadFinalization, but cannot grant transfers.
func OpenFinalizationManifest(bundle *os.Root, binding SnapshotBinding) (*os.File, FinalizationRecord, error) {
	record, err := ReadFinalization(bundle, binding)
	if err != nil {
		return nil, FinalizationRecord{}, err
	}
	if record.ObjectsVersion != 1 {
		return nil, FinalizationRecord{}, errors.New("finalization has no sealed retained objects")
	}
	journal, err := openPrivateRetainedDirectory(bundle, "finalization")
	if err != nil {
		return nil, FinalizationRecord{}, err
	}
	defer journal.Close()
	metadata, err := bundle.OpenRoot("finalization")
	if err != nil {
		return nil, FinalizationRecord{}, err
	}
	err = reclamationContentError(bundle, metadata, record)
	_ = metadata.Close()
	if err != nil {
		return nil, FinalizationRecord{}, err
	}
	file, err := openPrivateRetainedFile(journal, "manifest.json", -1)
	if err != nil {
		return nil, FinalizationRecord{}, err
	}
	info, err := file.Stat()
	if err != nil || info.Size() <= 0 || info.Size() > maxManifestBytes {
		_ = file.Close()
		return nil, FinalizationRecord{}, errors.New("invalid retained finalization manifest size")
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_SH|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, FinalizationRecord{}, errors.New("finalization manifest is in use")
	}
	return file, record, nil
}

// OpenFinalizationObject opens one private frozen object from the exact original journal.
// The caller must verify streamed bytes; no fallback reads or recaptures live work after corruption.
func OpenFinalizationObject(bundle *os.Root, expected FinalizationRecord, digest string) (*os.File, Entry, error) {
	if err := expected.Validate(); err != nil {
		return nil, Entry{}, err
	}
	manifestFile, record, err := OpenFinalizationManifest(bundle, expected.Binding)
	if err != nil {
		return nil, Entry{}, err
	}
	// Keep the manifest hold until the object's own shared lock is acquired, so a reader
	// cannot slip between an exclusive reclamation check and object descriptor transfer.
	defer manifestFile.Close()
	// State may advance after the manifest was read; immutable input cannot change.
	if record.Version != expected.Version || record.ObjectsVersion != expected.ObjectsVersion || record.TreeDigest != expected.TreeDigest || record.Unclean != expected.Unclean {
		return nil, Entry{}, errors.New("finalization object belongs to another input")
	}
	journal, err := bundle.OpenRoot("finalization")
	if err != nil {
		return nil, Entry{}, err
	}
	defer journal.Close()
	var manifest Manifest
	if err := readPrivateJSON(journal, "manifest.json", maxManifestBytes, &manifest); err != nil {
		return nil, Entry{}, err
	}
	var selected Entry
	for _, entry := range manifest.Entries {
		if entry.Kind == "file" && entry.SHA256 == digest {
			selected = entry
			break
		}
	}
	if selected.SHA256 == "" {
		return nil, Entry{}, errors.New("object is not in retained finalization")
	}
	objects, err := openPrivateRetainedDirectory(journal, "objects")
	if err != nil {
		return nil, Entry{}, err
	}
	defer objects.Close()
	file, err := openPrivateRetainedFile(objects, digest, selected.Size)
	if err != nil {
		return nil, Entry{}, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_SH|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, Entry{}, errors.New("finalization object is in use")
	}
	return file, selected, nil
}

func openPrivateRetainedDirectory(root *os.Root, name string) (*os.File, error) {
	parent, err := privateBundleFile(root)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	if err := verifyPrivateStore(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func openPrivateRetainedFile(parent *os.File, name string, size int64) (*os.File, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = file.Close()
		return nil, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o7777 != 0o600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 || size >= 0 && stat.Size != size {
		_ = file.Close()
		return nil, errors.New("unsafe retained finalization file")
	}
	return file, nil
}
