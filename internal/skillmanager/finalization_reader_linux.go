package skillmanager

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// FinalizationObjectReader pins one immutable capture for sequential, manifest-scoped object reads.
// The caller serializes Open/Close and still verifies every object's streamed bytes.
type FinalizationObjectReader struct {
	manifest     *os.File
	objects      *os.File
	manifestInfo os.FileInfo
	objectsInfo  os.FileInfo
	entries      map[string]Entry
}

// OpenFinalizationReader validates the original capture once and holds its kernel read lock.
// The returned manifest descriptor belongs to the reader and must not be closed separately.
func OpenFinalizationReader(bundle *os.Root, expected FinalizationRecord) (*FinalizationObjectReader, *os.File, error) {
	if expected.Validate() != nil || expected.ObjectsVersion != 1 {
		return nil, nil, errors.New("invalid finalization reader input")
	}
	manifest, record, err := HoldFinalizationContent(bundle, expected.Binding)
	if err != nil {
		return nil, nil, err
	}
	reader := &FinalizationObjectReader{manifest: manifest}
	ready := false
	defer func() {
		if !ready {
			_ = reader.Close()
		}
	}()
	if !SameFinalizationInput(record, expected) {
		return nil, nil, errors.New("finalization reader changed original capture")
	}
	reader.manifestInfo, err = manifest.Stat()
	if err != nil {
		return nil, nil, err
	}
	data, err := io.ReadAll(io.LimitReader(manifest, maxManifestBytes+1))
	if err != nil || len(data) > maxManifestBytes {
		return nil, nil, errors.New("invalid reader manifest length")
	}
	tree, err := DecodeManifest(data)
	if err != nil {
		return nil, nil, err
	}
	digest, err := Digest(tree)
	if err != nil || digest != expected.TreeDigest {
		return nil, nil, errors.New("reader manifest differs from original capture")
	}
	journal, err := bundle.OpenRoot("finalization")
	if err != nil {
		return nil, nil, err
	}
	reader.objects, err = openPrivateRetainedDirectory(journal, "objects")
	_ = journal.Close()
	if err != nil {
		return nil, nil, err
	}
	reader.objectsInfo, err = reader.objects.Stat()
	if err != nil {
		return nil, nil, err
	}
	reader.entries = make(map[string]Entry)
	for _, entry := range tree.Entries {
		if entry.Kind == "file" {
			if prior, ok := reader.entries[entry.SHA256]; ok {
				if prior.Size != entry.Size || prior.ContentKind != entry.ContentKind {
					return nil, nil, errors.New("inconsistent reader object identity")
				}
			} else {
				reader.entries[entry.SHA256] = entry
			}
		}
	}
	if _, err := manifest.Seek(0, io.SeekStart); err != nil {
		return nil, nil, err
	}
	if err := reader.unchanged(); err != nil {
		return nil, nil, err
	}
	ready = true
	return reader, manifest, nil
}

func (r *FinalizationObjectReader) unchanged() error {
	if r.manifest == nil || r.objects == nil {
		return errors.New("finalization reader is closed")
	}
	for _, item := range []struct {
		file   *os.File
		before os.FileInfo
	}{{r.manifest, r.manifestInfo}, {r.objects, r.objectsInfo}} {
		after, err := item.file.Stat()
		if err != nil || !sameSourceInfo(item.before, after) {
			return errors.New("finalization reader metadata changed")
		}
	}
	return nil
}

// Open returns only a retained manifest member, protected by its own shared inode lock.
func (r *FinalizationObjectReader) Open(digest string) (*os.File, Entry, error) {
	if err := r.unchanged(); err != nil {
		return nil, Entry{}, err
	}
	entry, ok := r.entries[digest]
	if !ok {
		return nil, Entry{}, errors.New("object is not in retained reader manifest")
	}
	file, err := openPrivateRetainedFile(r.objects, digest, entry.Size)
	if err != nil {
		return nil, Entry{}, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_SH|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, Entry{}, errors.New("finalization object is in use")
	}
	if err := r.unchanged(); err != nil {
		_ = file.Close()
		return nil, Entry{}, err
	}
	return file, entry, nil
}

// Close releases the reader's descriptors and manifest index; transferred descriptors stay valid.
func (r *FinalizationObjectReader) Close() error {
	var err error
	if r.objects != nil {
		err = r.objects.Close()
		r.objects = nil
	}
	if r.manifest != nil {
		err = errors.Join(err, r.manifest.Close())
		r.manifest = nil
	}
	r.entries = nil
	return err
}
