package skillmanager

import (
	"bytes"
	"encoding/json"
	"errors"
)

// UnmarshalJSON preserves explicit zero generations and rejects missing or aliased fixed inputs.
func (s *SkillSnapshot) UnmarshalJSON(data []byte) error {
	type plain SkillSnapshot
	var decoded plain
	if err := decodeSnapshotFields(data, &decoded, "starting_checkpoint_id",
		"snapshot_id", "task_id", "node_id", "user_id", "account_id", "session_id", "runtime_backend",
		"library_generation", "directory_epoch", "starting_checkpoint_id", "tree_digest", "manifest", "items", "system_releases"); err != nil {
		return err
	}
	*s = SkillSnapshot(decoded)
	return nil
}

// UnmarshalJSON rejects incomplete or case-aliased member identities before preparation.
func (s *SkillSnapshotItem) UnmarshalJSON(data []byte) error {
	type plain SkillSnapshotItem
	var decoded plain
	if err := decodeSnapshotFields(data, &decoded, "", "state_id", "entry_name", "state_epoch", "checkpoint_id", "resolution"); err != nil {
		return err
	}
	*s = SkillSnapshotItem(decoded)
	return nil
}

// UnmarshalJSON requires one explicit resolution without Go's case-insensitive field substitution.
func (s *SkillSnapshotResolution) UnmarshalJSON(data []byte) error {
	type plain SkillSnapshotResolution
	var decoded plain
	if err := decodeSnapshotFields(data, &decoded, "exclusion_reason", "enabled", "revision_id", "enabled_source", "revision_source", "eligible", "included", "exclusion_reason"); err != nil {
		return err
	}
	*s = SkillSnapshotResolution(decoded)
	return nil
}

func decodeSnapshotFields(data []byte, target any, nullable string, names ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if len(fields) != len(names) {
		return errors.New("skill snapshot has missing or unrecognized fields")
	}
	for _, name := range names {
		raw, present := fields[name]
		if !present || name != nullable && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return errors.New("skill snapshot omitted a fixed input")
		}
	}
	return json.Unmarshal(data, target)
}
