package worker

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (f *finalizationFixture) ReclaimSkillFinalization(ctx context.Context, _ string, capture skillmanager.FinalizationRecord, authorize runtimehelper.SkillReclamationAuthorizer) (skillmanager.FinalizationRecord, error) {
	f.reclamationCalls++
	if f.cleanupCalls == 0 || !capture.CanDeleteSession() {
		f.t.Error("reclamation preceded terminal cleanup")
	}
	for _, hold := range f.holds {
		if _, err := hold.Stat(); !errors.Is(err, os.ErrClosed) {
			f.t.Error("reclamation started before transfer released its reader")
		}
	}
	if f.helperFault == "reclaim" {
		return skillmanager.FinalizationRecord{}, errors.New("reclamation interrupted")
	}
	saved, err := f.journal.load(capture)
	if err != nil || saved == nil || saved.record.Receipt == nil || saved.record.Publication == nil {
		return skillmanager.FinalizationRecord{}, errors.New("reclamation lacks terminal input")
	}
	ack := skillmanager.FinalizationAcknowledgement{Version: 1, Capture: saved.record.Capture, Receipt: *saved.record.Receipt, Publication: saved.record.Publication}
	grant, err := authorize(ctx, "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", ack)
	if err != nil || grant.Match(ack) != nil || grant.PublicationStatus == "conflicted" {
		return skillmanager.FinalizationRecord{}, errors.New("current remote input is unavailable or unresolved")
	}
	if f.helperFault == "changed_reclaim" {
		capture.Binding.DirectoryEpoch++
	}
	return capture, nil
}

func (f *finalizationFixture) ResumeSkillReclamation(_ context.Context, _, nodeID, sessionID string) (skillmanager.FinalizationRecord, error) {
	f.resumeCalls++
	if f.helperFault == "resume" {
		return skillmanager.FinalizationRecord{}, errors.New("resume interrupted")
	}
	capture := f.capture
	capture.State = f.publication.Status
	if capture.Binding.NodeID != nodeID || capture.Binding.SessionID != sessionID {
		return skillmanager.FinalizationRecord{}, errors.New("resume changed session")
	}
	if f.helperFault == "changed_resume" {
		capture.Binding.SessionID = capture.Binding.UserID
	}
	return capture, nil
}

func TestFinalizationReclamationFollowsClosedUploadHoldAndDurableCleanup(t *testing.T) {
	f := newFinalizationFixture(t, false, "published")
	f.page = &skillmanager.FinalizationPage{Items: []skillmanager.FinalizationInventoryItem{{SessionID: f.capture.Binding.SessionID, Record: &f.capture}}}
	if _, err := recoverFinalizationPage(context.Background(), f.client, f, f.journal, f.capture.Binding.NodeID, ""); err != nil {
		t.Fatal(err)
	}
	if f.reclamationCalls != 1 || f.cleanupCalls != 1 || !reflect.DeepEqual(f.recordedCalls(), []string{"termination", "begin", "put", "complete", "publish", "reclaim"}) {
		t.Fatal("reclamation lost operation ordering", f.recordedCalls())
	}
}

func TestFinalizationReclamationKeepsUncertainInputPending(t *testing.T) {
	for _, phase := range []string{"complete", "cleanup", "reclaim", "changed_reclaim", "remote", "conflict"} {
		t.Run(phase, func(t *testing.T) {
			status := "published"
			if phase == "conflict" {
				status = "conflicted"
			}
			f := newFinalizationFixture(t, false, status)
			f.page = &skillmanager.FinalizationPage{Items: []skillmanager.FinalizationInventoryItem{{SessionID: f.capture.Binding.SessionID, Record: &f.capture}}}
			switch phase {
			case "complete":
				f.fault = "complete"
			case "remote":
				f.fault = "reclaim"
			default:
				f.helperFault = phase
			}
			if _, err := recoverFinalizationPage(context.Background(), f.client, f, f.journal, f.capture.Binding.NodeID, ""); !errors.Is(err, errFinalizationPending) {
				t.Fatal("uncertain reclamation became complete", err)
			}
			if (phase == "complete" || phase == "cleanup") && f.reclamationCalls != 0 {
				t.Fatal("failed transfer/cleanup still reclaimed input")
			}
			if _, err := os.Stat(f.object); err != nil {
				t.Fatal("worker directly removed content", err)
			}
		})
	}
}

func TestFinalizationReclamationResumeDoesNotRequireTransferLedgerOrHTTP(t *testing.T) {
	for _, fault := range []string{"", "resume", "changed_resume"} {
		t.Run("fault_"+fault, func(t *testing.T) {
			f := newFinalizationFixture(t, false, "published")
			f.helperFault = fault
			f.page = &skillmanager.FinalizationPage{Items: []skillmanager.FinalizationInventoryItem{{SessionID: f.capture.Binding.SessionID, Code: "reclamation_pending"}}}
			if _, err := recoverFinalizationPage(context.Background(), f.client, f, f.journal, f.capture.Binding.NodeID, ""); (err != nil) != (fault != "") {
				t.Fatal("unexpected resume outcome", err)
			}
			if len(f.recordedCalls()) != 0 || f.reclamationCalls != 0 || f.cleanupCalls != 0 || f.resumeCalls != 1 {
				t.Fatal("resume reentered transfer or authority acquisition")
			}
			if saved, err := f.journal.load(f.capture); err != nil || saved != nil {
				t.Fatal("resume created transfer state", err)
			}
		})
	}
}
