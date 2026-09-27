package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// SkillDeploymentPreparedResult binds durable Helper preparation to one original poll proposal.
type SkillDeploymentPreparedResult struct {
	LeaseAttempt int64                              `json:"lease_attempt"`
	Preparation  skillmanager.DeploymentPreparation `json:"preparation"`
}

// Validate checks metadata for confirmation or read-only recovery, never current execution authority.
func (r SkillDeploymentPreparedResult) Validate() error {
	if r.LeaseAttempt < 1 || r.LeaseAttempt > 2147483647 {
		return errors.New("invalid deployment result poll attempt")
	}
	return r.Preparation.ValidateBinding()
}

// SkillDeploymentResultObservation reports exact historical acceptance without granting a lease.
type SkillDeploymentResultObservation struct {
	Result              SkillDeploymentPreparedResult `json:"result"`
	Accepted            bool                          `json:"accepted"`
	CurrentLeaseAttempt int64                         `json:"current_lease_attempt"`
	TaskStatus          string                        `json:"task_status"`
}

// ConfirmSkillDeployment submits only an exact durable preparation and never automatically retries.
func (c Client) ConfirmSkillDeployment(ctx context.Context, result SkillDeploymentPreparedResult) (SkillDeploymentResultObservation, error) {
	return c.deploymentResult(ctx, result, false)
}

// InspectSkillDeployment observes the original proposal without renewing or publishing readiness.
func (c Client) InspectSkillDeployment(ctx context.Context, result SkillDeploymentPreparedResult) (SkillDeploymentResultObservation, error) {
	return c.deploymentResult(ctx, result, true)
}

func (c Client) deploymentResult(ctx context.Context, result SkillDeploymentPreparedResult, inspect bool) (SkillDeploymentResultObservation, error) {
	if err := result.Validate(); err != nil {
		return SkillDeploymentResultObservation{}, err
	}
	suffix, status := "/result", "confirmed"
	if inspect {
		suffix, status = "/result/inspect", "observed"
	}
	data, err := json.Marshal(result)
	if err != nil {
		return SkillDeploymentResultObservation{}, err
	}
	var response skillEnvelope[SkillDeploymentResultObservation]
	if err := c.skillRequest(ctx, http.MethodPost, deploymentTaskPath(result.Preparation.Binding, suffix), "application/json", bytes.NewReader(data), &response); err != nil {
		return SkillDeploymentResultObservation{}, err
	}
	observation := response.Data
	if response.SchemaVersion != 1 || response.Status != status || response.Committed == nil || *response.Committed == inspect || len(response.Errors) != 0 ||
		observation.Result != result || observation.CurrentLeaseAttempt < result.LeaseAttempt || observation.CurrentLeaseAttempt > 2147483647 ||
		!inspect && !observation.Accepted {
		return SkillDeploymentResultObservation{}, errors.New("invalid deployment result acknowledgement")
	}
	if observation.Accepted {
		if observation.TaskStatus != "succeeded" || observation.CurrentLeaseAttempt != result.LeaseAttempt {
			return SkillDeploymentResultObservation{}, errors.New("deployment receipt has inconsistent terminal state")
		}
	} else {
		switch observation.TaskStatus {
		case "pending", "leased", "running", "cancelled", "expired":
		default:
			return SkillDeploymentResultObservation{}, errors.New("deployment absence has inconsistent task state")
		}
	}
	return observation, nil
}

// UnmarshalJSON rejects missing/null/case-aliased fields and duplicate proposal authority.
func (r *SkillDeploymentPreparedResult) UnmarshalJSON(data []byte) error {
	type plain SkillDeploymentPreparedResult
	var decoded plain
	if err := decodeDeploymentResultFields(data, &decoded, "lease_attempt", "preparation"); err != nil {
		return err
	}
	*r = SkillDeploymentPreparedResult(decoded)
	return r.Validate()
}

// UnmarshalJSON requires an explicit acceptance boolean and complete original result observation.
func (o *SkillDeploymentResultObservation) UnmarshalJSON(data []byte) error {
	type plain SkillDeploymentResultObservation
	var decoded plain
	if err := decodeDeploymentResultFields(data, &decoded, "result", "accepted", "current_lease_attempt", "task_status"); err != nil {
		return err
	}
	*o = SkillDeploymentResultObservation(decoded)
	return nil
}

func decodeDeploymentResultFields(data []byte, out any, names ...string) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if len(fields) != len(names) {
		return errors.New("invalid deployment result fields")
	}
	for _, name := range names {
		if fields[name] == nil || bytes.Equal(bytes.TrimSpace(fields[name]), []byte("null")) {
			return errors.New("missing deployment result field")
		}
	}
	return json.Unmarshal(data, out)
}
