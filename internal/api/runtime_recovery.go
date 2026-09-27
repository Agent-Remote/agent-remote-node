package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/Agent-Remote/agent-remote-node/internal/accountmigration"
)

// AuthorizeRuntimeRecovery fetches fresh original-task authority for one leased check.
func (c Client) AuthorizeRuntimeRecovery(ctx context.Context, taskID string) (accountmigration.Authorization, error) {
	var response struct {
		Data accountmigration.Authorization `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/node-api/tasks/"+url.PathEscape(taskID)+"/runtime-migration-recovery-authorization", nil, &response, true); err != nil {
		return response.Data, err
	}
	grant := response.Data
	if grant.Binding.Validate() != nil || grant.Binding.TaskID != taskID || grant.LeaseAttempt <= 0 {
		return accountmigration.Authorization{}, errors.New("invalid runtime migration recovery authorization")
	}
	return grant, nil
}
