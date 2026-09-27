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

// SkillTakeoverLease is one short-lived authorization for the current poll attempt.
// ServerTime and LeaseUntil describe a duration; callers must not compare host wall clocks.
type SkillTakeoverLease struct {
	TakeoverID             string    `json:"takeover_id"`
	TaskID                 string    `json:"task_id"`
	NodeID                 string    `json:"node_id"`
	LeaseAttempt           int64     `json:"lease_attempt"`
	ServerTime             time.Time `json:"server_time"`
	LeaseUntil             time.Time `json:"lease_until"`
	RenewAfterMilliseconds int64     `json:"renew_after_milliseconds"`
}

// RenewSkillTakeoverLease extends only an unexpired, exact task's current poll attempt.
func (c Client) RenewSkillTakeoverLease(ctx context.Context, binding skillmanager.AccountTakeoverBinding, attempt int64) (SkillTakeoverLease, error) {
	if err := binding.Validate(); err != nil {
		return SkillTakeoverLease{}, err
	}
	if attempt <= 0 || attempt > 2147483647 {
		return SkillTakeoverLease{}, errors.New("invalid takeover lease attempt")
	}
	body, err := json.Marshal(struct {
		LeaseAttempt int64 `json:"lease_attempt"`
	}{attempt})
	if err != nil {
		return SkillTakeoverLease{}, err
	}
	var response skillEnvelope[SkillTakeoverLease]
	if err := c.skillRequest(ctx, http.MethodPost, takeoverPath(binding, "/lease", ""), "application/json", bytes.NewReader(body), &response); err != nil {
		return SkillTakeoverLease{}, err
	}
	lease := response.Data
	duration := lease.LeaseUntil.Sub(lease.ServerTime)
	if response.SchemaVersion != 1 || response.Status != "leased" || response.Committed == nil || *response.Committed || len(response.Errors) != 0 ||
		lease.TakeoverID != binding.TakeoverID || lease.TaskID != binding.TaskID || lease.NodeID != binding.NodeID || lease.LeaseAttempt != attempt ||
		lease.ServerTime.IsZero() || lease.LeaseUntil.IsZero() || duration <= 0 || duration > 300*time.Second ||
		lease.RenewAfterMilliseconds <= 0 || lease.RenewAfterMilliseconds >= duration.Milliseconds() {
		return SkillTakeoverLease{}, errors.New("invalid takeover lease acknowledgement")
	}
	return lease, nil
}
