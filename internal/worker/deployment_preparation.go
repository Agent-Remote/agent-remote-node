package worker

import (
	"context"
	"errors"
	"io"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

type deploymentResultClient interface {
	ConfirmSkillDeployment(context.Context, api.SkillDeploymentPreparedResult) (api.SkillDeploymentResultObservation, error)
	InspectSkillDeployment(context.Context, api.SkillDeploymentPreparedResult) (api.SkillDeploymentResultObservation, error)
}

type deploymentPreparationClient interface {
	deploymentControlClient
	GetSkillDeployment(context.Context, skillmanager.SkillDeploymentIdentity, int64) (skillmanager.SkillDeployment, error)
	ReadSkillDeploymentFile(context.Context, skillmanager.SkillDeploymentIdentity, int64, skillmanager.Entry, io.Writer) error
	RenewSkillDeploymentLease(context.Context, skillmanager.SkillDeploymentIdentity, int64) (api.SkillDeploymentLease, error)
}

type deploymentPreparationHelper interface {
	deploymentDrainHelper
	PrepareSkillDeployment(context.Context, string, skillmanager.SkillDeployment, runtimehelper.SkillDeploymentDownloader) (skillmanager.DeploymentPreparation, error)
}

func prepareDeployment(ctx context.Context, client deploymentPreparationClient, helper deploymentPreparationHelper, journal deploymentJournal, taskID string, binding skillmanager.SkillDeploymentIdentity, attempt int64) error {
	if binding.Validate() != nil || attempt < 1 || attempt > 2147483647 || taskID != "prepare_account_skills:"+binding.AttemptID {
		return errDeploymentPending
	}
	previous, err := journal.load(taskID)
	if err != nil {
		return err
	}
	var observation *api.SkillDeploymentResultObservation
	if previous != nil {
		if previous.record.Result.Preparation.Binding != binding {
			return errDeploymentPending
		}
		observed, err := client.InspectSkillDeployment(ctx, previous.record.Result)
		if err != nil || observed.Result != previous.record.Result {
			return errDeploymentPending
		}
		if observed.Accepted {
			return journal.confirm(*previous, observed)
		}
		if previous.entry.Status == deploymentPreparedConfirmed || observed.CurrentLeaseAttempt != attempt || observed.TaskStatus != "leased" && observed.TaskStatus != "running" {
			return errDeploymentPending
		}
		observation = &observed
	}
	var accepted bool
	var saveErr error
	var renewalErr error
	err = withTaskLease(ctx, errDeploymentLeaseLost, func(ctx context.Context) (taskLeaseTiming, error) {
		lease, err := client.RenewSkillDeploymentLease(ctx, binding, attempt)
		renewalErr = err
		if err != nil || lease.SkillDeploymentIdentity != binding || lease.LeaseAttempt != attempt {
			return taskLeaseTiming{}, errDeploymentLeaseLost
		}
		return taskLeaseTiming{lease.ServerTime, lease.LeaseUntil, lease.RenewAfterMilliseconds}, nil
	}, func(ctx context.Context) error {
		input, err := client.GetSkillDeployment(ctx, binding, attempt)
		if err != nil {
			return err
		}
		if err := input.Validate(binding); err != nil {
			return err
		}
		prepared, err := helper.PrepareSkillDeployment(ctx, "deployment-prepare:"+binding.TaskID, input, func(ctx context.Context, entry skillmanager.Entry, writer io.Writer) error {
			return client.ReadSkillDeploymentFile(ctx, binding, attempt, entry, writer)
		})
		if err != nil {
			return err
		}
		if err := prepared.Validate(input); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		pending, err := journal.propose(taskID, api.SkillDeploymentPreparedResult{LeaseAttempt: attempt, Preparation: prepared}, previous, observation)
		if err != nil {
			return err
		}
		confirmed, err := client.ConfirmSkillDeployment(ctx, pending.record.Result)
		if err != nil {
			return errors.Join(errDeploymentPending, err)
		}
		if !confirmed.Accepted || confirmed.Result != pending.record.Result || confirmed.TaskStatus != "succeeded" || confirmed.CurrentLeaseAttempt != attempt {
			return errDeploymentPending
		}
		accepted = true
		saveErr = journal.confirm(*pending, confirmed)
		return saveErr
	})
	// An exact accepted receipt wins renewal rejection caused by the task's terminal transition.
	if accepted {
		return saveErr
	}
	if errors.Is(err, errDeploymentLeaseLost) {
		return errors.Join(err, renewalErr)
	}
	return err
}
