package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestFinalizationReconciliationTransfersNewCapture(t *testing.T) {
	f := newFinalizationFixture(t, false, "published")
	f.page = &skillmanager.FinalizationPage{Items: []skillmanager.FinalizationInventoryItem{{SessionID: f.capture.Binding.SessionID, Code: "not_finalized"}}}
	f.observation = &runtimehelper.SkillSessionObservation{SessionID: f.capture.Binding.SessionID, State: "finalized", Record: &f.capture}
	if _, err := recoverFinalizationPage(context.Background(), f.client, f, f.journal, f.capture.Binding.NodeID, ""); err != nil {
		t.Fatal(err)
	}
	saved, err := f.journal.load(f.capture)
	if err != nil || saved == nil || saved.record.Publication == nil || f.cleanupCalls != 1 {
		t.Fatal("new capture did not complete transfer", err)
	}
}

func TestFinalizationReconciliationKeepsUnfinishedRuntime(t *testing.T) {
	for _, state := range []string{"not_started", "running", "pending", "foreign_capture"} {
		t.Run(state, func(t *testing.T) {
			f := newFinalizationFixture(t, false, "published")
			f.page = &skillmanager.FinalizationPage{Items: []skillmanager.FinalizationInventoryItem{{SessionID: f.capture.Binding.SessionID, Code: "not_finalized"}}}
			f.observation = &runtimehelper.SkillSessionObservation{SessionID: f.capture.Binding.SessionID, State: state}
			if state == "pending" {
				f.reconciliationError = errors.New("uncertain launch")
			}
			if state == "foreign_capture" {
				capture := f.capture
				capture.Binding.SessionID = capture.Binding.UserID
				f.observation.State, f.observation.Record = "finalized", &capture
			}
			_, err := recoverFinalizationPage(context.Background(), f.client, f, f.journal, f.capture.Binding.NodeID, "")
			if (err != nil) != (state == "pending" || state == "foreign_capture") {
				t.Fatal("wrong pending state", err)
			}
			if len(f.recordedCalls()) != 0 || f.cleanupCalls != 0 {
				t.Fatal("unfinished runtime transferred or cleaned")
			}
		})
	}
}

func TestFinalizationReclamationInventoryNeverRecapturesOrUploads(t *testing.T) {
	for _, code := range []string{"reclamation_pending", "content_reclaimed"} {
		t.Run(code, func(t *testing.T) {
			f := newFinalizationFixture(t, false, "published")
			f.page = &skillmanager.FinalizationPage{Items: []skillmanager.FinalizationInventoryItem{{SessionID: f.capture.Binding.SessionID, Code: code}}}
			_, err := recoverFinalizationPage(context.Background(), f.client, f, f.journal, f.capture.Binding.NodeID, "")
			if err != nil {
				t.Fatal("reclamation completion became an endless upload retry", err)
			}
			if (f.resumeCalls == 1) != (code == "reclamation_pending") {
				t.Fatal("pending deletion was not resumed independently")
			}
			if len(f.recordedCalls()) != 0 || f.cleanupCalls != 0 {
				t.Fatal("reclamation inventory retried content transfer or cleanup")
			}
			if saved, err := f.journal.load(f.capture); err != nil || saved != nil {
				t.Fatal("reclamation inventory created another transfer", err)
			}
		})
	}
}

func TestCapturePendingRetriesWithoutTransferThenFreezes(t *testing.T) {
	f := newFinalizationFixture(t, false, "published")
	f.page = &skillmanager.FinalizationPage{Items: []skillmanager.FinalizationInventoryItem{{SessionID: f.capture.Binding.SessionID, Code: "not_finalized"}}}
	failure := skillmanager.CaptureFailure{Binding: f.capture.Binding, Code: "quota_exceeded"}
	f.observation = &runtimehelper.SkillSessionObservation{SessionID: failure.Binding.SessionID, State: "capture_pending", Pending: &failure}
	for _, fault := range []string{"capture_pending", "", ""} {
		f.fault = fault
		if _, err := recoverFinalizationPage(context.Background(), f.client, f, f.journal, failure.Binding.NodeID, ""); !errors.Is(err, errFinalizationPending) {
			t.Fatal("capture retry was forgotten", err)
		}
		if saved, err := f.journal.load(f.capture); err != nil || saved != nil || f.cleanupCalls != 0 || f.reclamationCalls != 0 {
			t.Fatal("failed capture created transfer or cleanup authority", err)
		}
	}
	for _, call := range f.recordedCalls() {
		if call != "capture_pending" {
			t.Fatal("unexpected content transfer", call)
		}
	}
	f.observation = &runtimehelper.SkillSessionObservation{SessionID: failure.Binding.SessionID, State: "finalized", Record: &f.capture}
	if _, err := recoverFinalizationPage(context.Background(), f.client, f, f.journal, failure.Binding.NodeID, ""); err != nil {
		t.Fatal("capture recovery did not progress", err)
	}
	if f.cleanupCalls != 1 {
		t.Fatal("recovered capture did not complete")
	}
}
