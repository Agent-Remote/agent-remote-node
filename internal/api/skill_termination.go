package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

type skillTermination struct {
	SessionID         string `json:"session_id"`
	TaskID            string `json:"task_id"`
	InitialTreeDigest string `json:"initial_tree_digest"`
	DirectoryEpoch    int64  `json:"directory_epoch"`
	LibraryGeneration int64  `json:"library_generation"`
	IncomingDigest    string `json:"incoming_digest"`
	Unclean           bool   `json:"unclean"`
}

// UnmarshalJSON rejects missing, aliased, duplicate and null termination receipt fields.
func (r *skillTermination) UnmarshalJSON(data []byte) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	names := []string{"session_id", "task_id", "initial_tree_digest", "directory_epoch", "library_generation", "incoming_digest", "unclean"}
	if len(fields) != len(names) {
		return errors.New("invalid termination receipt fields")
	}
	for _, name := range names {
		if fields[name] == nil || bytes.Equal(bytes.TrimSpace(fields[name]), []byte("null")) {
			return errors.New("missing termination receipt field")
		}
	}
	type plain skillTermination
	return json.Unmarshal(data, (*plain)(r))
}

// ObserveSkillTermination confirms one original frozen runtime before its first upload attempt.
// The stopped receipt is not content persistence, publication, or replacement launch authority.
func (c Client) ObserveSkillTermination(ctx context.Context, capture skillmanager.FinalizationRecord) error {
	if err := capture.Validate(); err != nil {
		return err
	}
	if capture.ObjectsVersion != 1 || !validSkillUUID(capture.Binding.TaskID) {
		return errors.New("termination requires original managed task and frozen objects")
	}
	binding := capture.Binding
	payload := skillTermination{SessionID: binding.SessionID, TaskID: binding.TaskID, InitialTreeDigest: binding.InitialTreeDigest,
		DirectoryEpoch: binding.DirectoryEpoch, LibraryGeneration: binding.LibraryGeneration, IncomingDigest: capture.TreeDigest, Unclean: capture.Unclean}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var response skillEnvelope[skillTermination]
	if err := c.skillRequest(ctx, http.MethodPost, "/api/v1/node/skill-snapshots/"+binding.SnapshotID+"/termination", "application/json", bytes.NewReader(body), &response); err != nil {
		return err
	}
	if response.SchemaVersion != 1 || response.Committed == nil || !*response.Committed || response.Status != "stopped" || len(response.Errors) != 0 || response.Data != payload {
		return errors.New("termination receipt differs from original frozen runtime")
	}
	return nil
}
