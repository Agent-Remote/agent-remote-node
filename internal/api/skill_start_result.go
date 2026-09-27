package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

const managedStartStoppedMessage = "Managed startup stopped; skill finalization is pending."

// ManagedSessionStartResult is the bounded, snapshot-bound outcome retained by the Server.
// A committed receipt is historical task acceptance, not a later runtime liveness claim.
type ManagedSessionStartResult struct {
	SessionID         string `json:"session_id"`
	AccountID         string `json:"tool_account_id"`
	RuntimeBackend    string `json:"runtime_backend"`
	RuntimeResourceID string `json:"runtime_resource_id"`
	SnapshotID        string `json:"skill_snapshot_id"`
	TaskRecordID      string `json:"task_record_id"`
	LeaseAttempt      int64  `json:"lease_attempt"`
	Status            string `json:"status,omitempty"`
	ToolType          string `json:"tool_type,omitempty"`
	TmuxSessionName   string `json:"tmux_session_name,omitempty"`
	Code              string `json:"code,omitempty"`
	Message           string `json:"message,omitempty"`
}

// NewManagedSessionStartResult formats an already verified Helper outcome without paths or credentials.
// It does not establish readiness or writer quiescence; callers must obtain that evidence first.
func NewManagedSessionStartResult(binding SkillSnapshotIdentity, attempt int64, status, tmux string) (ManagedSessionStartResult, error) {
	digest := sha256.Sum256([]byte(binding.SessionID))
	result := ManagedSessionStartResult{
		SessionID: binding.SessionID, AccountID: binding.AccountID, RuntimeBackend: binding.RuntimeBackend,
		RuntimeResourceID: "agent-remote-session-" + hex.EncodeToString(digest[:6]) + ".service",
		SnapshotID:        binding.SnapshotID, TaskRecordID: binding.TaskID, LeaseAttempt: attempt,
	}
	switch status {
	case "running":
		result.Status, result.ToolType, result.TmuxSessionName = status, "claude", tmux
	case "stopped":
		if tmux != "" {
			return ManagedSessionStartResult{}, errors.New("stopped startup result cannot carry terminal metadata")
		}
		result.Code, result.Message = "SKILL_START_STOPPED", managedStartStoppedMessage
	default:
		return ManagedSessionStartResult{}, errors.New("unsupported managed startup outcome")
	}
	if err := result.validate(binding, attempt); err != nil {
		return ManagedSessionStartResult{}, err
	}
	return result, nil
}

// ConfirmManagedSessionStart confirms exactly one original outcome without retrying uncertain writes.
// The same payload may be explicitly retried after a lost acknowledgement; changed outcomes conflict.
func (c Client) ConfirmManagedSessionStart(ctx context.Context, logicalTaskID string, binding SkillSnapshotIdentity, attempt int64, outcome ManagedSessionStartResult) (ManagedSessionStartResult, error) {
	path, err := managedStartResultPath(logicalTaskID)
	if err != nil {
		return ManagedSessionStartResult{}, err
	}
	if err := outcome.validate(binding, attempt); err != nil {
		return ManagedSessionStartResult{}, err
	}
	body, err := json.Marshal(outcome)
	if err != nil {
		return ManagedSessionStartResult{}, err
	}
	var response skillEnvelope[ManagedSessionStartResult]
	if err := c.skillRequest(ctx, http.MethodPost, path, "application/json", bytes.NewReader(body), &response); err != nil {
		return ManagedSessionStartResult{}, err
	}
	if response.SchemaVersion != 1 || response.Status != "completed" || response.Committed == nil || !*response.Committed || len(response.Errors) != 0 || response.Data != outcome {
		return ManagedSessionStartResult{}, errors.New("managed startup confirmation does not match its original outcome")
	}
	return response.Data, nil
}

func managedStartResultPath(logicalTaskID string) (string, error) {
	if logicalTaskID == "" || len(logicalTaskID) > 128 || strings.ContainsAny(logicalTaskID, "/\\\x00\r\n") {
		return "", errors.New("invalid managed startup logical task identity")
	}
	return "/api/v1/node-api/tasks/" + url.PathEscape(logicalTaskID) + "/managed-start-result", nil
}

func (r ManagedSessionStartResult) validate(binding SkillSnapshotIdentity, attempt int64) error {
	digest := sha256.Sum256([]byte(binding.SessionID))
	if binding.Validate() != nil || binding.RuntimeBackend != "native" || attempt <= 0 || attempt > 2147483647 ||
		r.SessionID != binding.SessionID || r.AccountID != binding.AccountID || r.RuntimeBackend != binding.RuntimeBackend ||
		r.SnapshotID != binding.SnapshotID || r.TaskRecordID != binding.TaskID || r.LeaseAttempt != attempt ||
		r.RuntimeResourceID != "agent-remote-session-"+hex.EncodeToString(digest[:6])+".service" {
		return errors.New("invalid managed startup result identity")
	}
	if r.Status == "running" && r.ToolType == "claude" && r.TmuxSessionName != "" && len(r.TmuxSessionName) <= 128 &&
		!strings.ContainsAny(r.TmuxSessionName, "\x00\r\n") && r.Code == "" && r.Message == "" {
		return nil
	}
	if r.Code == "SKILL_START_STOPPED" && r.Message == managedStartStoppedMessage && r.Status == "" && r.ToolType == "" && r.TmuxSessionName == "" {
		return nil
	}
	return errors.New("invalid managed startup outcome")
}

// UnmarshalJSON rejects incomplete, aliased, duplicate and null acknowledgement fields.
func (r *ManagedSessionStartResult) UnmarshalJSON(data []byte) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	names := []string{"session_id", "tool_account_id", "runtime_backend", "runtime_resource_id", "skill_snapshot_id", "task_record_id", "lease_attempt"}
	if _, ready := fields["status"]; ready {
		names = append(names, "status", "tool_type", "tmux_session_name")
	} else {
		names = append(names, "code", "message")
	}
	if len(fields) != len(names) {
		return errors.New("invalid managed startup receipt fields")
	}
	for _, name := range names {
		if fields[name] == nil || bytes.Equal(bytes.TrimSpace(fields[name]), []byte("null")) {
			return errors.New("missing managed startup receipt field")
		}
	}
	type plain ManagedSessionStartResult
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*r = ManagedSessionStartResult(decoded)
	return nil
}
