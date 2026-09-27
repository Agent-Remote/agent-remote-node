package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// SkillDeploymentTerminationRequest permanently revokes an original poll without asserting drain.
type SkillDeploymentTerminationRequest struct {
	LeaseAttempt int64  `json:"lease_attempt"`
	ErrorCode    string `json:"error_code"`
}

// Validate admits only bounded protocol failure codes and original positive poll attempts.
func (r SkillDeploymentTerminationRequest) Validate() error {
	_, known := deploymentFailureCode(r.ErrorCode)
	if !known || r.LeaseAttempt < 1 || r.LeaseAttempt > 2147483647 {
		return errors.New("invalid deployment termination request")
	}
	return nil
}

// SkillDeploymentTerminationIntent fixes the Server's original revocation and terminal classification.
type SkillDeploymentTerminationIntent struct {
	Version   int                                  `json:"version"`
	IntentID  string                               `json:"intent_id"`
	Binding   skillmanager.SkillDeploymentIdentity `json:"binding"`
	Request   SkillDeploymentTerminationRequest    `json:"request"`
	Outcome   string                               `json:"outcome"`
	ErrorCode string                               `json:"error_code"`
	Retryable bool                                 `json:"retryable"`
}

// Validate checks internal consistency without treating revocation as Helper drain or Server completion.
func (i SkillDeploymentTerminationIntent) Validate() error {
	if i.Version != 1 || !validSkillUUID(i.IntentID) || i.Binding.Validate() != nil || i.Request.Validate() != nil {
		return errors.New("invalid deployment termination intent")
	}
	retryable, _ := deploymentFailureCode(i.Request.ErrorCode)
	if i.Outcome == "superseded" {
		if i.ErrorCode != "OPERATION_SUPERSEDED" || i.Retryable {
			return errors.New("invalid superseded deployment intent")
		}
	} else if i.Outcome != "failed" || i.Request.ErrorCode == "OPERATION_SUPERSEDED" || i.ErrorCode != i.Request.ErrorCode || i.Retryable != retryable {
		return errors.New("invalid failed deployment intent")
	}
	return nil
}

// SkillDeploymentTerminatedResult associates an immutable Server intent with exact permanent local drain.
type SkillDeploymentTerminatedResult struct {
	Intent SkillDeploymentTerminationIntent `json:"intent"`
	Drain  skillmanager.DeploymentDrain     `json:"drain"`
}

// Validate rejects substituting any original identity or independently changing the failure category.
func (r SkillDeploymentTerminatedResult) Validate() error {
	if err := r.Intent.Validate(); err != nil {
		return err
	}
	return r.Drain.Validate(r.Intent.Binding)
}

// SkillDeploymentTerminationObservation reports original terminal acceptance without execution authority.
type SkillDeploymentTerminationObservation struct {
	Result              SkillDeploymentTerminatedResult `json:"result"`
	Accepted            bool                            `json:"accepted"`
	CurrentLeaseAttempt int64                           `json:"current_lease_attempt"`
	TaskStatus          string                          `json:"task_status"`
}

// RequestSkillDeploymentTermination persists revocation before a caller may request local drain.
func (c Client) RequestSkillDeploymentTermination(ctx context.Context, binding skillmanager.SkillDeploymentIdentity, request SkillDeploymentTerminationRequest) (SkillDeploymentTerminationIntent, error) {
	if binding.Validate() != nil || request.Validate() != nil {
		return SkillDeploymentTerminationIntent{}, errors.New("invalid deployment termination authority")
	}
	data, err := json.Marshal(request)
	if err != nil {
		return SkillDeploymentTerminationIntent{}, err
	}
	var response terminationEnvelope[SkillDeploymentTerminationIntent]
	if err := c.skillRequest(ctx, http.MethodPost, deploymentTaskPath(binding, "/termination"), "application/json", bytes.NewReader(data), &response); err != nil {
		return SkillDeploymentTerminationIntent{}, err
	}
	if !deploymentTerminationEnvelope(response.SchemaVersion, response.Status, response.Committed, len(response.Errors), "drain_required", true) || response.Data.Binding != binding || response.Data.Request != request || response.Data.Validate() != nil {
		return SkillDeploymentTerminationIntent{}, errors.New("invalid deployment revocation acknowledgement")
	}
	return response.Data, nil
}

