package worker

import (
	"context"
	"errors"

	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

var errFinalizationPending = errors.New("retained skill finalizations require recovery")

type finalizationInventoryReader interface {
	finalizationReader
	finalizationReclaimer
	ListSkillFinalizations(context.Context, string, string, string) (skillmanager.FinalizationPage, error)
	ReconcileSkillSession(context.Context, string, string, string) (runtimehelper.SkillSessionObservation, error)
}

func (w Worker) finalizationRecoveryOperation() func(context.Context) error {
	cursor := ""
	helper := managedFinalizationReader{
		managedAdmissionHelper: runtimehelper.NewClient(w.cfg.RuntimeSocketPath),
		admissions:             w.managedAdmissions, broker: w.browserBroker,
	}
	return func(ctx context.Context) error {
		journal, err := w.finalizations.acquire(ctx, w.cfg.LedgerPath)
		if err != nil {
			return errFinalizationPending
		}
		defer w.finalizations.release()

		next, err := recoverFinalizationPage(ctx, w.client, helper, journal, w.cfg.NodeID, cursor)
		cursor = next
		if err != nil {
			return errFinalizationPending
		}
		return nil
	}
}

// recoverFinalizationPage always advances past per-session failures so one corrupt bundle or
// rejected publication cannot starve later sessions. Each new pass revisits pending captures.
func recoverFinalizationPage(ctx context.Context, client finalizationRecoveryClient, helper finalizationInventoryReader, journal finalizationTransferJournal, nodeID, cursor string) (string, error) {
	page, err := helper.ListSkillFinalizations(ctx, "finalization-inventory", nodeID, cursor)
	if err != nil {
		return cursor, errFinalizationPending
	}
	if err := page.Validate(cursor, nodeID); err != nil {
		return cursor, errFinalizationPending
	}
	pending := page.InvalidNames
	for _, item := range page.Items {
		if err := ctx.Err(); err != nil {
			return cursor, err
		}
		if item.Record == nil {
			if item.Code == "content_reclaimed" {
				continue
			}
			if item.Code == "reclamation_pending" {
				if err := resumeFinalizationReclamation(ctx, helper, nodeID, item.SessionID); err != nil {
					pending = true
				}
				continue
			}
			if item.Code != "not_finalized" {
				pending = true
				continue
			}
			observation, err := helper.ReconcileSkillSession(ctx, "finalization-reconcile", nodeID, item.SessionID)
			if err != nil || observation.Validate(nodeID, item.SessionID) != nil {
				pending = true
				continue
			}
			if observation.Pending != nil {
				// Keep retrying capture even after the independent process receipt is committed.
				_ = client.ObserveSkillCapturePending(ctx, *observation.Pending)
				pending = true
				continue
			}
			if observation.Record == nil {
				continue
			}
			item.Record = observation.Record
		}
		if err := transferFinalization(ctx, client, helper, journal, *item.Record); err != nil {
			pending = true
			continue
		}
		if err := reclaimTransferredFinalization(ctx, client, helper, journal, *item.Record); err != nil {
			pending = true
		}
	}
	if pending {
		return page.NextCursor, errFinalizationPending
	}
	return page.NextCursor, nil
}
