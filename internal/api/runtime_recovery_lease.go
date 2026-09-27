package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/accountmigration"
)

// RuntimeRecoveryLease binds a short renewal to the complete original authorization.
type RuntimeRecoveryLease struct {
	Authorization          accountmigration.Authorization `json:"authorization"`
	ServerTime             time.Time                      `json:"server_time"`
	LeaseUntil             time.Time                      `json:"lease_until"`
	RenewAfterMilliseconds int64                          `json:"renew_after_milliseconds"`
}

// RenewRuntimeRecoveryLease extends only an unexpired original recovery poll attempt.
func (c Client) RenewRuntimeRecoveryLease(ctx context.Context, expected accountmigration.Authorization) (RuntimeRecoveryLease, error) {
	var response struct {
		Data RuntimeRecoveryLease `json:"data"`
	}
	if expected.Binding.Validate() != nil || expected.LeaseAttempt <= 0 {
		return response.Data, errors.New("invalid runtime recovery lease authority")
	}
	path := "/api/v1/node-api/tasks/" + url.PathEscape(expected.Binding.TaskID) + "/runtime-migration-recovery-lease"
	client := *c.httpClient
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	c.httpClient = &client
	if err := c.do(ctx, http.MethodPost, path, expected, &response, true); err != nil {
		return response.Data, err
	}
	lease := response.Data
	duration := lease.LeaseUntil.Sub(lease.ServerTime)
	if lease.Authorization != expected || lease.ServerTime.IsZero() || lease.LeaseUntil.IsZero() || duration <= 0 || duration > 300*time.Second || lease.RenewAfterMilliseconds <= 0 || lease.RenewAfterMilliseconds >= duration.Milliseconds() {
		return RuntimeRecoveryLease{}, errors.New("invalid runtime recovery lease acknowledgement")
	}
	return lease, nil
}
