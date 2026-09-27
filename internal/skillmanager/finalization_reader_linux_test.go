package skillmanager

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFinalizationReaderPinsOriginalObjectsAndRefusesUnknownDigests(t *testing.T) {
	bundle, path, record, entry := frozenFinalization(t)
	reader, _, err := OpenFinalizationReader(bundle, record)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err := os.RemoveAll(filepath.Join(path, "work")); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		file, actual, err := reader.Open(entry.SHA256)
		if err != nil || actual != entry {
			t.Fatal("reader lost original member", err)
		}
		if _, err := file.Write([]byte("changed")); err == nil {
			t.Fatal("reader returned writable object")
		}
		data, err := io.ReadAll(file)
		_ = file.Close()
		if err != nil || VerifyContent(entry, data) != nil {
			t.Fatal("reader changed frozen bytes", err)
		}
	}
	if file, _, err := reader.Open(strings.Repeat("0", 64)); err == nil || file != nil {
		t.Fatal("unknown digest escaped manifest scope")
	}
	_ = reader.Close()
	if file, _, err := reader.Open(entry.SHA256); err == nil || file != nil {
		t.Fatal("closed reader reopened content")
	}
}

func TestFinalizationReaderDetectsMetadataAndObjectSubstitution(t *testing.T) {
	for _, fault := range []string{"manifest", "directory", "symlink", "mode", "hardlink"} {
		t.Run(fault, func(t *testing.T) {
			bundle, path, record, entry := frozenFinalization(t)
			reader, _, err := OpenFinalizationReader(bundle, record)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			object := filepath.Join(path, "finalization/objects", entry.SHA256)
			switch fault {
			case "manifest":
				err = os.Chmod(filepath.Join(path, "finalization/manifest.json"), 0o640)
			case "directory":
				err = os.WriteFile(filepath.Join(path, "finalization/objects/new"), nil, 0o600)
			case "symlink":
				err = os.Remove(object)
				if err == nil {
					err = os.Symlink(filepath.Join(path, "work/a"), object)
				}
			case "mode":
				err = os.Chmod(object, 0o644)
			case "hardlink":
				err = os.Link(object, filepath.Join(path, "extra-link"))
			}
			if err != nil {
				t.Fatal(err)
			}
			if file, _, err := reader.Open(entry.SHA256); err == nil || file != nil {
				t.Fatal("changed private reader state accepted")
			}
		})
	}
}

func TestFinalizationReaderExcludesReclamationUntilClosure(t *testing.T) {
	bundle, capture, authority := reclamationBundle(t)
	reader, _, err := OpenFinalizationReader(bundle, capture)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := MarkFinalizationReclamation(context.Background(), bundle, capture, authority, time.Now().Add(time.Minute), "/runtime/original"); err == nil {
		t.Fatal("active reader permitted reclamation")
	}
	_ = reader.Close()
	markReclamation(t, bundle, capture, authority)
	if other, _, err := OpenFinalizationReader(bundle, capture); err == nil || other != nil {
		t.Fatal("marked content admitted a new reader")
	}
}
