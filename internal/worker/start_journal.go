package worker

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
)

const managedStartPending = "managed_start_pending"
const managedStartConfirmed = "managed_start_confirmed"
const managedStartRetired = "managed_start_retired"

type managedStartRecord struct {
	SchemaVersion int                           `json:"schema_version"`
	Binding       api.SkillSnapshotIdentity     `json:"binding"`
	Outcome       api.ManagedSessionStartResult `json:"outcome"`
}

type managedStartJournal struct {
	ledger *ledger.Ledger
}

type managedStartEntry struct {
	entry      ledger.Entry
	record     managedStartRecord
	retirement api.ManagedStartObservation
}

func (j managedStartJournal) load(taskID string) (*managedStartEntry, error) {
	entry, exists, err := j.ledger.Get(taskID)
	if err != nil || !exists {
		return nil, err
	}
	return decodeManagedStartEntry(entry)
}

func (j managedStartJournal) pending() ([]managedStartEntry, error) {
	entries, err := j.ledger.List(managedStartPending)
	if err != nil {
		return nil, err
	}
	result := make([]managedStartEntry, 0, len(entries))
	for _, entry := range entries {
		decoded, err := decodeManagedStartEntry(entry)
		if err != nil {
			return nil, err
		}
		result = append(result, *decoded)
	}
	return result, nil
}

func (j managedStartJournal) propose(taskID string, binding api.SkillSnapshotIdentity, outcome api.ManagedSessionStartResult, previous *managedStartEntry, observation *api.ManagedStartObservation) (*managedStartEntry, error) {
	record := managedStartRecord{SchemaVersion: 1, Binding: binding, Outcome: outcome}
	if err := record.validate(taskID); err != nil {
		return nil, err
	}
	var expected *ledger.Entry
	if previous != nil {
		if previous.entry.Status == managedStartRetired {
			return nil, ledger.ErrConflict
		}
		if previous.entry.TaskID != taskID || previous.record.Binding != binding {
			return nil, ledger.ErrConflict
		}
		if previous.record == record {
			current, err := j.load(taskID)
			if err != nil || current == nil || current.record != record {
				return nil, errors.Join(ledger.ErrConflict, err)
			}
			return current, nil
		}
		if previous.entry.Status != managedStartPending || observation == nil || observation.Accepted ||
			observation.Result != previous.record.Outcome || observation.CurrentLeaseAttempt != outcome.LeaseAttempt ||
			outcome.LeaseAttempt <= previous.record.Outcome.LeaseAttempt ||
			(observation.TaskStatus != "leased" && observation.TaskStatus != "running") {
			return nil, ledger.ErrConflict
		}
		expected = &previous.entry
	}
	data, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	entry := ledger.Entry{TaskID: taskID, Status: managedStartPending, Result: payload}
	if err := j.ledger.CompareAndSwap(expected, entry); err != nil {
		return nil, err
	}
	return j.load(taskID)
}

func (j managedStartJournal) confirm(previous managedStartEntry, receipt api.ManagedSessionStartResult) error {
	if receipt != previous.record.Outcome {
		return ledger.ErrConflict
	}
	if previous.entry.Status == managedStartConfirmed {
		return nil
	}
	if previous.entry.Status != managedStartPending {
		return ledger.ErrConflict
	}
	confirmed := previous.entry
	confirmed.Status = managedStartConfirmed
	err := j.ledger.CompareAndSwap(&previous.entry, confirmed)
	if errors.Is(err, ledger.ErrConflict) {
		current, loadErr := j.load(previous.entry.TaskID)
		if loadErr != nil {
			return loadErr
		}
		if current != nil && current.record == previous.record && current.entry.Status == managedStartConfirmed {
			return nil
		}
	}
	return err
}

func (r managedStartRecord) validate(taskID string) error {
	if r.SchemaVersion != 1 || taskID == "" || len(taskID) > 128 || strings.ContainsAny(taskID, "/\\\x00\r\n") {
		return errors.New("invalid managed startup journal identity")
	}
	status := "running"
	if r.Outcome.Code == "SKILL_START_STOPPED" {
		status = "stopped"
	}
	expected, err := api.NewManagedSessionStartResult(r.Binding, r.Outcome.LeaseAttempt, status, r.Outcome.TmuxSessionName)
	if err != nil || expected != r.Outcome {
		return errors.New("invalid managed startup journal outcome")
	}
	return nil
}

func decodeManagedStartEntry(entry ledger.Entry) (*managedStartEntry, error) {
	if (entry.Status != managedStartPending && entry.Status != managedStartConfirmed && entry.Status != managedStartRetired) || entry.Error != nil {
		return nil, errors.New("task ledger contains a different execution protocol")
	}
	data, err := json.Marshal(entry.Result)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	names := []string{"schema_version", "binding", "outcome"}
	if entry.Status == managedStartRetired {
		names = append(names, "retirement")
	}
	if !exactJournalFields(fields, names...) {
		return nil, errors.New("invalid managed startup journal fields")
	}
	var binding map[string]json.RawMessage
	if err := json.Unmarshal(fields["binding"], &binding); err != nil {
		return nil, err
	}
	if !exactJournalFields(binding, "snapshot_id", "task_id", "node_id", "user_id", "account_id", "session_id", "runtime_backend") {
		return nil, errors.New("invalid managed startup journal binding")
	}
	var record managedStartRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	if err := record.validate(entry.TaskID); err != nil {
		return nil, err
	}
	decoded := &managedStartEntry{entry: entry, record: record}
	if entry.Status == managedStartRetired {
		if err := json.Unmarshal(fields["retirement"], &decoded.retirement); err != nil {
			return nil, err
		}
		if !cancelledManagedStart(record.Outcome, decoded.retirement) {
			return nil, errors.New("invalid managed startup retirement")
		}
	}
	return decoded, nil
}

func exactJournalFields(fields map[string]json.RawMessage, names ...string) bool {
	if len(fields) != len(names) {
		return false
	}
	for _, name := range names {
		if fields[name] == nil {
			return false
		}
	}
	return true
}
