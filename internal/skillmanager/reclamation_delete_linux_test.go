package skillmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func reclamationQuiescent(ctx context.Context) error { return ctx.Err() }

func TestReclamationExecutorDeletesExactRootsAndRetainsAudit(t *testing.T) {
	bundle, capture, authority := reclamationBundle(t)
	intent := markReclamation(t, bundle, capture, authority)
	for range 2 {
		if err := ReclaimFinalizationContent(context.Background(), bundle, intent, reclamationQuiescent); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"work", "finalization/objects"} {
		if _, err := bundle.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("content root survived", name, err)
		}
	}
	if retained, complete, err := ReadFinalizationReclamation(bundle, capture.Binding); err != nil || !complete || retained == nil {
		t.Fatal("missing complete audit", err)
	}
	if _, err := ReadFinalizationAcknowledgement(bundle, capture.Binding); err != nil {
		t.Fatal(err)
	}
	if _, _, err := OpenFinalizationManifest(bundle, capture.Binding); !errors.Is(err, ErrFinalizationReclaimed) {
		t.Fatal("reclaimed input remained exportable", err)
	}
}

func TestReclamationExecutorResumesAfterActualPartialDeletion(t *testing.T) {
	for _, point := range []string{"work_file", "work_root", "object_file"} {
		t.Run(point, func(t *testing.T) {
			bundle, capture, authority := reclamationBundle(t)
			intent := markReclamation(t, bundle, capture, authority)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			verify := func(ctx context.Context) error {
				calls++
				path := "work/learning"
				if point == "work_root" {
					path = "work"
				}
				if point == "object_file" {
					matches, err := filepath.Glob(filepath.Join(bundle.Name(), "finalization/objects/*"))
					if err != nil {
						return err
					}
					if len(matches) == 0 {
						cancel()
					}
				} else if _, err := bundle.Lstat(path); errors.Is(err, os.ErrNotExist) {
					cancel()
				}
				return ctx.Err()
			}
			if err := ReclaimFinalizationContent(ctx, bundle, intent, verify); !errors.Is(err, context.Canceled) {
				t.Fatal("deletion ignored cancellation", calls, err)
			}
			if _, complete, err := ReadFinalizationReclamation(bundle, capture.Binding); err != nil || complete {
				t.Fatal("partial deletion falsely completed", err)
			}
			reopened, err := os.OpenRoot(bundle.Name())
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if err := ReclaimFinalizationContent(context.Background(), reopened, intent, reclamationQuiescent); err != nil {
				t.Fatal("restart could not resume original deletion", err)
			}
			if _, complete, err := ReadFinalizationReclamation(reopened, capture.Binding); err != nil || !complete {
				t.Fatal("resumed deletion did not complete", err)
			}
		})
	}
}

func TestReclamationExecutorPreflightPreservesUnexpectedOrChangedContent(t *testing.T) {
	for _, kind := range []string{"new_work", "changed_work", "changed_mode", "work_link", "special_file", "extra_object", "changed_object", "object_hardlink", "no_proof", "active_writer", "wrong_intent"} {
		t.Run(kind, func(t *testing.T) {
			bundle, capture, authority := reclamationBundle(t)
			intent := markReclamation(t, bundle, capture, authority)
			verify := reclamationQuiescent
			var err error
			work := filepath.Join(bundle.Name(), "work/learning")
			objects, err := filepath.Glob(filepath.Join(bundle.Name(), "finalization/objects/*"))
			if err != nil || len(objects) != 1 {
				t.Fatal("invalid fixture", err)
			}
			switch kind {
			case "new_work":
				err = os.WriteFile(filepath.Join(bundle.Name(), "work/new"), []byte("unique"), 0600)
			case "changed_work":
				err = os.WriteFile(work, []byte("modified"), 0600)
			case "changed_mode":
				err = os.Chmod(work, 0644)
			case "work_link":
				if err = os.Rename(work, work+".preserved"); err == nil {
					err = os.Symlink("learning.preserved", work)
				}
			case "special_file":
				if err = os.Remove(work); err == nil {
					err = unix.Mkfifo(work, 0600)
				}
			case "extra_object":
				err = os.WriteFile(filepath.Join(bundle.Name(), "finalization/objects/extra"), []byte("unique"), 0600)
			case "changed_object":
				err = os.WriteFile(objects[0], []byte("modified"), 0600)
			case "object_hardlink":
				err = os.Link(objects[0], filepath.Join(bundle.Name(), "object-alias"))
			case "no_proof":
				verify = nil
			case "active_writer":
				verify = func(context.Context) error { return errors.New("writers remain") }
			case "wrong_intent":
				intent.SessionRoot += "-replacement"
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := ReclaimFinalizationContent(context.Background(), bundle, intent, verify); err == nil {
				t.Fatal("unsafe content was deleted")
			}
			if _, err := os.Lstat(work); err != nil {
				t.Fatal("preflight failure deleted work", err)
			}
			if _, err := os.Lstat(objects[0]); err != nil {
				t.Fatal("preflight failure deleted objects", err)
			}
			if _, err := bundle.Lstat("finalization/reclaimed.json"); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed deletion claimed completion", err)
			}
		})
	}
}

