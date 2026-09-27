package skillmanager

import (
	"encoding/json"
	"errors"
)

// ErrDeploymentDrained denies all future preparation under an original sealed attempt.
var ErrDeploymentDrained = errors.New("skill deployment attempt is permanently drained")

// DeploymentDrain records permanent local preparation exclusion, not Server cancellation or cleanup.
type DeploymentDrain struct {
	Version         int                     `json:"version"`
	Binding         SkillDeploymentIdentity `json:"binding"`
	HelperReceiptID string                  `json:"helper_receipt_id"`
}

// Validate requires the exact original deployment binding and a durable Helper receipt identity.
func (r DeploymentDrain) Validate(expected SkillDeploymentIdentity) error {
	if err := expected.Validate(); err != nil {
		return err
	}
	if r.Version != 1 || r.Binding != expected || !validSkillUUID(r.HelperReceiptID) {
		return errors.New("deployment drain differs from original binding")
	}
	return nil
}

// UnmarshalJSON rejects missing, aliased, duplicate, null and unknown receipt or binding fields.
func (r *DeploymentDrain) UnmarshalJSON(data []byte) error {
	type plain DeploymentDrain
	var decoded plain
	if err := decodeDeploymentFields(data, &decoded, "version", "binding", "helper_receipt_id"); err != nil {
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
	*r = DeploymentDrain(decoded)
	return nil
}
