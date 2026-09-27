package api

import (
	"context"
	"io"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// ReadSkillDeploymentFile streams an exact original manifest member into private, unpublished staging.
// The caller discards all staging on failure; verified bytes alone are not a deployment result.
func (c Client) ReadSkillDeploymentFile(ctx context.Context, binding skillmanager.SkillDeploymentIdentity, attempt int64, entry skillmanager.Entry, target io.Writer) error {
	if err := validateDeploymentRequest(binding, attempt); err != nil {
		return err
	}
	return c.readSkillFile(ctx, deploymentPath(binding, attempt, "/files/"+entry.SHA256), entry, target)
}
