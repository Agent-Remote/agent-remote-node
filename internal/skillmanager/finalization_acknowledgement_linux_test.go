package skillmanager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func frozenAckBundle(t *testing.T, unclean bool) (*os.Root, string, FinalizationRecord) {
	t.Helper()
	bundle, path, binding := sealedBundle(t)
	record, err := FinalizeWorkTree(context.Background(), bundle, binding, unclean)
	if err != nil {
		t.Fatal(err)
	}
	return bundle, path, record
}

func TestFinalizationAcknowledgementRetainsExactReceiptsBeforeTerminalState(t *testing.T) {
	for _, target := range []string{"published", "conflicted", "detached", "superseded", "unclean"} {
		t.Run(target, func(t *testing.T) {
			bundle, _, capture := frozenAckBundle(t, target == "unclean")
			if target == "unclean" {
				target = "detached"
			}
			for _, phase := range []string{"upload_pending", "persisted", target} {
				ack := finalizationAckFixture(capture, phase)
				got, err := AcknowledgeFinalization(context.Background(), bundle, ack)
				state, _ := ack.State()
				if err != nil || got.State != state || !SameFinalizationInput(got, capture) {
					t.Fatal("wrong acknowledged state", got.State, err)
				}
				saved, err := ReadFinalizationAcknowledgement(bundle, capture.Binding)
				if err != nil || !reflect.DeepEqual(saved, ack) {
					t.Fatal("lost exact receipt", err)
				}
				replay, err := AcknowledgeFinalization(context.Background(), bundle, ack)
				if err != nil || replay != got {
					t.Fatal("exact replay failed", err)
				}
			}
			record, err := ReadFinalization(bundle, capture.Binding)
			if err != nil || record.CanDeleteSession() != (target != "superseded") {
				t.Fatal("incorrect terminal retention", err)
			}
			if record.CanDeleteSession() {
				root := "/var/lib/agent-remote-runtime/sessions/" + capture.Binding.SessionID
				if err := RetainFinalizationRuntimeCleanup(bundle, record, root); err != nil {
					t.Fatal(err)
				}
				if cleaned, err := FinalizationRuntimeCleaned(bundle, record, root); err != nil || !cleaned {
					t.Fatal("cleanup completion lost", err)
				}
				if _, err := FinalizationRuntimeCleaned(bundle, record, root+"-other"); err == nil {
					t.Fatal("cleanup changed runtime root")
				}
			}
		})
	}
}

func TestFinalizationAcknowledgementResumesInterruptedStateAdvancement(t *testing.T) {
	for _, phase := range []string{"local_durable", "upload_pending", "persisted"} {
		t.Run(phase, func(t *testing.T) {
			bundle, _, capture := frozenAckBundle(t, false)
			ack := finalizationAckFixture(capture, "published")
			journal, err := bundle.OpenRoot("finalization")
			if err != nil {
				t.Fatal(err)
			}
			defer journal.Close()
			directory, err := privateBundleFile(journal)
			if err != nil {
				t.Fatal(err)
			}
			defer directory.Close()
			if err := writePrivateJSON(journal, directory, "acknowledgement.json", ack, true); err != nil {
				t.Fatal(err)
			}
			if phase != "local_durable" {
				if _, err := AdvanceFinalization(bundle, capture.Binding, "local_durable", "upload_pending"); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "persisted" {
				if _, err := AdvanceFinalization(bundle, capture.Binding, "upload_pending", "persisted"); err != nil {
					t.Fatal(err)
				}
			}
			if err := bundle.RemoveAll("work"); err != nil {
				t.Fatal(err)
			}
			got, err := AcknowledgeFinalization(context.Background(), bundle, ack)
			if err != nil || got.State != "published" {
				t.Fatal("durable acknowledgement could not complete original progression", err)
			}
		})
	}
}

func TestFinalizationAcknowledgementRejectsChangedAuthorityAndCorruptJournal(t *testing.T) {
	for _, mode := range []string{"node", "tree", "unclean", "checkpoint", "upload", "publication", "symlink", "hardlink", "mode", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			bundle, path, capture := frozenAckBundle(t, false)
			original := finalizationAckFixture(capture, "published")
			if _, err := AcknowledgeFinalization(context.Background(), bundle, original); err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(original)
			var changed FinalizationAcknowledgement
			if err := json.Unmarshal(data, &changed); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			name := filepath.Join(path, "finalization/acknowledgement.json")
			switch mode {
			case "node":
				changed.Capture.Binding.NodeID = changed.Capture.Binding.UserID
			case "tree":
				changed.Capture.TreeDigest = changed.Capture.Binding.InitialTreeDigest + "0"
			case "unclean":
				changed.Capture.Unclean = true
			case "checkpoint":
				changed.Receipt.CheckpointID = &changed.Capture.Binding.UserID
			case "upload":
				changed.Receipt.UploadAttempt++
			case "publication":
				changed.Publication.ID = changed.Capture.Binding.UserID
			case "symlink":
				if err := os.Rename(name, name+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(name+".original", name); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(name, name+".link"); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(name, 0644); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				cancel()
			}
			if _, err := AcknowledgeFinalization(ctx, bundle, changed); err == nil {
				t.Fatal("changed or unsafe acknowledgement accepted")
			}
			record, err := ReadFinalization(bundle, capture.Binding)
			if err != nil || record.State != "published" {
				t.Fatal("failed acknowledgement corrupted original capture", err)
			}
		})
	}
}

func TestFinalizationCleanupRequiresIndependentPublicationAcknowledgement(t *testing.T) {
	bundle, _, capture := frozenAckBundle(t, false)
	ack := finalizationAckFixture(capture, "persisted")
	record, err := AcknowledgeFinalization(context.Background(), bundle, ack)
	if err != nil {
		t.Fatal(err)
	}
	if err := RetainFinalizationRuntimeCleanup(bundle, record, "/runtime/session"); err == nil {
		t.Fatal("persistence alone authorized cleanup")
	}
	if _, err := AcknowledgeFinalization(context.Background(), bundle, finalizationAckFixture(capture, "upload_pending")); err == nil {
		t.Fatal("acknowledgement downgraded persistence")
	}
	if _, err := os.Stat(filepath.Join(bundle.Name(), "finalization/runtime-cleanup.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid cleanup left a completion marker", err)
	}
}
