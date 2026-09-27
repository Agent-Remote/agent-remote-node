package worker

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

const (
	deploymentTerminationRequested = "deployment_termination_requested"
	deploymentTerminationRevoked   = "deployment_termination_revoked"
	deploymentTerminationDrained   = "deployment_termination_drained"
	deploymentTerminationConfirmed = "deployment_termination_confirmed"
)

type deploymentTerminationRecord struct {
	SchemaVersion int                                        `json:"schema_version"`
	Binding       skillmanager.SkillDeploymentIdentity       `json:"binding"`
	Request       api.SkillDeploymentTerminationRequest      `json:"request"`
	Preparation   *api.SkillDeploymentPreparedResult         `json:"preparation"`
	Intent        *api.SkillDeploymentTerminationIntent      `json:"intent"`
	Drain         *skillmanager.DeploymentDrain              `json:"drain"`
	Confirmation  *api.SkillDeploymentTerminationObservation `json:"confirmation"`
}

type deploymentTerminationEntry struct {
	entry  ledger.Entry
	record deploymentTerminationRecord
}

func deploymentTerminationStatus(status string) bool {
	switch status {
	case deploymentTerminationRequested, deploymentTerminationRevoked, deploymentTerminationDrained, deploymentTerminationConfirmed:
		return true
	}
	return false
}

func (r deploymentTerminationRecord) status() (string, error) {
	if r.SchemaVersion != 1 || r.Binding.Validate() != nil || r.Request.Validate() != nil {
		return "", errDeploymentPending
	}
	if r.Preparation != nil && (r.Preparation.Validate() != nil || r.Preparation.Preparation.Binding != r.Binding) {
		return "", errDeploymentPending
	}
	status := deploymentTerminationRequested
	if r.Intent != nil {
		if r.Intent.Validate() != nil || r.Intent.Binding != r.Binding || r.Intent.Request != r.Request {
			return "", errDeploymentPending
		}
		status = deploymentTerminationRevoked
	}
	if r.Drain != nil {
		if r.Intent == nil || r.Drain.Validate(r.Binding) != nil {
			return "", errDeploymentPending
		}
		status = deploymentTerminationDrained
	}
	if r.Confirmation != nil {
		if r.Drain == nil || !validTerminationObservation(*r.Confirmation, r.result()) || !r.Confirmation.Accepted {
			return "", errDeploymentPending
		}
		status = deploymentTerminationConfirmed
	}
	return status, nil
}

func (r deploymentTerminationRecord) result() api.SkillDeploymentTerminatedResult {
	return api.SkillDeploymentTerminatedResult{Intent: *r.Intent, Drain: *r.Drain}
}

func validTerminationObservation(o api.SkillDeploymentTerminationObservation, result api.SkillDeploymentTerminatedResult) bool {
	if result.Validate() != nil || o.Result != result || o.CurrentLeaseAttempt < result.Intent.Request.LeaseAttempt || o.CurrentLeaseAttempt > 2147483647 {
		return false
	}
	if o.Accepted {
		return result.Intent.Outcome == "failed" && o.TaskStatus == "failed" || result.Intent.Outcome == "superseded" && o.TaskStatus == "cancelled"
	}
	switch o.TaskStatus {
	case "pending", "leased", "running", "expired":
		return true
	}
	return false
}

func decodeDeploymentTermination(entry ledger.Entry) (*deploymentTerminationEntry, error) {
	if !deploymentTerminationStatus(entry.Status) || entry.Error != nil {
		return nil, errDeploymentPending
	}
	data, err := json.Marshal(entry.Result)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	if len(fields) != 7 {
		return nil, errDeploymentPending
	}
	for _, key := range []string{"schema_version", "binding", "request", "preparation", "intent", "drain", "confirmation"} {
		if fields[key] == nil {
			return nil, errDeploymentPending
		}
	}
	var bindingFields map[string]json.RawMessage
	if json.Unmarshal(fields["binding"], &bindingFields) != nil || !exactJournalFields(bindingFields,
		"operation_id", "attempt_id", "task_id", "user_id", "account_id", "node_id", "checkpoint_id", "plan_digest", "tree_digest", "runtime_backend") {
		return nil, errDeploymentPending
	}
	var record deploymentTerminationRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	status, err := record.status()
	if err != nil || status != entry.Status || entry.TaskID != "prepare_account_skills:"+record.Binding.AttemptID {
		return nil, errDeploymentPending
	}
	return &deploymentTerminationEntry{entry: entry, record: record}, nil
}

func (j deploymentJournal) saveTermination(previous *ledger.Entry, record deploymentTerminationRecord) (*deploymentTerminationEntry, error) {
	status, err := record.status()
	if err != nil {
		return nil, err
	}
	taskID := "prepare_account_skills:" + record.Binding.AttemptID
	if previous != nil {
		if previous.TaskID != taskID {
			return nil, ledger.ErrConflict
		}
		if deploymentTerminationStatus(previous.Status) {
			old, err := decodeDeploymentTermination(*previous)
			if err != nil || !terminationAdvance(old.record, record) {
				return nil, errors.Join(ledger.ErrConflict, err)
			}
		} else {
			old, err := decodeDeploymentEntry(*previous)
			if err != nil || old.entry.Status != deploymentPreparedPending || record.Preparation == nil || *record.Preparation != old.record.Result {
				return nil, errors.Join(ledger.ErrConflict, err)
			}
		}
	}
	data, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	entry := ledger.Entry{TaskID: taskID, Status: status, Result: payload}
	if err := j.ledger.CompareAndSwap(previous, entry); err != nil {
		return nil, err
	}
	// Use the persisted timestamp in subsequent whole-record CAS; never reload a concurrent successor.
	current, exists, err := j.ledger.Get(taskID)
	if err != nil || !exists {
		return nil, errors.Join(ledger.ErrConflict, err)
	}
	decoded, err := decodeDeploymentTermination(current)
	if err != nil {
		return nil, err
	}
	actual, err := json.Marshal(decoded.record)
	if err != nil || !bytes.Equal(actual, data) {
		return nil, errors.Join(ledger.ErrConflict, err)
	}
	return decoded, nil
}

func terminationAdvance(old, next deploymentTerminationRecord) bool {
	if old.Binding != next.Binding || !samePreparedProposal(old.Preparation, next.Preparation) {
		return false
	}
	if old.Intent != nil && (next.Intent == nil || *old.Intent != *next.Intent) {
		return false
	}
	if old.Drain != nil && (next.Drain == nil || *old.Drain != *next.Drain) {
		return false
	}
	if old.Confirmation != nil && (next.Confirmation == nil || *old.Confirmation != *next.Confirmation) {
		return false
	}
	return old.Intent != nil || next.Intent != nil || old.Request.ErrorCode == next.Request.ErrorCode && next.Request.LeaseAttempt >= old.Request.LeaseAttempt
}

func samePreparedProposal(a, b *api.SkillDeploymentPreparedResult) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
