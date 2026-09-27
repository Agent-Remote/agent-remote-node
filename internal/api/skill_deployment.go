package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// GetSkillDeployment fetches only the original task's full prepared directory under its current lease.
func (c Client) GetSkillDeployment(ctx context.Context, binding skillmanager.SkillDeploymentIdentity, attempt int64) (skillmanager.SkillDeployment, error) {
	if err := validateDeploymentRequest(binding, attempt); err != nil {
		return skillmanager.SkillDeployment{}, err
	}
	var response skillEnvelope[skillmanager.SkillDeployment]
	if err := c.skillRequestLimit(ctx, http.MethodGet, deploymentPath(binding, attempt, ""), "", nil, &response, maxSkillCaptureBytes); err != nil {
		return skillmanager.SkillDeployment{}, err
	}
	if response.SchemaVersion != 1 || response.Status != "prepared_input" || response.Committed == nil || *response.Committed || len(response.Errors) != 0 {
		return skillmanager.SkillDeployment{}, errors.New("invalid deployment input response")
	}
	if err := response.Data.Validate(binding); err != nil {
		return skillmanager.SkillDeployment{}, err
	}
	return response.Data, nil
}

func deploymentPath(binding skillmanager.SkillDeploymentIdentity, attempt int64, suffix string) string {
	return deploymentTaskPath(binding, suffix) + "&lease_attempt=" + strconv.FormatInt(attempt, 10)
}

func validateDeploymentRequest(binding skillmanager.SkillDeploymentIdentity, attempt int64) error {
	if err := binding.Validate(); err != nil {
		return err
	}
	if attempt < 1 || attempt > 2147483647 {
		return errors.New("invalid deployment poll attempt")
	}
	return nil
}

func deploymentTaskPath(binding skillmanager.SkillDeploymentIdentity, suffix string) string {
	return "/api/v1/node/skill-deployments/" + binding.AttemptID + suffix + "?task_id=" + url.QueryEscape(binding.TaskID)
}