// GetSkillDeploymentTermination recovers only the original durable intent and grants no lease.
func (c Client) GetSkillDeploymentTermination(ctx context.Context, binding skillmanager.SkillDeploymentIdentity) (*SkillDeploymentTerminationIntent, error) {
	if err := binding.Validate(); err != nil {
		return nil, err
	}
	var response terminationEnvelope[deploymentTerminationLookup]
	if err := c.skillRequest(ctx, http.MethodGet, deploymentTaskPath(binding, "/termination"), "", nil, &response); err != nil {
		return nil, err
	}
	if !response.Data.present || !deploymentTerminationEnvelope(response.SchemaVersion, response.Status, response.Committed, len(response.Errors), "observed", false) {
		return nil, errors.New("invalid deployment revocation observation")
	}
	intent := response.Data.Intent
	if intent != nil && (intent.Validate() != nil || intent.Binding != binding) {
		return nil, errors.New("deployment revocation changed original binding")
	}
	return intent, nil
}

// ConfirmSkillDeploymentTermination submits the exact durable drain under its original Server intent.
func (c Client) ConfirmSkillDeploymentTermination(ctx context.Context, result SkillDeploymentTerminatedResult) (SkillDeploymentTerminationObservation, error) {
	return c.deploymentTerminationResult(ctx, result, false)
}

// InspectSkillDeploymentTermination reads a prior terminal proposal without publishing a new outcome.
func (c Client) InspectSkillDeploymentTermination(ctx context.Context, result SkillDeploymentTerminatedResult) (SkillDeploymentTerminationObservation, error) {
	return c.deploymentTerminationResult(ctx, result, true)
}

func (c Client) deploymentTerminationResult(ctx context.Context, result SkillDeploymentTerminatedResult, inspect bool) (SkillDeploymentTerminationObservation, error) {
	if err := result.Validate(); err != nil {
		return SkillDeploymentTerminationObservation{}, err
	}
	suffix, status := "/termination/result", "confirmed"
	if inspect {
		suffix += "/inspect"
		status = "observed"
	}
	data, err := json.Marshal(result)
	if err != nil {
		return SkillDeploymentTerminationObservation{}, err
	}
	var response terminationEnvelope[SkillDeploymentTerminationObservation]
	if err := c.skillRequest(ctx, http.MethodPost, deploymentTaskPath(result.Intent.Binding, suffix), "application/json", bytes.NewReader(data), &response); err != nil {
		return SkillDeploymentTerminationObservation{}, err
	}
	observed := response.Data
	if !deploymentTerminationEnvelope(response.SchemaVersion, response.Status, response.Committed, len(response.Errors), status, !inspect) || observed.Result != result || observed.CurrentLeaseAttempt < result.Intent.Request.LeaseAttempt || observed.CurrentLeaseAttempt > 2147483647 || !inspect && !observed.Accepted {
		return SkillDeploymentTerminationObservation{}, errors.New("invalid deployment termination acknowledgement")
	}
	if observed.Accepted {
		expected := "failed"
		if result.Intent.Outcome == "superseded" {
			expected = "cancelled"
		}
		if observed.TaskStatus != expected {
			return SkillDeploymentTerminationObservation{}, errors.New("deployment termination has inconsistent terminal state")
		}
	} else {
		switch observed.TaskStatus {
		case "pending", "leased", "running", "expired":
		default:
			return SkillDeploymentTerminationObservation{}, errors.New("deployment drain absence has terminal task state")
		}
	}
	return observed, nil
}

func deploymentTerminationEnvelope(version int, status string, committed *bool, errorCount int, expected string, write bool) bool {
	return version == 1 && status == expected && committed != nil && *committed == write && errorCount == 0
}

func deploymentFailureCode(code string) (retryable, known bool) {
	switch code {
	case "NODE_UNAVAILABLE", "TRANSFER_FAILED", "QUOTA_EXCEEDED", "DEPLOYMENT_INTERRUPTED":
		return true, true
	case "AUTHORIZATION_DENIED", "SKILL_MANAGER_UNSUPPORTED", "DEPLOYMENT_INPUT_INVALID", "OPERATION_SUPERSEDED":
		return false, true
	default:
		return false, false
	}
}
