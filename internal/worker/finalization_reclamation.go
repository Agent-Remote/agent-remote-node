package worker

import (
	"context"

	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

type finalizationRecoveryClient interface {
	finalizationTransferClient
	ObserveSkillCapturePending(context.Context, skillmanager.CaptureFailure) error
	AuthorizeSkillReclamationChallenge(context.Context, string, skillmanager.FinalizationAcknowledgement) (skillmanager.ReclamationAuthorization, error)
}

type finalizationReclaimer interface {
	ReclaimSkillFinalization(context.Context, string, skillmanager.FinalizationRecord, runtimehelper.SkillReclamationAuthorizer) (skillmanager.FinalizationRecord, error)
	ResumeSkillReclamation(context.Context, string, string, string) (skillmanager.FinalizationRecord, error)
}

// Only the caller that has joined transfer may enter reclamation: transfer owns a kernel read hold
// through acknowledgement and cleanup. The Helper independently checks its exact saved receipts.
func reclaimTransferredFinalization(ctx context.Context, client finalizationRecoveryClient, helper finalizationReclaimer, journal finalizationTransferJournal, capture skillmanager.FinalizationRecord) error {
	saved, err := journal.load(capture)
	if err != nil || saved == nil || saved.record.Receipt == nil || saved.record.Publication == nil {
		return errFinalizationPending
	}
	ack := skillmanager.FinalizationAcknowledgement{Version: 1, Capture: saved.record.Capture, Receipt: *saved.record.Receipt, Publication: saved.record.Publication}
	state, err := ack.State()
	if err != nil {
		return errFinalizationPending
	}
	capture.State = state
	if !capture.CanDeleteSession() {
		return errFinalizationPending
	}
	reclaimed, err := helper.ReclaimSkillFinalization(ctx, "finalization-reclaim:"+capture.Binding.SnapshotID, capture, client.AuthorizeSkillReclamationChallenge)
	if err != nil || reclaimed != capture {
		return errFinalizationPending
	}
	return nil
}

func resumeFinalizationReclamation(ctx context.Context, helper finalizationReclaimer, nodeID, sessionID string) error {
	reclaimed, err := helper.ResumeSkillReclamation(ctx, "finalization-resume:"+sessionID, nodeID, sessionID)
	if err != nil || reclaimed.Validate() != nil || !reclaimed.CanDeleteSession() || reclaimed.ObjectsVersion != 1 ||
		reclaimed.Binding.NodeID != nodeID || reclaimed.Binding.SessionID != sessionID {
		return errFinalizationPending
	}
	return nil
}
