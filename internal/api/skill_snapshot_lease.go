package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// SkillSnapshotLease is one short-lived authorization for the current poll attempt.
// ServerTime and LeaseUntil describe a duration; callers must not compare host wall clocks.
type SkillSnapshotLease struct {
	skillmanager.SkillSnapshotIdentity
	LeaseAttempt           int64     `json:"lease_attempt"`
	ServerTime             time.Time `json:"server_time"`
	LeaseUntil             time.Time `json:"lease_until"`
	RenewAfterMilliseconds int64     `json:"renew_after_milliseconds"`
}

// RenewSkillSnapshotLease extends only an unexpired, exact task's current poll attempt.
func (c Client) RenewSkillSnapshotLease(ctx context.Context, binding skillmanager.SkillSnapshotIdentity, attempt int64) (SkillSnapshotLease, error) {
	if err := binding.Validate(); err != nil {
		return SkillSnapshotLease{}, err
	}
	if attempt <= 0 || attempt > 2147483647 {
		return SkillSnapshotLease{}, errors.New("invalid snapshot lease attempt")
	}
	body, err := json.Marshal(struct {
		LeaseAttempt int64 `json:"lease_attempt"`
	}{attempt})
	if err != nil {
		return SkillSnapshotLease{}, err
	}
	var response skillEnvelope[SkillSnapshotLease]
	if err := c.skillRequest(ctx, http.MethodPost, snapshotPath(binding, "/lease"), "application/json", bytes.NewReader(body), &response); err != nil {
		return SkillSnapshotLease{}, err
	}
	lease := response.Data
	duration := lease.LeaseUntil.Sub(lease.ServerTime)
	if response.SchemaVersion != 1 || response.Status != "leased" || response.Committed == nil || *response.Committed || len(response.Errors) != 0 ||
		lease.SkillSnapshotIdentity != binding || lease.LeaseAttempt != attempt ||
		lease.ServerTime.IsZero() || lease.LeaseUntil.IsZero() || duration <= 0 || duration > 300*time.Second ||
		lease.RenewAfterMilliseconds <= 0 || lease.RenewAfterMilliseconds >= duration.Milliseconds() {
		return SkillSnapshotLease{}, errors.New("invalid snapshot lease acknowledgement")
	}
	return lease, nil
}

// UnmarshalJSON rejects incomplete, null and case-aliased lease authority fields.
func (l *SkillSnapshotLease) UnmarshalJSON(data []byte) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	names := []string{"snapshot_id", "task_id", "node_id", "user_id", "account_id", "session_id", "runtime_backend", "lease_attempt", "server_time", "lease_until", "renew_after_milliseconds"}
	if len(fields) != len(names) {
		return errors.New("invalid snapshot lease fields")
	}
	for _, name := range names {
		if fields[name] == nil || bytes.Equal(bytes.TrimSpace(fields[name]), []byte("null")) {
			return errors.New("missing snapshot lease field")
		}
	}
	type plain SkillSnapshotLease
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*l = SkillSnapshotLease(decoded)
	return nil
}
