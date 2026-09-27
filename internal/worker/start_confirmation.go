package worker

import (
	"context"
	"errors"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
)

type managedStartConfirmationClient interface {
	ConfirmManagedSessionStart(context.Context, string, api.SkillSnapshotIdentity, int64, api.ManagedSessionStartResult) (api.ManagedSessionStartResult, error)
	InspectManagedSessionStart(context.Context, string, api.SkillSnapshotIdentity, int64, api.ManagedSessionStartResult) (api.ManagedStartObservation, error)
}

func (w Worker) recoverManagedStartConfirmations(ctx context.Context) error {
	if err := inspectPendingManagedStarts(ctx, w.client, managedStartJournal{ledger: w.ledger}, w.cfg.NodeID); err != nil {
		return errManagedSessionPending
	}
	return nil
}

// confirmManagedStart persists a verified outcome before sending it. A true return means the Server
// committed, even when the local acknowledgement write failed; that receipt wins lease cancellation.
func confirmManagedStart(ctx context.Context, client managedStartConfirmationClient, journal managedStartJournal, taskID string, binding api.SkillSnapshotIdentity, outcome api.ManagedSessionStartResult, previous *managedStartEntry, observation *api.ManagedStartObservation) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	pending, err := journal.propose(taskID, binding, outcome, previous, observation)
	if err != nil {
		return false, err
	}
	if pending.entry.Status == managedStartConfirmed {
		return true, nil
	}
	if pending.entry.Status != managedStartPending {
		return false, errManagedStartRetired
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	receipt, err := client.ConfirmManagedSessionStart(ctx, taskID, binding, outcome.LeaseAttempt, outcome)
	if err != nil || receipt != outcome {
		return false, errors.Join(errManagedSessionPending, err)
	}
	return true, journal.confirm(*pending, receipt)
}

// inspectPendingManagedStarts can run independently of task polling after a lost acknowledgement.
// It never republishes readiness, reacquires a lease, changes an outcome or launches a runtime.
func inspectPendingManagedStarts(ctx context.Context, client managedStartConfirmationClient, journal managedStartJournal, nodeID string) error {
	pending, err := journal.pending()
	if err != nil {
		return err
	}
	var failures []error
	for _, entry := range pending {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if entry.record.Binding.NodeID != nodeID {
			failures = append(failures, errors.New("pending startup belongs to a different node"))
			continue
		}
		record := entry.record
		observed, err := client.InspectManagedSessionStart(ctx, entry.entry.TaskID, record.Binding, record.Outcome.LeaseAttempt, record.Outcome)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if observed.Result != record.Outcome {
			failures = append(failures, errors.New("pending startup observation changed its outcome"))
			continue
		}
		if observed.Accepted {
			if err := journal.confirm(entry, observed.Result); err != nil {
				failures = append(failures, err)
			}
		} else if observed.TaskStatus == "cancelled" {
			if err := journal.retire(entry, observed); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}
