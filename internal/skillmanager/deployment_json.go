package skillmanager

import "encoding/json"

// UnmarshalJSON rejects missing, null, aliased and duplicate deployment input fields.
func (d *SkillDeployment) UnmarshalJSON(data []byte) error {
	type plain SkillDeployment
	var decoded plain
	if err := decodeDeploymentFields(data, &decoded,
		"operation_id", "attempt_id", "task_id", "user_id", "account_id", "node_id", "checkpoint_id",
		"plan_digest", "tree_digest", "runtime_backend", "directory_epoch", "plan", "manifest", "items"); err != nil {
		return err
	}
	*d = SkillDeployment(decoded)
	return nil
}

// UnmarshalJSON preserves explicit false enabled choices and source-specific epoch fields.
func (s *SkillDeploymentSource) UnmarshalJSON(data []byte) error {
	var discriminator struct {
		Origin string `json:"origin"`
	}
	if err := json.Unmarshal(data, &discriminator); err != nil {
		return err
	}
	names := []string{"origin", "source_id", "revision_id", "content_digest", "name", "enabled"}
	if discriminator.Origin == "library" {
		names = append(names, "installation_epoch")
	}
	type plain SkillDeploymentSource
	var decoded plain
	if err := decodeDeploymentFields(data, &decoded, names...); err != nil {
		return err
	}
	*s = SkillDeploymentSource(decoded)
	return nil
}

// UnmarshalJSON preserves exact int64 generations and all original plan fields.
func (p *SkillDeploymentPlan) UnmarshalJSON(data []byte) error {
	type plain SkillDeploymentPlan
	var decoded plain
	if err := decodeDeploymentFields(data, &decoded, "version", "user_id", "operation_id", "generation",
		"account_id", "node_id", "tool_type", "runtime_backend", "sources"); err != nil {
		return err
	}
	*p = SkillDeploymentPlan(decoded)
	return nil
}

// UnmarshalJSON requires the exact immutable member identity and branch epoch.
func (m *SkillDeploymentMember) UnmarshalJSON(data []byte) error {
	type plain SkillDeploymentMember
	var decoded plain
	if err := decodeDeploymentFields(data, &decoded, "entry_name", "state_id", "state_epoch", "checkpoint_id"); err != nil {
		return err
	}
	*m = SkillDeploymentMember(decoded)
	return nil
}

func decodeDeploymentFields(data []byte, out any, names ...string) error {
	if err := rejectReceiptDuplicates(data); err != nil {
		return err
	}
	return decodeSnapshotFields(data, out, "", names...)
}
