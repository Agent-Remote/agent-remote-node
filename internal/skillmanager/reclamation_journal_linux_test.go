package skillmanager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func reclamationBundle(t *testing.T, exclusions ...string) (*os.Root, FinalizationRecord, ReclamationAuthorization) {
	t.Helper()
	bundle, path, binding := sealedBundle(t)
	if len(exclusions) != 0 {
		snapshot, err := loadPreparedSnapshot(bundle, binding)
		if err != nil {
			t.Fatal(err)
		}
		snapshot.Capture.SystemPaths = exclusions
		data, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "snapshot.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(path, "work/learning"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	return freezeReclamationBundle(t, bundle, binding)
}

func freezeReclamationBundle(t *testing.T, bundle *os.Root, binding SnapshotBinding, target ...string) (*os.Root, FinalizationRecord, ReclamationAuthorization) {
	t.Helper()
	capture, err := FinalizeWorkTree(context.Background(), bundle, binding, false)
	if err != nil {
		t.Fatal(err)
	}
	status := "published"
	if len(target) != 0 {
		status = target[0]
	}
	ack := finalizationAckFixture(capture, status)
	capture, err = AcknowledgeFinalization(context.Background(), bundle, ack)
	if err != nil {
		t.Fatal(err)
	}
	if err := RetainFinalizationRuntimeCleanup(bundle, capture, "/runtime/original"); err != nil {
		t.Fatal(err)
	}
	verified := time.Now()
	authority := ReclamationAuthorization{Version: 1, RequestID: binding.SnapshotID,
		NodeID: binding.NodeID, UserID: binding.UserID, AccountID: binding.AccountID,
		SessionID: binding.SessionID, SnapshotID: binding.SnapshotID, FinalizationID: ack.Receipt.ID,
		CheckpointID: *ack.Receipt.CheckpointID, TreeDigest: capture.TreeDigest,
		PublicationID: ack.Publication.ID, PublicationAttempt: ack.Publication.Attempt,
		PublicationStatus: ack.Publication.Status, VerifiedAt: verified, ExpiresAt: verified.Add(time.Minute)}
	return bundle, capture, authority
}

func markReclamation(t *testing.T, bundle *os.Root, capture FinalizationRecord, authority ReclamationAuthorization) FinalizationReclamation {
	t.Helper()
	intent, err := MarkFinalizationReclamation(context.Background(), bundle, capture, authority, time.Now().Add(time.Minute), "/runtime/original")
	if err != nil {
		t.Fatal(err)
	}
	return intent
}

func TestReclamationJournalRetainsAuditAcrossPartialDeletionAndReopen(t *testing.T) {
	bundle, capture, authority := reclamationBundle(t)
	if intent, done, err := ReadFinalizationReclamation(bundle, capture.Binding); err != nil || intent != nil || done {
		t.Fatal("unmarked content was reclaimed", err)
	}
	intent := markReclamation(t, bundle, capture, authority)
	if err := RetainFinalizationReclaimed(context.Background(), bundle, intent); err == nil {
		t.Fatal("completion accepted existing content")
	}
	if _, err := os.Stat(filepath.Join(bundle.Name(), "work/learning")); err != nil {
		t.Fatal("marking deleted original work", err)
	}
	file, _, err := OpenFinalizationManifest(bundle, capture.Binding)
	if file != nil || !errors.Is(err, ErrFinalizationReclaiming) {
		t.Fatal("marked input remained transferable", err)
	}
	if _, err := FinalizeWorkTree(context.Background(), bundle, capture.Binding, true); !errors.Is(err, ErrFinalizationReclaiming) {
		t.Fatal("marked input was recaptured", err)
	}
	if _, err := OpenSessionWork(bundle, RuntimeBinding{UID: 1000, GID: 1000}); !errors.Is(err, ErrFinalizationReclaiming) {
		t.Fatal("marked work remained mountable", err)
	}
	// Simulate a deletion interrupted between roots; no production deletion API is involved.
	if err := bundle.RemoveAll("finalization/objects"); err != nil {
		t.Fatal(err)
	}
	reopened, err := os.OpenRoot(bundle.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if record, err := ReadFinalization(reopened, capture.Binding); err != nil || record != capture {
		t.Fatal("partial deletion lost original audit record", err)
	}
	if saved, done, err := ReadFinalizationReclamation(reopened, capture.Binding); err != nil || done || saved == nil || !sameReclamationIntent(*saved, intent) {
		t.Fatal("restart lost immutable deletion intent", err)
	}
	// A replay only re-syncs original intent; it does not mint a fresh HTTP authorization.
	if replay, err := MarkFinalizationReclamation(context.Background(), reopened, capture, authority, time.Time{}, intent.SessionRoot); err != nil || !sameReclamationIntent(replay, intent) {
		t.Fatal("exact intent replay depended on an expired HTTP budget", err)
	}
	if err := reopened.RemoveAll("work"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := RetainFinalizationReclaimed(context.Background(), reopened, intent); err != nil {
			t.Fatal("completion could not be durably replayed", err)
		}
	}
	if saved, done, err := ReadFinalizationReclamation(reopened, capture.Binding); err != nil || !done || saved == nil {
		t.Fatal("completed deletion disappeared", err)
	}
	if _, _, err := OpenFinalizationManifest(reopened, capture.Binding); !errors.Is(err, ErrFinalizationReclaimed) {
		t.Fatal("completed deletion exported an empty tree", err)
	}
	if _, err := ReadFinalizationAcknowledgement(reopened, capture.Binding); err != nil {
		t.Fatal("reclamation erased the Server receipts", err)
	}
	for _, name := range []string{"snapshot.json", "baseline.json", "termination.json", "finalization/record.json", "finalization/manifest.json", "finalization/runtime-cleanup.json"} {
		if _, err := reopened.Stat(name); err != nil {
			t.Fatal("reclamation lost audit metadata", name, err)
		}
	}
	if err := reopened.Mkdir("work", 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFinalization(reopened, capture.Binding); err == nil {
		t.Fatal("completed reclamation adopted a reappearing work directory")
	}
}

func TestReclamationMarkPreservesUnacknowledgedOrChangedWork(t *testing.T) {
	for _, mode := range []string{"changed", "extra", "missing_work", "work_link", "ack_absent", "cleanup_absent", "cleanup_root", "authority", "expired", "wall_clock", "long_budget", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			bundle, capture, authority := reclamationBundle(t)
			deadline := time.Now().Add(time.Minute)
			root := "/runtime/original"
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var err error
			switch mode {
			case "changed":
				err = os.WriteFile(filepath.Join(bundle.Name(), "work/learning"), []byte("modified"), 0600)
			case "extra":
				err = os.WriteFile(filepath.Join(bundle.Name(), "work/new"), []byte("unique"), 0600)
			case "missing_work":
				err = bundle.RemoveAll("work")
			case "work_link":
				if err = bundle.Rename("work", "original-work"); err == nil {
					err = bundle.Symlink("original-work", "work")
				}
			case "ack_absent":
				err = bundle.Remove("finalization/acknowledgement.json")
			case "cleanup_absent":
				err = bundle.Remove("finalization/runtime-cleanup.json")
			case "cleanup_root":
				root += "-replacement"
			case "authority":
				authority.CheckpointID = capture.Binding.NodeID
			case "expired":
				deadline = time.Now().Add(-time.Second)
			case "wall_clock":
				deadline = deadline.Round(0)
			case "long_budget":
				deadline = time.Now().Add(time.Hour)
			case "cancelled":
				cancel()
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := MarkFinalizationReclamation(ctx, bundle, capture, authority, deadline, root); err == nil {
				t.Fatal("unsafe initial reclamation was marked")
			}
			if _, err := bundle.Lstat("finalization/reclamation.json"); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed marking left deletion authority", err)
			}
			file, _, err := OpenFinalizationManifest(bundle, capture.Binding)
			if err != nil {
				t.Fatal("failed marking damaged the frozen input", err)
			}
			_ = file.Close()
		})
	}
}

func TestReclamationJournalRejectsDamagedIntentAndReplacedRoots(t *testing.T) {
	for _, mode := range []string{"work", "objects", "intent_symlink", "intent_hardlink", "intent_mode", "objects_mode", "missing_field", "changed_binding", "missing_cleanup", "orphan_completion", "changed_completion"} {
		t.Run(mode, func(t *testing.T) {
			bundle, capture, authority := reclamationBundle(t)
			intent := markReclamation(t, bundle, capture, authority)
			name := filepath.Join(bundle.Name(), "finalization/reclamation.json")
			var err error
			switch mode {
			case "work", "objects":
				root := "work"
				if mode == "objects" {
					root = "finalization/objects"
				}
				if err = bundle.Rename(root, root+"-retained"); err == nil {
					err = bundle.Mkdir(root, 0700)
				}
			case "intent_symlink":
				if err = os.Rename(name, name+".original"); err == nil {
					err = os.Symlink("reclamation.json.original", name)
				}
			case "intent_hardlink":
				err = os.Link(name, name+".link")
			case "intent_mode":
				err = os.Chmod(name, 0644)
			case "objects_mode":
				err = os.Chmod(filepath.Join(bundle.Name(), "finalization/objects"), 0777)
			case "missing_field":
				data, readErr := os.ReadFile(name)
				if readErr != nil {
					t.Fatal(readErr)
				}
				var fields map[string]json.RawMessage
				if err = json.Unmarshal(data, &fields); err == nil {
					delete(fields, "work")
					data, err = json.Marshal(fields)
				}
				if err == nil {
					err = os.WriteFile(name, data, 0600)
				}
			case "changed_binding":
				intent.Capture.Binding.DirectoryEpoch++
				data, _ := json.Marshal(intent)
				err = os.WriteFile(name, data, 0600)
			case "missing_cleanup":
				err = bundle.Remove("finalization/runtime-cleanup.json")
			case "orphan_completion":
				err = os.Rename(name, filepath.Join(bundle.Name(), "finalization/reclaimed.json"))
			case "changed_completion":
				intent.SessionRoot += "-replacement"
				data, _ := json.Marshal(intent)
				err = os.WriteFile(filepath.Join(bundle.Name(), "finalization/reclaimed.json"), data, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ReadFinalization(bundle, capture.Binding); err == nil {
				t.Fatal("damaged reclamation was presented as normal input")
			}
			if _, _, err := OpenFinalizationManifest(bundle, capture.Binding); err == nil {
				t.Fatal("damaged reclamation remained transferable")
			}
			if err := RetainFinalizationReclaimed(context.Background(), bundle, intent); err == nil {
				t.Fatal("damaged reclamation was completed")
			}
		})
	}
}

func TestOpenSessionWorkRejectsInBundleLeafSymlink(t *testing.T) {
	bundle, _, _ := sealedBundle(t)
	if err := bundle.Rename("work", "other-work"); err != nil {
		t.Fatal(err)
	}
	if err := bundle.Symlink("other-work", "work"); err != nil {
		t.Fatal(err)
	}
	options := runtimeOptions()
	file, err := OpenSessionWork(bundle, RuntimeBinding{UID: options.UID, GID: options.GID})
	if file != nil {
		_ = file.Close()
	}
	if err == nil {
		t.Fatal("os.Root resolution followed a substituted work symlink")
	}
}

func TestReclamationPreservesUnrecordedSystemPathContent(t *testing.T) {
	for _, mode := range []string{"absent", "empty", "file", "symlink", "nonempty"} {
		t.Run(mode, func(t *testing.T) {
			bundle, capture, authority := reclamationBundle(t, "ego-browser")
			root := filepath.Join(bundle.Name(), "work/ego-browser")
			var err error
			switch mode {
			case "empty", "nonempty":
				err = os.Mkdir(root, 0700)
				if err == nil && mode == "nonempty" {
					err = os.WriteFile(filepath.Join(root, "unique"), []byte("not captured"), 0600)
				}
			case "file":
				err = os.WriteFile(root, []byte("not captured"), 0600)
			case "symlink":
				err = os.Symlink("learning", root)
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = MarkFinalizationReclamation(context.Background(), bundle, capture, authority, time.Now().Add(time.Minute), "/runtime/original")
			if (err == nil) != (mode == "absent" || mode == "empty") {
				t.Fatal("unrecorded excluded content could be lost", err)
			}
			if mode == "nonempty" {
				if data, err := os.ReadFile(filepath.Join(root, "unique")); err != nil || string(data) != "not captured" {
					t.Fatal("unrecorded data changed", err)
				}
			}
		})
	}
}

func TestReclamationRetainsUnresolvedConflictUntilLaterPublication(t *testing.T) {
	bundle, _, binding := sealedBundle(t)
	bundle, capture, authority := freezeReclamationBundle(t, bundle, binding, "conflicted")
	if _, err := MarkFinalizationReclamation(context.Background(), bundle, capture, authority, time.Now().Add(time.Minute), "/runtime/original"); err == nil {
		t.Fatal("unresolved conflict was marked for deletion")
	}
	if _, err := bundle.Lstat("finalization/reclamation.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("conflict refusal left intent", err)
	}
	authority.PublicationID = binding.NodeID
	authority.PublicationAttempt++
	authority.PublicationStatus = "published"
	intent := markReclamation(t, bundle, capture, authority)
	if err := ReclaimFinalizationContent(context.Background(), bundle, intent, reclamationQuiescent); err != nil {
		t.Fatal("later resolved original input could not be reclaimed", err)
	}
}
