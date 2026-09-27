package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

type skillCapturePending struct {
	SessionID         string `json:"session_id"`
	TaskID            string `json:"task_id"`
	InitialTreeDigest string `json:"initial_tree_digest"`
	DirectoryEpoch    int64  `json:"directory_epoch"`
	LibraryGeneration int64  `json:"library_generation"`
	CaptureError      string `json:"capture_error"`
	Unclean           bool   `json:"unclean"`
}

// UnmarshalJSON rejects missing, aliased, duplicate and null capture-pending receipt fields.
func (r *skillCapturePending) UnmarshalJSON(data []byte) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	names := []string{"session_id", "task_id", "initial_tree_digest", "directory_epoch", "library_generation", "capture_error", "unclean"}
	if len(fields) != len(names) {
		return errors.New("invalid capture-pending receipt fields")
	}
	for _, name := range names {
		if fields[name] == nil || bytes.Equal(bytes.TrimSpace(fields[name]), []byte("null")) {
			return errors.New("missing capture-pending receipt field")
		}
	}
	type plain skillCapturePending
	return json.Unmarshal(data, (*plain)(r))
}

// ObserveSkillCapturePending confirms stopped writers while content capture remains blocked.
// The stopped receipt is not content persistence, publication, or replacement launch authority.
func (c Client) ObserveSkillCapturePending(ctx context.Context, capture skillmanager.CaptureFailure) error {
	if err := capture.Validate(); err != nil {
		return err
	}
	if !validSkillUUID(capture.Binding.TaskID) {
		return errors.New("capture-pending requires original managed task")
	}
	binding := capture.Binding
	payload := skillCapturePending{SessionID: binding.SessionID, TaskID: binding.TaskID, InitialTreeDigest: binding.InitialTreeDigest,
		DirectoryEpoch: binding.DirectoryEpoch, LibraryGeneration: binding.LibraryGeneration, CaptureError: capture.Code, Unclean: capture.Unclean}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var response skillEnvelope[skillCapturePending]
	if err := c.skillRequest(ctx, http.MethodPost, "/api/v1/node/skill-snapshots/"+binding.SnapshotID+"/capture-pending", "application/json", bytes.NewReader(body), &response); err != nil {
		return err
	}
	if response.SchemaVersion != 1 || response.Committed == nil || !*response.Committed || response.Status != "stopped" || len(response.Errors) != 0 || response.Data != payload {
		return errors.New("capture-pending receipt differs from original stopped runtime")
	}
	return nil
}
