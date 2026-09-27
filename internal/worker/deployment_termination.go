package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

type deploymentTerminationClient interface {
	GetSkillDeploymentTermination(context.Context, skillmanager.SkillDeploymentIdentity) (*api.SkillDeploymentTerminationIntent, error)
	RequestSkillDeploymentTermination(context.Context, skillmanager.SkillDeploymentIdentity, api.SkillDeploymentTerminationRequest) (api.SkillDeploymentTerminationIntent, error)
	ConfirmSkillDeploymentTermination(context.Context, api.SkillDeploymentTerminatedResult) (api.SkillDeploymentTerminationObservation, error)
	InspectSkillDeploymentTermination(context.Context, api.SkillDeploymentTerminatedResult) (api.SkillDeploymentTerminationObservation, error)
}

type deploymentControlClient interface {
	deploymentResultClient
	deploymentTerminationClient
}

type deploymentDrainHelper interface {
	DrainSkillDeployment(context.Context, string, skillmanager.SkillDeploymentIdentity) (skillmanager.DeploymentDrain, error)
}

func resumeDeploymentTermination(ctx context.Context, client deploymentControlClient, helper deploymentDrainHelper, journal deploymentJournal, pending *deploymentTerminationEntry) error {
	record := pending.record
	if record.Intent == nil {
		intent, err := client.GetSkillDeploymentTermination(ctx, record.Binding)
		if err != nil {
			return err
		}
		if intent == nil {
			// A competing success may have committed after this request was saved or its POST was lost.
			if record.Preparation != nil {
				observed, err := client.InspectSkillDeployment(ctx, *record.Preparation)
				if err != nil || observed.Result != *record.Preparation {
					return errDeploymentPending
				}
				if observed.Accepted {
					return journal.restoreConfirmedPreparation(*pending, observed)
				}
			}
			requested, err := client.RequestSkillDeploymentTermination(ctx, record.Binding, record.Request)
			if err != nil {
				return err
			}
			if requested.Request != record.Request {
				return errDeploymentPending
			}
			intent = &requested
		}
		if intent.Validate() != nil || intent.Binding != record.Binding {
			return errDeploymentPending
		}
		record.Intent, record.Request = intent, intent.Request
		pending, err = journal.saveTermination(&pending.entry, record)
		if err != nil {
			return err
		}
	}
	if record.Drain == nil {
		drain, err := helper.DrainSkillDeployment(ctx, "deployment-drain:"+record.Binding.TaskID, record.Binding)
		if err != nil {
			return err
		}
		if drain.Validate(record.Binding) != nil {
			return errDeploymentPending
		}
		record.Drain = &drain
		pending, err = journal.saveTermination(&pending.entry, record)
		if err != nil {
			return err
		}
	}
	result := record.result()
	observed, err := client.InspectSkillDeploymentTermination(ctx, result)
	if err != nil || !validTerminationObservation(observed, result) {
		return errDeploymentPending
	}
	if record.Confirmation != nil {
		if observed != *record.Confirmation {
			return errDeploymentPending
		}
		return nil
	}
	if !observed.Accepted {
		observed, err = client.ConfirmSkillDeploymentTermination(ctx, result)
		if err != nil || !validTerminationObservation(observed, result) || !observed.Accepted {
			return errDeploymentPending
		}
	}
	record.Confirmation = &observed
	_, err = journal.saveTermination(&pending.entry, record)
	return err
}

func (j deploymentJournal) beginTermination(binding skillmanager.SkillDeploymentIdentity, request api.SkillDeploymentTerminationRequest, intent *api.SkillDeploymentTerminationIntent, previous *deploymentEntry) (*deploymentTerminationEntry, error) {
	record := deploymentTerminationRecord{SchemaVersion: 1, Binding: binding, Request: request, Intent: intent}
	var expected *ledger.Entry
	if previous != nil {
		expected, record.Preparation = &previous.entry, &previous.record.Result
	}
	return j.saveTermination(expected, record)
}

func (j deploymentJournal) restoreConfirmedPreparation(pending deploymentTerminationEntry, observed api.SkillDeploymentResultObservation) error {
	result := pending.record.Preparation
	if pending.entry.Status != deploymentTerminationRequested || result == nil || observed.Result != *result || !observed.Accepted || observed.TaskStatus != "succeeded" || observed.CurrentLeaseAttempt != result.LeaseAttempt {
		return ledger.ErrConflict
	}
	data, err := json.Marshal(deploymentRecord{SchemaVersion: 1, Result: *result})
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return err
	}
	return j.ledger.CompareAndSwap(&pending.entry, ledger.Entry{TaskID: pending.entry.TaskID, Status: deploymentPreparedConfirmed, Result: payload})
}

func recoverDeploymentTerminations(ctx context.Context, client deploymentControlClient, helper deploymentDrainHelper, journal deploymentJournal, nodeID string) error {
	var entries []ledger.Entry
	for _, status := range []string{deploymentTerminationRequested, deploymentTerminationRevoked, deploymentTerminationDrained} {
		found, err := journal.ledger.List(status)
		if err != nil {
			return err
		}
		entries = append(entries, found...)
	}
	var failures []error
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		pending, err := decodeDeploymentTermination(entry)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if pending.record.Binding.NodeID != nodeID {
			failures = append(failures, errDeploymentPending)
			continue
		}
		if err := resumeDeploymentTermination(ctx, client, helper, journal, pending); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
