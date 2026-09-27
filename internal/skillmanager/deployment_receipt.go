package skillmanager

import (
	"encoding/json"
	"errors"
)

// DeploymentPreparation records complete local preparation independently of Server readiness.
type DeploymentPreparation struct {
	Version         int                     `json:"version"`
	Binding         SkillDeploymentIdentity `json:"binding"`
	InputDigest     string                  `json:"input_digest"`
	DirectoryEpoch  int64                   `json:"directory_epoch"`
	Generation      int64                   `json:"generation"`
	HelperReceiptID string                  `json:"helper_receipt_id"`
}

// Validate requires the complete original input; a matching tree alone cannot substitute a plan.
func (r DeploymentPreparation) Validate(input SkillDeployment) error {
	if err := r.ValidateBinding(); err != nil {
		return err
	}
	digest, err := input.InputDigest()
	if err != nil {
		return err
	}
	if r.Version != 1 || r.Binding != input.SkillDeploymentIdentity || r.InputDigest != digest ||
		r.DirectoryEpoch != input.DirectoryEpoch || r.Generation != input.Plan.Generation || !validSkillUUID(r.HelperReceiptID) {
		return errors.New("deployment preparation differs from original input")
	}
	return nil
}

// ValidateBinding checks bounded persisted receipt metadata without reconstructing the original input.
// It validates history for result transport, never content availability or current lease authority.
func (r DeploymentPreparation) ValidateBinding() error {
	if err := r.Binding.Validate(); err != nil {
		return err
	}
	if r.Version != 1 || !contentDigestPattern.MatchString(r.InputDigest) || r.DirectoryEpoch < 1 || r.Generation < 0 || !validSkillUUID(r.HelperReceiptID) {
		return errors.New("invalid deployment preparation receipt")
	}
	return nil
}

// UnmarshalJSON requires canonical receipt fields, including explicitly present zero generation.
func (r *DeploymentPreparation) UnmarshalJSON(data []byte) error {
	type plain DeploymentPreparation
	var decoded plain
	if err := decodeDeploymentFields(data, &decoded, "version", "binding", "input_digest", "directory_epoch", "generation", "helper_receipt_id"); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if err := decodeDeploymentFields(fields["binding"], &decoded.Binding, "operation_id", "attempt_id", "task_id",
		"user_id", "account_id", "node_id", "checkpoint_id", "plan_digest", "tree_digest", "runtime_backend"); err != nil {
		return err
	}
	*r = DeploymentPreparation(decoded)
	return nil
}
