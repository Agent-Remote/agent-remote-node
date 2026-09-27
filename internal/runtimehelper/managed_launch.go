package runtimehelper

import (
	"context"
	"errors"
	"path"
	"strings"
)

const managedLaunchOperation = "start_managed_session"
const managedRecoveryOperation = "recover_managed_session"
const managedCancelOperation = "cancel_managed_session"

var errManagedStartPending = errors.New("SKILL_START_PENDING")
var errManagedStartStopped = errors.New("SKILL_START_STOPPED")

// CancelManagedSession drains only the exact original launch and retains its skill finalization.
// Callers use a fresh bounded context when their startup lease or connection has already expired.
func (c Client) CancelManagedSession(ctx context.Context, requestID string, input ManagedSessionSpecRequest) error {
	if err := input.validate(input.Snapshot.NodeID); err != nil {
		return err
	}
	payload, err := Map(input)
	if err != nil {
		return err
	}
	result, err := c.Call(ctx, requestID, managedCancelOperation, payload)
	if err != nil {
		return err
	}
	if len(result) != 5 || (result["status"] != "stopped" && result["status"] != "not_started") || result["session_id"] != input.Snapshot.SessionID || result["skill_snapshot_id"] != input.Snapshot.SnapshotID || result["task_record_id"] != input.Snapshot.TaskID || result["runtime_backend"] != "native" {
		return errors.New("managed session cancellation response is inconsistent")
	}
	return nil
}

// RecoverManagedSession verifies a live original launch before the worker repeats preparation.
// A not_started result permits exact preparation only; it is not permission to bypass startup checks.
// Historical readiness alone is insufficient: stopped runtimes require retained-state finalization.
func (c Client) RecoverManagedSession(ctx context.Context, requestID string, input ManagedSessionSpecRequest) (map[string]any, error) {
	if err := input.validate(input.Snapshot.NodeID); err != nil {
		return nil, err
	}
	payload, err := Map(input)
	if err != nil {
		return nil, err
	}
	result, err := c.Call(ctx, requestID, managedRecoveryOperation, payload)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	if result["status"] == "not_started" {
		if len(result) != 5 || result["session_id"] != input.Snapshot.SessionID || result["skill_snapshot_id"] != input.Snapshot.SnapshotID || result["task_record_id"] != input.Snapshot.TaskID || result["runtime_backend"] != "native" {
			return nil, errors.New("managed session recovery response is inconsistent")
		}
	} else if !validManagedLaunchResult(result, input) {
		return nil, errors.New("managed session recovery response is inconsistent")
	}
	return result, nil
}

// StartManagedSession starts a prepared Native session at most once, or replays its original receipt.
// A pending error is not permission to relaunch or discard the retained session work.
func (c Client) StartManagedSession(ctx context.Context, requestID string, input ManagedSessionSpecRequest) (map[string]any, error) {
	if err := input.validate(input.Snapshot.NodeID); err != nil {
		return nil, err
	}
	payload, err := Map(input)
	if err != nil {
		return nil, err
	}
	result, err := c.Call(ctx, requestID, managedLaunchOperation, payload)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	if !validManagedLaunchResult(result, input) {
		return nil, errors.New("managed session launch response is inconsistent")
	}
	return result, nil
}

func validManagedLaunchResult(result map[string]any, input ManagedSessionSpecRequest) bool {
	expected := map[string]any{
		"status": "running", "session_id": input.Snapshot.SessionID,
		"tool_account_id": input.Snapshot.AccountID, "tool_type": "claude",
		"tmux_session_name": input.Session.TmuxSessionName, "tmux_started": true,
		"sandbox_name": "", "container_id": "", "runtime_backend": "native",
		"runtime_resource_id": "agent-remote-session-" + shortDigest(input.Snapshot.SessionID, 12) + ".service",
		"skill_snapshot_id":   input.Snapshot.SnapshotID, "task_record_id": input.Snapshot.TaskID,
	}
	if len(result) != len(expected)+3 {
		return false
	}
	for field, value := range expected {
		if result[field] != value {
			return false
		}
	}
	uid, ok := result["runtime_uid"].(float64)
	if !ok || uid <= 0 || uid >= 1<<32 || uid != float64(uint32(uid)) {
		return false
	}
	return managedLaunchPath(result["workspace_remote_path"], input.Snapshot.UserID, "workspaces", input.Session.WorkspaceID, "files") &&
		managedLaunchPath(result["account_remote_path"], input.Snapshot.UserID, "tool-accounts", "claude", input.Snapshot.AccountID)
}

func managedLaunchPath(value any, suffix ...string) bool {
	directory, ok := value.(string)
	return ok && path.IsAbs(directory) && path.Clean(directory) == directory && !strings.ContainsRune(directory, '\x00') &&
		strings.HasSuffix(directory, "/"+path.Join(suffix...))
}
