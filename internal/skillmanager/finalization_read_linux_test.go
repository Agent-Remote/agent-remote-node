package skillmanager

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func frozenFinalization(t *testing.T) (*os.Root, string, FinalizationRecord, Entry) {
	t.Helper()
	bundle, path, binding := sealedBundle(t)
	content := []byte("frozen\x00\xff")
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(path, "work", name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	record, err := FinalizeWorkTree(context.Background(), bundle, binding, false)
	if err != nil || record.ObjectsVersion != 1 {
		t.Fatal("finalization did not retain objects", err)
	}
	return bundle, path, record, objectEntry("a", content, 0o600)
}

func TestFinalizationObjectsSurviveWorkRemovalAndRemainReadOnly(t *testing.T) {
	bundle, path, record, entry := frozenFinalization(t)
	names, err := os.ReadDir(filepath.Join(path, "finalization/objects"))
	if err != nil || len(names) != 1 || names[0].Name() != entry.SHA256 {
		t.Fatal("equal content was not retained once", err)
	}
	if err := os.RemoveAll(filepath.Join(path, "work")); err != nil {
		t.Fatal(err)
	}
	file, actual, err := OpenFinalizationObject(bundle, record, entry.SHA256)
	if err != nil || actual != entry {
		t.Fatal("frozen data depended on work", err)
	}
	defer file.Close()
	if _, err := file.Write([]byte("overwrite")); err == nil {
		t.Fatal("frozen descriptor is writable")
	}
	content, err := io.ReadAll(file)
	if err != nil || VerifyContent(entry, content) != nil {
		t.Fatal("frozen bytes changed", err)
	}
	manifest, received, err := OpenFinalizationManifest(bundle, record.Binding)
	if err != nil || received != record {
		t.Fatal("manifest changed after work removal", err)
	}
	_ = manifest.Close()
}

func TestFinalizationObjectsNeverFallbackAfterCorruption(t *testing.T) {
	for _, corrupt := range []string{"absent", "symlink", "hardlink", "directory", "permissions", "objects_link", "objects_absent", "foreign_digest", "foreign_epoch", "changed_unclean"} {
		t.Run(corrupt, func(t *testing.T) {
			bundle, path, record, entry := frozenFinalization(t)
			object := filepath.Join(path, "finalization/objects", entry.SHA256)
			digest := entry.SHA256
			switch corrupt {
			case "absent", "symlink", "directory":
				if err := os.Remove(object); err != nil {
					t.Fatal(err)
				}
				if corrupt == "symlink" {
					if err := os.Symlink(filepath.Join(path, "work/a"), object); err != nil {
						t.Fatal(err)
					}
				} else if corrupt == "directory" {
					if err := os.Mkdir(object, 0o700); err != nil {
						t.Fatal(err)
					}
				}
			case "hardlink":
				if err := os.Link(object, filepath.Join(path, "another-object")); err != nil {
					t.Fatal(err)
				}
			case "permissions":
				if err := os.Chmod(object, 0o644); err != nil {
					t.Fatal(err)
				}
			case "objects_link", "objects_absent":
				objects := filepath.Dir(object)
				if err := os.Rename(objects, objects+"-original"); err != nil {
					t.Fatal(err)
				}
				if corrupt == "objects_link" {
					if err := os.Symlink("objects-original", objects); err != nil {
						t.Fatal(err)
					}
				}
			case "foreign_digest":
				digest = record.TreeDigest
			case "foreign_epoch":
				record.Binding.DirectoryEpoch++
			case "changed_unclean":
				record.Unclean = true
			}
			if file, _, err := OpenFinalizationObject(bundle, record, digest); err == nil {
				_ = file.Close()
				t.Fatal("unsafe object or foreign input accepted")
			}
			if content, err := os.ReadFile(filepath.Join(path, "work/a")); err != nil || string(content) != "frozen\x00\xff" {
				t.Fatal("failed transfer changed original work", err)
			}
		})
	}
}

func TestLegacyFinalizationUpgradeRecoversWithoutRecapture(t *testing.T) {
	for _, phase := range []string{"before_objects", "after_objects", "changed_work", "corrupt_objects"} {
		t.Run(phase, func(t *testing.T) {
			bundle, path, record, entry := frozenFinalization(t)
			record.ObjectsVersion = 0
			data, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(path, "finalization/record.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			if phase == "before_objects" || phase == "changed_work" {
				if err := os.RemoveAll(filepath.Join(path, "finalization/objects")); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "changed_work" {
				if err := os.WriteFile(filepath.Join(path, "work/a"), []byte("changed"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "corrupt_objects" {
				if err := os.WriteFile(filepath.Join(path, "finalization/objects", entry.SHA256), []byte("changed"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "after_objects" {
				if err := os.RemoveAll(filepath.Join(path, "work")); err != nil {
					t.Fatal(err)
				}
			}
			if file, _, err := OpenFinalizationManifest(bundle, record.Binding); err == nil {
				_ = file.Close()
				t.Fatal("read-only transfer silently upgraded a legacy journal")
			}
			upgraded, err := FinalizeWorkTree(context.Background(), bundle, record.Binding, true)
			if phase == "changed_work" || phase == "corrupt_objects" {
				if err == nil {
					t.Fatal("legacy input was recaptured or repaired")
				}
				retained, err := ReadFinalization(bundle, record.Binding)
				if err != nil || retained != record {
					t.Fatal("failed upgrade changed original record", err)
				}
				return
			}
			expected := record
			expected.ObjectsVersion = 1
			if err != nil || upgraded != expected {
				t.Fatal("upgrade changed input or termination classification", err)
			}
			file, _, err := OpenFinalizationObject(bundle, upgraded, entry.SHA256)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			content, err := io.ReadAll(file)
			if err != nil || VerifyContent(entry, content) != nil {
				t.Fatal("upgraded bytes differ", err)
			}
		})
	}
}

func TestFinalizationReserveFailurePreservesWorkAndTermination(t *testing.T) {
	bundle, path, binding := sealedBundle(t)
	if err := os.WriteFile(filepath.Join(path, "work/retained"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := FinalizeWorkTreeWithPolicy(context.Background(), bundle, binding, true, CopyPolicy{MinimumFreeBytes: math.MaxUint64}); err == nil {
		t.Fatal("finalization consumed the disk reserve")
	}
	if _, err := os.Stat(filepath.Join(path, "finalization")); !os.IsNotExist(err) {
		t.Fatal("failed freeze published a journal", err)
	}
	record, err := FinalizeWorkTree(context.Background(), bundle, binding, false)
	if err != nil || !record.Unclean || record.ObjectsVersion != 1 {
		t.Fatal("retry lost original termination evidence", err)
	}
}
