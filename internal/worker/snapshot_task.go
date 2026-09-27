package worker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"github.com/Agent-Remote/agent-remote-node/internal/toolsessions"
)

type managedSnapshotTask struct {
	binding skillmanager.SkillSnapshotIdentity
	session toolsessions.CreatePayload
}

func (w Worker) executeManagedStartup(ctx context.Context, task api.TaskEnvelope) error {
	input, err := decodeManagedSnapshotTask(task, w.cfg.NodeID)
	if err != nil {
		return errManagedSessionPending
	}
	previous, err := (managedStartJournal{ledger: w.ledger}).load(task.TaskID)
	if err != nil || previous != nil && previous.entry.Status == managedStartRetired {
		return errManagedSessionPending
	}
	if err := w.requireBackend(input.binding.RuntimeBackend); err != nil {
		return errManagedSessionPending
	}
	if err := w.managedAdmissions.lock(ctx); err != nil {
		return errManagedSessionPending
	}
	defer w.managedAdmissions.unlock()
	peer, err := w.prepareManagedSessionPeer(input.session)
	if err != nil {
		return errManagedSessionPending
	}
	result, err := startAndConfirmManagedSnapshot(ctx, w.client, runtimehelper.NewClient(w.cfg.RuntimeSocketPath), managedStartJournal{ledger: w.ledger}, task.TaskID, input.binding, task.LeaseAttempt, peer.session, peer)
	w.managedAdmissions.remember(peer)
	if result.Status != "running" || !peer.admitted {
		peer.rollbackRegistration()
	}
	if err != nil {
		return errManagedSessionPending
	}
	return nil
}

func decodeManagedSnapshotTask(task api.TaskEnvelope, nodeID string) (managedSnapshotTask, error) {
	invalid := errors.New("invalid managed Native startup task")
	if task.TaskType != "create_tool_session" || task.NodeID != nodeID || task.TaskID == "" || len(task.TaskID) > 128 ||
		strings.ContainsAny(task.TaskID, "/\\\x00\r\n") || task.IdempotencyKey != task.TaskID || task.LeaseAttempt <= 0 || task.LeaseAttempt > 2147483647 {
		return managedSnapshotTask{}, invalid
	}
	pointer, ok := task.Payload["skill_manager"].(map[string]any)
	if !ok || len(pointer) != 4 {
		return managedSnapshotTask{}, invalid
	}
	for _, name := range []string{"protocol_version", "manifest_version", "snapshot_id", "task_id"} {
		if pointer[name] == nil {
			return managedSnapshotTask{}, invalid
		}
	}
	data, err := json.Marshal(pointer)
	if err != nil {
		return managedSnapshotTask{}, invalid
	}
	var decoded struct {
		ProtocolVersion int    `json:"protocol_version"`
		ManifestVersion int    `json:"manifest_version"`
		SnapshotID      string `json:"snapshot_id"`
		TaskID          string `json:"task_id"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil || decoded.ProtocolVersion != 1 || decoded.ManifestVersion != 1 || decoded.TaskID != task.TaskRecordID {
		return managedSnapshotTask{}, invalid
	}
	allowed, err := runtimehelper.Map(toolsessions.CreatePayload{})
	if err != nil {
		return managedSnapshotTask{}, err
	}
	payload := make(map[string]any, len(task.Payload)-1)
	for name, value := range task.Payload {
		if name == "skill_manager" {
			continue
		}
		// The Server also carries workspace-sync metadata; it does not configure Helper execution.
		if name == "git_sync_policy" {
			if !validManagedGitSyncPolicy(value) {
				return managedSnapshotTask{}, invalid
			}
			continue
		}
		if _, canonical := allowed[name]; !canonical {
			return managedSnapshotTask{}, invalid
		}
		payload[name] = value
	}
	session, err := toolsessions.DecodeCreatePayload(payload)
	if err != nil || session.ToolType != "claude" || session.RuntimeBackend != "native" {
		return managedSnapshotTask{}, invalid
	}
	binding := skillmanager.SkillSnapshotIdentity{
		SnapshotID: decoded.SnapshotID, TaskID: task.TaskRecordID, NodeID: task.NodeID,
		UserID: session.UserID, AccountID: session.ToolAccountID, SessionID: session.SessionID, RuntimeBackend: session.RuntimeBackend,
	}
	if err := binding.Validate(); err != nil {
		return managedSnapshotTask{}, invalid
	}
	return managedSnapshotTask{binding: binding, session: session}, nil
}

func validManagedGitSyncPolicy(value any) bool {
	policy, ok := value.(map[string]any)
	if !ok {
		return false
	}
	for name, value := range policy {
		switch name {
		case "exclude_hooks", "exclude_locks", "require_clean_git_lock", "warn_concurrent_git":
			if _, ok := value.(bool); !ok {
				return false
			}
		default:
			return false
		}
	}
	return true
}
