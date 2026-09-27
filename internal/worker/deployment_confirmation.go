package worker

import (
	"context"
	"errors"

	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
)

func (w Worker) recoverDeploymentConfirmations(ctx context.Context) error {
	journal := deploymentJournal{w.ledger}
	inspectionErr := inspectPendingDeployments(ctx, w.client, journal, w.cfg.NodeID)
	terminationErr := recoverDeploymentTerminations(ctx, w.client, runtimehelper.NewClient(w.cfg.RuntimeSocketPath), journal, w.cfg.NodeID)
	if errors.Join(inspectionErr, terminationErr) != nil {
		return errDeploymentPending
	}
	return nil
}

func inspectPendingDeployments(ctx context.Context, client deploymentControlClient, journal deploymentJournal, nodeID string) error {
	entries, err := journal.ledger.List(deploymentPreparedPending)
	if err != nil {
		return err
	}
	var failures []error
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		pending, err := decodeDeploymentEntry(entry)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if pending.record.Result.Preparation.Binding.NodeID != nodeID {
			failures = append(failures, errDeploymentPending)
			continue
		}
		binding := pending.record.Result.Preparation.Binding
		intent, err := client.GetSkillDeploymentTermination(ctx, binding)
		if err != nil {
			failures = append(failures, errDeploymentPending)
			continue
		}
		if intent != nil {
			if _, err := journal.beginTermination(binding, intent.Request, intent, pending); err != nil {
				failures = append(failures, err)
			}
			continue
		}
		observation, err := client.InspectSkillDeployment(ctx, pending.record.Result)
		if err != nil || observation.Result != pending.record.Result {
			failures = append(failures, errDeploymentPending)
			continue
		}
		if observation.Accepted {
			if err := journal.confirm(*pending, observation); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}
