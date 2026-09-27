package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// ManagedStartObservation describes one locked Server observation, never a grant to launch or renew.
type ManagedStartObservation struct {
	Result              ManagedSessionStartResult `json:"result"`
	Accepted            bool                      `json:"accepted"`
	CurrentLeaseAttempt int64                     `json:"current_lease_attempt"`
	TaskStatus          string                    `json:"task_status"`
}

// InspectManagedSessionStart resolves an exact pending outcome without publishing it.
// Same-attempt absence on a live task cannot rule out concurrent confirmation. A newer attempt
// fences the old proposal; an unaccepted cancelled task excludes any later confirmation.
func (c Client) InspectManagedSessionStart(ctx context.Context, logicalTaskID string, binding SkillSnapshotIdentity, attempt int64, outcome ManagedSessionStartResult) (ManagedStartObservation, error) {
	path, err := managedStartResultPath(logicalTaskID)
	if err != nil {
		return ManagedStartObservation{}, err
	}
	if err := outcome.validate(binding, attempt); err != nil {
		return ManagedStartObservation{}, err
	}
	body, err := json.Marshal(outcome)
	if err != nil {
		return ManagedStartObservation{}, err
	}
	var response skillEnvelope[ManagedStartObservation]
	if err := c.skillRequest(ctx, http.MethodPost, path+"/inspect", "application/json", bytes.NewReader(body), &response); err != nil {
		return ManagedStartObservation{}, err
	}
	observed := response.Data
	status := "unconfirmed"
	if observed.Accepted {
		status = "completed"
	}
	if response.SchemaVersion != 1 || response.Status != status || response.Committed == nil || *response.Committed != observed.Accepted ||
		len(response.Errors) != 0 || observed.Result != outcome || observed.validate() != nil {
		return ManagedStartObservation{}, errors.New("managed startup observation does not match its original outcome")
	}
	return observed, nil
}

func (o ManagedStartObservation) validate() error {
	if o.CurrentLeaseAttempt < 0 || o.CurrentLeaseAttempt > 2147483647 {
		return errors.New("invalid observed task attempt")
	}
	if o.Accepted {
		status := "failed"
		if o.Result.Status == "running" {
			status = "succeeded"
		}
		if o.TaskStatus == status && o.CurrentLeaseAttempt >= o.Result.LeaseAttempt {
			return nil
		}
	} else {
		switch o.TaskStatus {
		case "pending", "leased", "running", "cancelled", "expired":
			return nil
		}
	}
	return errors.New("inconsistent managed startup observation")
}

// UnmarshalJSON rejects ambiguous or incomplete observations before they can resolve a pending write.
func (o *ManagedStartObservation) UnmarshalJSON(data []byte) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	names := []string{"result", "accepted", "current_lease_attempt", "task_status"}
	if len(fields) != len(names) {
		return errors.New("invalid managed startup observation fields")
	}
	for _, name := range names {
		if fields[name] == nil || bytes.Equal(bytes.TrimSpace(fields[name]), []byte("null")) {
			return errors.New("missing managed startup observation field")
		}
	}
	type plain ManagedStartObservation
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*o = ManagedStartObservation(decoded)
	return nil
}