func TestReclamationExecutorRechecksEntryAfterIndependentProof(t *testing.T) {
	bundle, capture, authority := reclamationBundle(t)
	intent := markReclamation(t, bundle, capture, authority)
	calls := 0
	verify := func(context.Context) error {
		calls++
		// Initial proof, root proof, directory proof, then the first file's unlink proof.
		if calls == 4 {
			return os.WriteFile(filepath.Join(bundle.Name(), "work/learning"), []byte("modified"), 0600)
		}
		return nil
	}
	if err := ReclaimFinalizationContent(context.Background(), bundle, intent, verify); err == nil {
		t.Fatal("file replacement between verification and unlink was accepted")
	}
	if data, err := os.ReadFile(filepath.Join(bundle.Name(), "work/learning")); err != nil || string(data) != "modified" {
		t.Fatal("changed bytes were removed", err)
	}
}

func TestReclamationExecutorHandlesNestedNormalizedAndLinkedContent(t *testing.T) {
	store, _ := privateStore(t)
	content := []byte("retained data")
	entry := objectEntry("nested/data", content, 0444)
	source := Manifest{Version: 1, Entries: []Entry{
		{Path: "link", Kind: "symlink", Mode: 0777, Target: "nested/data"},
		{Path: "nested", Kind: "directory", Mode: 0555}, entry,
		{Path: "nested/empty", Kind: "directory", Mode: 0555},
	}}
	options := runtimeOptions()
	receipt := sessionReceipt(t, source, options)
	if err := PrepareSessionSnapshot(context.Background(), store, source, objectSource(map[string][]byte{entry.SHA256: content}), receipt, options); err != nil {
		t.Fatal(err)
	}
	bundle, _, err := OpenSessionSnapshot(store, receipt.Snapshot.Binding.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	bundle, capture, authority := freezeReclamationBundle(t, bundle, receipt.Snapshot.Binding)
	if capture.TreeDigest != capture.Binding.InitialTreeDigest {
		t.Fatal("fixture introduced a permission edit")
	}
	intent := markReclamation(t, bundle, capture, authority)
	if err := ReclaimFinalizationContent(context.Background(), bundle, intent, reclamationQuiescent); err != nil {
		t.Fatal("normalization or nested links prevented exact deletion", err)
	}
	retained, _, err := OpenSessionSnapshot(store, receipt.Snapshot.Binding.SessionID)
	if err != nil {
		t.Fatal("deletion lost session authority", err)
	}
	_ = retained.Close()
}

func TestReclamationExecutorRejectsRealBindMounts(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_RUN_RECLAMATION_MOUNT_TEST") != "1" {
		t.Skip("requires isolated Linux mount namespace")
	}
	for _, name := range []string{"work", "work/learning", "finalization/objects"} {
		t.Run(name, func(t *testing.T) {
			bundle, capture, authority := reclamationBundle(t)
			intent := markReclamation(t, bundle, capture, authority)
			target := filepath.Join(bundle.Name(), name)
			if err := unix.Mount(target, target, "", unix.MS_BIND, ""); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := unix.Unmount(target, 0); err != nil {
					t.Error(err)
				}
			}()
			if err := ReclaimFinalizationContent(context.Background(), bundle, intent, reclamationQuiescent); err == nil {
				t.Fatal("same-inode bind mount was traversed for deletion")
			}
			if data, err := os.ReadFile(filepath.Join(bundle.Name(), "work/learning")); err != nil || string(data) != "original" {
				t.Fatal("mount refusal lost original work", err)
			}
		})
	}
}

func TestReclamationExecutorPreservesRootReplacedBeforeUnlink(t *testing.T) {
	bundle, capture, authority := reclamationBundle(t)
	intent := markReclamation(t, bundle, capture, authority)
	calls := 0
	verify := func(context.Context) error {
		calls++
		if calls != 4 {
			return nil
		}
		if err := bundle.Rename("work", "preserved-work"); err != nil {
			return err
		}
		return bundle.Mkdir("work", 0700)
	}
	if err := ReclaimFinalizationContent(context.Background(), bundle, intent, verify); err == nil {
		t.Fatal("replaced root was deleted through an old descriptor")
	}
	if data, err := os.ReadFile(filepath.Join(bundle.Name(), "preserved-work/learning")); err != nil || string(data) != "original" {
		t.Fatal("renamed work was modified", err)
	}
}
