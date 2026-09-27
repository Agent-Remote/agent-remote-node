package worker

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
)

const deploymentPreparedPending = "deployment_prepared_pending"
const deploymentPreparedConfirmed = "deployment_prepared_confirmed"

type deploymentRecord struct {
	SchemaVersion int                               `json:"schema_version"`
	Result        api.SkillDeploymentPreparedResult `json:"result"`
}

type deploymentEntry struct {
	entry  ledger.Entry
	record deploymentRecord
}

type deploymentJournal struct{ ledger *ledger.Ledger }

func (j deploymentJournal) load(taskID string) (*deploymentEntry, error) {
	entry, exists, err := j.ledger.Get(taskID)
	if err != nil || !exists {
		return nil, err
	}
	return decodeDeploymentEntry(entry)
}

func decodeDeploymentEntry(entry ledger.Entry) (*deploymentEntry, error) {
	if entry.Status != deploymentPreparedPending && entry.Status != deploymentPreparedConfirmed || entry.Error != nil {
		return nil, errors.New("deployment ledger uses a different result protocol")
	}
	data, err := json.Marshal(entry.Result)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	if !exactJournalFields(fields, "schema_version", "result") {
		return nil, errors.New("invalid deployment journal fields")
	}
	var record deploymentRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	if record.SchemaVersion != 1 || record.Result.Validate() != nil || entry.TaskID != "prepare_account_skills:"+record.Result.Preparation.Binding.AttemptID {
		return nil, errors.New("invalid deployment journal identity")
	}
	return &deploymentEntry{entry: entry, record: record}, nil
}

func (j deploymentJournal) propose(taskID string, result api.SkillDeploymentPreparedResult, previous *deploymentEntry, observation *api.SkillDeploymentResultObservation) (*deploymentEntry, error) {
	if result.Validate() != nil || taskID != "prepare_account_skills:"+result.Preparation.Binding.AttemptID {
		return nil, errDeploymentPending
	}
	record := deploymentRecord{SchemaVersion: 1, Result: result}
	var expected *ledger.Entry
	if previous != nil {
		if previous.entry.TaskID != taskID {
			return nil, ledger.ErrConflict
		}
		if previous.record == record {
			current, err := j.load(taskID)
			if err != nil || current == nil || current.record != record {
				return nil, errors.Join(ledger.ErrConflict, err)
			}
			return current, nil
		}
		if previous.entry.Status != deploymentPreparedPending || observation == nil || observation.Accepted ||
			observation.Result != previous.record.Result || observation.CurrentLeaseAttempt != result.LeaseAttempt || result.LeaseAttempt <= previous.record.Result.LeaseAttempt ||
			result.Preparation != previous.record.Result.Preparation || (observation.TaskStatus != "leased" && observation.TaskStatus != "running") {
			return nil, ledger.ErrConflict
		}
		expected = &previous.entry
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
	if err := j.ledger.CompareAndSwap(expected, ledger.Entry{TaskID: taskID, Status: deploymentPreparedPending, Result: payload}); err != nil {
		return nil, err
	}
	return j.load(taskID)
}

func (j deploymentJournal) confirm(previous deploymentEntry, observation api.SkillDeploymentResultObservation) error {
	if !observation.Accepted || observation.Result != previous.record.Result || observation.TaskStatus != "succeeded" || observation.CurrentLeaseAttempt != previous.record.Result.LeaseAttempt {
		return ledger.ErrConflict
	}
	if previous.entry.Status == deploymentPreparedConfirmed {
		return nil
	}
	if previous.entry.Status != deploymentPreparedPending {
		return ledger.ErrConflict
	}
	confirmed := previous.entry
	confirmed.Status = deploymentPreparedConfirmed
	err := j.ledger.CompareAndSwap(&previous.entry, confirmed)
	if errors.Is(err, ledger.ErrConflict) {
		current, readErr := j.load(previous.entry.TaskID)
		if readErr != nil {
			return readErr
		}
		if current != nil && current.record == previous.record && current.entry.Status == deploymentPreparedConfirmed {
			return nil
		}
	}
	return err
}
