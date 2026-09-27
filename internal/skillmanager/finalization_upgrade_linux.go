package skillmanager

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// The finalizer calls this only after the same whole-runtime writer-exit proof used for capture.
// Recovery copies the original recorded bytes; it cannot recapture or reclassify an old receipt.
func retainLegacyFinalizationObjects(ctx context.Context, bundle *os.Root, record FinalizationRecord, policy CopyPolicy) (FinalizationRecord, error) {
	journal, err := bundle.OpenRoot("finalization")
	if err != nil {
		return FinalizationRecord{}, err
	}
	defer journal.Close()
	parent, err := privateBundleFile(journal)
	if err != nil {
		return FinalizationRecord{}, err
	}
	defer parent.Close()
	var manifest Manifest
	if err := readPrivateJSON(journal, "manifest.json", maxManifestBytes, &manifest); err != nil {
		return FinalizationRecord{}, err
	}
	digest, err := Digest(manifest)
	if err != nil || digest != record.TreeDigest || policy.ReservePercent > 100 {
		return FinalizationRecord{}, errors.New("invalid legacy finalization input or reserve")
	}
	objects, err := openPrivateRetainedDirectory(journal, "objects")
	if err == nil {
		_ = objects.Close()
	} else if errors.Is(err, os.ErrNotExist) {
		if err := copyLegacyFinalizationObjects(ctx, bundle, journal, parent, manifest, policy); err != nil {
			return FinalizationRecord{}, err
		}
	} else {
		return FinalizationRecord{}, err
	}
	// A crash may have published objects before publishing the version marker. Verify all bytes
	// before finishing that transition; an existing partial or corrupt objects directory is never repaired.
	if err := verifyFinalizationObjects(ctx, journal, manifest); err != nil {
		return FinalizationRecord{}, err
	}
	record.ObjectsVersion = 1
	if err := writePrivateJSON(journal, parent, "record.json", record, false); err != nil {
		return FinalizationRecord{}, err
	}
	return record, nil
}

func copyLegacyFinalizationObjects(ctx context.Context, bundle, journal *os.Root, parent *os.File, manifest Manifest, policy CopyPolicy) error {
	if err := checkDiskSpace(parent, manifest, policy); err != nil {
		return err
	}
	work, err := bundle.OpenRoot("work")
	if err != nil {
		return err
	}
	defer work.Close()
	name := ".objects-" + rand.Text()
	if err := journal.Mkdir(name, 0o700); err != nil {
		return err
	}
	defer journal.RemoveAll(name)
	stage, err := journal.OpenRoot(name)
	if err != nil {
		return err
	}
	defer stage.Close()
	if err := copyRetainedObjects(ctx, stage, work, manifest); err != nil {
		return err
	}
	directory, err := privateBundleFile(stage)
	if err != nil {
		return err
	}
	defer directory.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := unix.Renameat2(int(directory.Fd()), "objects", int(parent.Fd()), "objects", unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	return parent.Sync()
}

func verifyFinalizationObjects(ctx context.Context, journal *os.Root, manifest Manifest) error {
	objects, err := openPrivateRetainedDirectory(journal, "objects")
	if err != nil {
		return err
	}
	defer objects.Close()
	seen := make(map[string]bool)
	for _, entry := range manifest.Entries {
		if entry.Kind != "file" || seen[entry.SHA256] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		file, err := openPrivateRetainedFile(objects, entry.SHA256, entry.Size)
		if err != nil {
			return err
		}
		err = verifyRetainedBytes(ctx, file, entry)
		_ = file.Close()
		if err != nil {
			return err
		}
		seen[entry.SHA256] = true
	}
	names, err := objects.Readdirnames(len(seen) + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(names) != len(seen) {
		return errors.New("retained objects differ from original finalization")
	}
	for _, name := range names {
		if !seen[name] {
			return errors.New("undeclared retained finalization object")
		}
	}
	return nil
}

func verifyRetainedBytes(ctx context.Context, file *os.File, entry Entry) error {
	verifier, err := NewContentVerifier(entry)
	if err != nil {
		return err
	}
	buffer := make([]byte, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := file.Read(buffer)
		if _, err := verifier.Write(buffer[:n]); err != nil {
			return err
		}
		if errors.Is(readErr, io.EOF) {
			return verifier.Finish()
		}
		if readErr != nil {
			return readErr
		}
	}
}
