package skillmanager

import (
	"errors"
)

// SkillDeploymentIdentity pins an independent deployment input without inventing a session.
type SkillDeploymentIdentity struct {
	OperationID    string `json:"operation_id"`
	AttemptID      string `json:"attempt_id"`
	TaskID         string `json:"task_id"`
	UserID         string `json:"user_id"`
	AccountID      string `json:"account_id"`
	NodeID         string `json:"node_id"`
	CheckpointID   string `json:"checkpoint_id"`
	PlanDigest     string `json:"plan_digest"`
	TreeDigest     string `json:"tree_digest"`
	RuntimeBackend string `json:"runtime_backend"`
}

// Validate rejects arbitrary addressing before transport or Helper access.
func (b SkillDeploymentIdentity) Validate() error {
	for _, id := range []string{b.OperationID, b.AttemptID, b.TaskID, b.UserID, b.AccountID, b.NodeID, b.CheckpointID} {
		if !validSkillUUID(id) {
			return errors.New("invalid skill deployment identity")
		}
	}
	if b.RuntimeBackend != "native" || !contentDigestPattern.MatchString(b.PlanDigest) || !contentDigestPattern.MatchString(b.TreeDigest) {
		return errors.New("invalid skill deployment backend or digest")
	}
	return nil
}

// SkillDeployment holds the complete original prepared directory and source selection.
type SkillDeployment struct {
	SkillDeploymentIdentity
	DirectoryEpoch int64                   `json:"directory_epoch"`
	Plan           SkillDeploymentPlan     `json:"plan"`
	Manifest       Manifest                `json:"manifest"`
	Items          []SkillDeploymentMember `json:"items"`
}

// SkillDeploymentMember pins the original branch input, independently of later head progress.
type SkillDeploymentMember struct {
	EntryName    string `json:"entry_name"`
	StateID      string `json:"state_id"`
	StateEpoch   int64  `json:"state_epoch"`
	CheckpointID string `json:"checkpoint_id"`
}

// Validate checks every owner, plan, manifest and enabled member against the original task.
func (d SkillDeployment) Validate(expected SkillDeploymentIdentity) error {
	if err := expected.Validate(); err != nil {
		return err
	}
	if d.SkillDeploymentIdentity != expected || d.DirectoryEpoch < 1 || d.Items == nil ||
		d.Plan.OperationID != d.OperationID || d.Plan.UserID != d.UserID || d.Plan.AccountID != d.AccountID ||
		d.Plan.NodeID != d.NodeID || d.Plan.RuntimeBackend != d.RuntimeBackend {
		return errors.New("deployment input differs from original binding")
	}
	plan, err := d.Plan.Digest()
	if err != nil || plan != d.PlanDigest {
		return errors.New("invalid original deployment plan")
	}
	tree, err := Digest(d.Manifest)
	if err != nil || tree != d.TreeDigest {
		return errors.New("invalid deployment manifest identity")
	}
	selected := make(map[string]bool)
	for _, source := range d.Plan.Sources {
		if source.Enabled {
			selected[source.Name] = true
		}
	}
	entries := make(map[string]string, len(d.Manifest.Entries))
	for _, entry := range d.Manifest.Entries {
		entries[entry.Path] = entry.Kind
	}
	states := make(map[string]bool)
	for _, item := range d.Items {
		if !selected[item.EntryName] || !validSkillUUID(item.StateID) || !validSkillUUID(item.CheckpointID) ||
			item.StateEpoch < 1 || states[item.StateID] || entries[item.EntryName] != "directory" ||
			entries[item.EntryName+"/SKILL.md"] != "file" {
			return errors.New("invalid deployment directory member")
		}
		delete(selected, item.EntryName)
		states[item.StateID] = true
	}
	if len(selected) != 0 {
		return errors.New("deployment omitted an enabled member")
	}
	return nil
}

// InputDigest pins the complete validated input for durable local preparation.
func (d SkillDeployment) InputDigest() (string, error) {
	if err := d.Validate(d.SkillDeploymentIdentity); err != nil {
		return "", err
	}
	return deploymentJSONDigest(d)
}
