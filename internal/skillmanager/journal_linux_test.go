package skillmanager

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func sealedBundle(t *testing.T) (*os.Root, string, SnapshotBinding) {
	t.Helper()
	store, path := privateStore(t)
	source := Manifest{Version: 1}
	if _, err := Materialize(context.Background(), store, "copy", source, objectSource(nil), runtimeOptions()); err != nil {
		t.Fatal(err)
	}
	root, err := store.OpenRoot("copy")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	digest, err := Digest(source)
	if err != nil {
		t.Fatal(err)
	}
	binding := SnapshotBinding{
		UserID: "11111111-1111-4111-8111-111111111111", AccountID: "22222222-2222-4222-8222-222222222222",
		NodeID: "33333333-3333-4333-8333-333333333333", SessionID: "44444444-4444-4444-8444-444444444444",
		SnapshotID: "55555555-5555-4555-8555-555555555555", DirectoryEpoch: 1, LibraryGeneration: 2, InitialTreeDigest: digest,
	}
	if err := SealPreparedSnapshot(root, PreparedSnapshot{Version: 1, Binding: binding, Capture: DefaultCaptureOptions()}); err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(path, "copy"), binding
}

func TestFinalizationSurvivesReopenAndRetriesWithoutRecapture(t *testing.T) {
	root, path, binding := sealedBundle(t)
	if err := os.WriteFile(filepath.Join(path, "work/learned"), []byte("saved learning"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := FinalizeWorkTree(context.Background(), root, binding, false)
	if err != nil {
		t.Fatal(err)
	}
	if first.State != "local_durable" || first.Unclean || first.CanDeleteSession() {
		t.Fatal("local snapshot falsely claims remote persistence")
	}
	reopened, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	// The retry must use the durable receipt, even if the transient runtime spec is already gone.
	if err := os.RemoveAll(filepath.Join(path, "work")); err != nil {
		t.Fatal(err)
	}
	second, err := FinalizeWorkTree(context.Background(), reopened, binding, true)
	if err != nil || first != second {
		t.Fatalf("retry recaptured or reclassified the snapshot: %v", err)
	}
}

func TestFinalizationStateRequiresPersistenceBeforeTerminalPublication(t *testing.T) {
	root, _, binding := sealedBundle(t)
	if _, err := FinalizeWorkTree(context.Background(), root, binding, false); err != nil {
		t.Fatal(err)
	}
	if _, err := AdvanceFinalization(root, binding, "local_durable", "published"); err == nil {
		t.Fatal("local bytes were treated as published")
	}
	for _, transition := range [][2]string{{"local_durable", "upload_pending"}, {"upload_pending", "persisted"}, {"persisted", "conflicted"}} {
		record, err := AdvanceFinalization(root, binding, transition[0], transition[1])
		if err != nil {
			t.Fatal(err)
		}
		if record.CanDeleteSession() != (transition[1] == "conflicted") {
			t.Fatal("session delete guard confuses pending and Server-retained state")
		}
		duplicate, err := AdvanceFinalization(root, binding, transition[0], transition[1])
		if err != nil || record != duplicate {
			t.Fatalf("duplicate acknowledgement is not idempotent: %v", err)
		}
	}
	if _, err := AdvanceFinalization(root, binding, "conflicted", "upload_pending"); err == nil {
		t.Fatal("terminal journal regressed")
	}
}

func TestUncleanRecoveryCanOnlyPersistAndDetach(t *testing.T) {
	root, _, binding := sealedBundle(t)
	if _, err := FinalizeWorkTree(context.Background(), root, binding, true); err != nil {
		t.Fatal(err)
	}
	if _, err := AdvanceFinalization(root, binding, "local_durable", "upload_pending"); err != nil {
		t.Fatal(err)
	}
	if _, err := AdvanceFinalization(root, binding, "upload_pending", "persisted"); err == nil {
		t.Fatal("unclean snapshot lost its classification")
	}
	if _, err := AdvanceFinalization(root, binding, "upload_pending", "persisted_unclean"); err != nil {
		t.Fatal(err)
	}
	if _, err := AdvanceFinalization(root, binding, "persisted_unclean", "published"); err == nil {
		t.Fatal("unclean recovery automatically published")
	}
	record, err := AdvanceFinalization(root, binding, "persisted_unclean", "detached")
	if err != nil || !record.CanDeleteSession() {
		t.Fatalf("retained detached snapshot blocks session view deletion: %v", err)
	}
}

func TestSnapshotBindingCannotChangeOwnerEpochOrStartingTree(t *testing.T) {
	root, _, binding := sealedBundle(t)
	original := PreparedSnapshot{Version: 1, Binding: binding, Capture: DefaultCaptureOptions()}
	if err := SealPreparedSnapshot(root, original); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*SnapshotBinding){
		func(value *SnapshotBinding) { value.UserID = value.NodeID },
		func(value *SnapshotBinding) { value.AccountID = value.NodeID },
		func(value *SnapshotBinding) { value.DirectoryEpoch++ },
		func(value *SnapshotBinding) {
			value.InitialTreeDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		},
	} {
		changed := binding
		mutate(&changed)
		if err := SealPreparedSnapshot(root, PreparedSnapshot{Version: 1, Binding: changed, Capture: DefaultCaptureOptions()}); err == nil {
			t.Fatal("prepared work rebound to another snapshot")
		}
		if _, err := FinalizeWorkTree(context.Background(), root, changed, false); err == nil {
			t.Fatal("mismatched finalization binding accepted")
		}
	}
}

func TestCorruptJournalDoesNotRecaptureOrRemoveWork(t *testing.T) {
	root, path, binding := sealedBundle(t)
	if err := os.WriteFile(filepath.Join(path, "work/state"), []byte("retain me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := FinalizeWorkTree(context.Background(), root, binding, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "finalization/manifest.json"), []byte(`{"version":1,"entries":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFinalization(root, binding); err == nil {
		t.Fatal("journal integrity mismatch ignored")
	}
	if _, err := FinalizeWorkTree(context.Background(), root, binding, false); err == nil {
		t.Fatal("corrupt journal silently recaptured")
	}
	if err := os.Remove(filepath.Join(path, "finalization/record.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := FinalizeWorkTree(context.Background(), root, binding, false); err == nil {
		t.Fatal("incomplete journal silently replaced")
	}
	data, err := os.ReadFile(filepath.Join(path, "work/state"))
	if err != nil || string(data) != "retain me" {
		t.Fatalf("journal failure lost runtime state: %v", err)
	}
}

func TestFailedCaptureNeverPublishesFinalizationJournal(t *testing.T) {
	root, path, binding := sealedBundle(t)
	if err := os.Symlink("/etc/shadow", filepath.Join(path, "work/external")); err != nil {
		t.Fatal(err)
	}
	if _, err := FinalizeWorkTree(context.Background(), root, binding, false); err == nil {
		t.Fatal("unportable tree finalized")
	}
	if _, err := root.Lstat("finalization"); !os.IsNotExist(err) {
		t.Fatal("failed capture exposed a finalization journal")
	}
	if _, err := root.Lstat("work/external"); err != nil {
		t.Fatal("failed capture removed recoverable input")
	}
}

func TestSealingRequiresThePreparedRuntimeDependencyMapping(t *testing.T) {
	_, _, binding := sealedBundle(t)
	store, _ := privateStore(t)
	source := Manifest{Version: 1, Entries: []Entry{{Path: "python", Kind: "runtime_link", Mode: 0o777, Target: "/usr/bin/python3", Dependency: "python3"}}}
	options := runtimeOptions()
	options.RuntimeDependencies = map[string]string{"python3": "/usr/bin/python3"}
	if _, err := Materialize(context.Background(), store, "copy", source, objectSource(nil), options); err != nil {
		t.Fatal(err)
	}
	bundle, err := store.OpenRoot("copy")
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	binding.InitialTreeDigest, err = Digest(source)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := PreparedSnapshot{Version: 1, Binding: binding, Capture: DefaultCaptureOptions()}
	if err := SealPreparedSnapshot(bundle, snapshot); err == nil {
		t.Fatal("prepared interpreter loses its required runtime dependency mapping")
	}
	snapshot.Capture.RuntimeDependencies = options.RuntimeDependencies
	if err := SealPreparedSnapshot(bundle, snapshot); err != nil {
		t.Fatal(err)
	}
}

func TestTerminationClassificationSurvivesCaptureFailure(t *testing.T) {
	for _, unclean := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean", true: "unclean"}[unclean], func(t *testing.T) {
			bundle, _, binding := sealedBundle(t)
			if err := bundle.Symlink("/outside", "work/nonportable"); err != nil {
				t.Fatal(err)
			}
			if _, err := FinalizeWorkTree(context.Background(), bundle, binding, unclean); err == nil {
				t.Fatal("nonportable snapshot unexpectedly finalized")
			}
			if record, err := ReadTermination(bundle, binding); err != nil || record.Unclean != unclean {
				t.Fatalf("capture failure lost original exit evidence: %#v, %v", record, err)
			}
			if err := bundle.Remove("work/nonportable"); err != nil {
				t.Fatal(err)
			}
			record, err := FinalizeWorkTree(context.Background(), bundle, binding, !unclean)
			if err != nil || record.Unclean != unclean {
				t.Fatalf("retry changed original exit evidence: %#v, %v", record, err)
			}
		})
	}
}
