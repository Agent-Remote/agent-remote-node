package worker

import (
	"context"
	"errors"
	"net"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func runDeployment(ctx context.Context, client deploymentPreparationClient, helper deploymentPreparationHelper, journal deploymentJournal, taskID string, binding skillmanager.SkillDeploymentIdentity, attempt int64) error {
	if binding.Validate() != nil || attempt < 1 || attempt > 2147483647 || taskID != "prepare_account_skills:"+binding.AttemptID {
		return errDeploymentPending
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	entry, exists, err := journal.ledger.Get(taskID)
	if err != nil {
		return err
	}
	if exists && deploymentTerminationStatus(entry.Status) {
		pending, err := decodeDeploymentTermination(entry)
		if err != nil || pending.record.Binding != binding {
			return errDeploymentPending
		}
		if pending.entry.Status == deploymentTerminationRequested && attempt > pending.record.Request.LeaseAttempt {
			intent, err := client.GetSkillDeploymentTermination(ctx, binding)
			if err != nil {
				return err
			}
			record := pending.record
			if intent != nil {
				record.Intent, record.Request = intent, intent.Request
			} else {
				record.Request.LeaseAttempt = attempt
			}
			pending, err = journal.saveTermination(&pending.entry, record)
			if err != nil {
				return err
			}
		}
		return resumeDeploymentTermination(ctx, client, helper, journal, pending)
	}
	previous, err := journal.load(taskID)
	if err != nil {
		return err
	}
	if previous != nil && previous.record.Result.Preparation.Binding != binding {
		return errDeploymentPending
	}
	if previous != nil && previous.entry.Status == deploymentPreparedConfirmed {
		return prepareDeployment(ctx, client, helper, journal, taskID, binding, attempt)
	}
	intent, err := client.GetSkillDeploymentTermination(ctx, binding)
	if err != nil {
		return err
	}
	if intent != nil {
		pending, err := journal.beginTermination(binding, intent.Request, intent, previous)
		if err != nil {
			return err
		}
		return resumeDeploymentTermination(ctx, client, helper, journal, pending)
	}
	err = prepareDeployment(ctx, client, helper, journal, taskID, binding, attempt)
	if err == nil {
		return nil
	}
	// Unknown success POST outcomes stay pending. A definite rejection may revoke only after
	// the termination coordinator inspects the retained original proposal for competing success.
	previous, readErr := journal.load(taskID)
	if readErr != nil || previous != nil && previous.entry.Status == deploymentPreparedConfirmed {
		return errors.Join(err, readErr)
	}
	code := deploymentFailureReason(err)
	if code == "" {
		return err
	}
	pending, saveErr := journal.beginTermination(binding, api.SkillDeploymentTerminationRequest{LeaseAttempt: attempt, ErrorCode: code}, nil, previous)
	if saveErr != nil {
		return errors.Join(err, saveErr)
	}
	// Shutdown leaves the original request durable for the independent recovery loop.
	if ctx.Err() != nil {
		return err
	}
	return resumeDeploymentTermination(ctx, client, helper, journal, pending)
}

func deploymentFailureReason(err error) string {
	var response *api.HTTPError
	if errors.As(err, &response) {
		// Account disablement revokes this attempt through the existing drain protocol.
		if response.Code == "ACCOUNT_NOT_AVAILABLE" {
			return "AUTHORIZATION_DENIED"
		}
		request := api.SkillDeploymentTerminationRequest{LeaseAttempt: 1, ErrorCode: response.Code}
		if request.Validate() == nil {
			return response.Code
		}
		if response.Code == "DEPLOYMENT_REVOKED" {
			return "DEPLOYMENT_INTERRUPTED"
		}
		return ""
	}
	if errors.Is(err, errDeploymentLeaseLost) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "DEPLOYMENT_INTERRUPTED"
	}
	var helper *runtimehelper.Error
	if errors.As(err, &helper) && helper.Code == "SKILL_DEPLOYMENT_UNAVAILABLE" {
		return "NODE_UNAVAILABLE"
	}
	var connection *net.OpError
	if errors.As(err, &connection) && connection.Net == "unix" {
		return "NODE_UNAVAILABLE"
	}
	var network net.Error
	if errors.As(err, &network) {
		return "TRANSFER_FAILED"
	}
	return ""
}
