package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// SkillSnapshotIdentity binds authenticated downloads to the original snapshot owners.
type SkillSnapshotIdentity = skillmanager.SkillSnapshotIdentity

// SkillSnapshot contains the complete immutable preparation input.
type SkillSnapshot = skillmanager.SkillSnapshot

// SkillSnapshotItem identifies a materialized branch.
type SkillSnapshotItem = skillmanager.SkillSnapshotItem

// SkillSnapshotResolution preserves original rule resolution.
type SkillSnapshotResolution = skillmanager.SkillSnapshotResolution

// GetSkillSnapshot retrieves and verifies the complete original snapshot, without choosing a new one.
func (c Client) GetSkillSnapshot(ctx context.Context, expected SkillSnapshotIdentity) (SkillSnapshot, error) {
	if err := expected.Validate(); err != nil {
		return SkillSnapshot{}, err
	}
	var response skillEnvelope[SkillSnapshot]
	err := c.skillRequestLimit(ctx, http.MethodGet, snapshotPath(expected, ""), "", nil, &response, maxSkillCaptureBytes)
	if err != nil {
		return SkillSnapshot{}, err
	}
	if response.SchemaVersion != 1 || response.Status != "ready" || response.Committed == nil || *response.Committed || len(response.Errors) != 0 {
		return SkillSnapshot{}, errors.New("invalid skill snapshot response")
	}
	if err := response.Data.Validate(expected); err != nil {
		return SkillSnapshot{}, err
	}
	return response.Data, nil
}

func snapshotPath(binding SkillSnapshotIdentity, suffix string) string {
	return "/api/v1/node/skill-snapshots/" + binding.SnapshotID + suffix + "?task_id=" + url.QueryEscape(binding.TaskID)
}
