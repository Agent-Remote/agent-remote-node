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

// SkillDeploymentLease authorizes one exact input and current poll attempt for a bounded duration.
type SkillDeploymentLease struct {
	skillmanager.SkillDeploymentIdentity
	LeaseAttempt           int64     `json:"lease_attempt"`
	ServerTime             time.Time `json:"server_time"`
	LeaseUntil             time.Time `json:"lease_until"`
	RenewAfterMilliseconds int64     `json:"renew_after_milliseconds"`
}

// RenewSkillDeploymentLease never revives an expired or replaced deployment and never retries a write.
func (c Client) RenewSkillDeploymentLease(ctx context.Context, binding skillmanager.SkillDeploymentIdentity, attempt int64) (SkillDeploymentLease, error) {
	if err := validateDeploymentRequest(binding, attempt); err != nil {
		return SkillDeploymentLease{}, err
	}
	body, err := json.Marshal(struct {
		LeaseAttempt int64 `json:"lease_attempt"`
	}{attempt})
	if err != nil {
		return SkillDeploymentLease{}, err
	}
	var response skillEnvelope[SkillDeploymentLease]
	if err := c.skillRequest(ctx, http.MethodPost, deploymentTaskPath(binding, "/lease"), "application/json", bytes.NewReader(body), &response); err != nil {
		return SkillDeploymentLease{}, err
	}
	lease := response.Data
	duration := lease.LeaseUntil.Sub(lease.ServerTime)
	if response.SchemaVersion != 1 || response.Status != "leased" || response.Committed == nil || *response.Committed || len(response.Errors) != 0 ||
		lease.SkillDeploymentIdentity != binding || lease.LeaseAttempt != attempt || lease.ServerTime.IsZero() || lease.LeaseUntil.IsZero() ||
		duration <= 0 || duration > 300*time.Second || lease.RenewAfterMilliseconds <= 0 || lease.RenewAfterMilliseconds >= duration.Milliseconds() {
		return SkillDeploymentLease{}, errors.New("invalid deployment lease acknowledgement")
	}
	return lease, nil
}

// UnmarshalJSON requires all canonical lease fields and exact integer types.
func (l *SkillDeploymentLease) UnmarshalJSON(data []byte) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	names := []string{"operation_id", "attempt_id", "task_id", "user_id", "account_id", "node_id", "checkpoint_id", "plan_digest", "tree_digest", "runtime_backend",
		"lease_attempt", "server_time", "lease_until", "renew_after_milliseconds"}
	if len(fields) != len(names) {
		return errors.New("invalid deployment lease fields")
	}
	for _, name := range names {
		if fields[name] == nil || bytes.Equal(bytes.TrimSpace(fields[name]), []byte("null")) {
			return errors.New("missing deployment lease field")
		}
	}
	type plain SkillDeploymentLease
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*l = SkillDeploymentLease(decoded)
	return nil
}
