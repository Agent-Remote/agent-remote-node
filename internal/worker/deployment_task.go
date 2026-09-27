package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

var errDeploymentPending = errors.New("DEPLOYMENT_PENDING: original skill deployment requires confirmation")
var errDeploymentLeaseLost = errors.New("DEPLOYMENT_LEASE_LOST: original deployment lease is unavailable")

func deploymentTask(task api.TaskEnvelope) bool {
	return task.TaskType == "prepare_account_skills" || strings.HasPrefix(task.TaskID, "prepare_account_skills:")
}

func decodeDeploymentTask(task api.TaskEnvelope, nodeID string) (skillmanager.SkillDeploymentIdentity, error) {
	var payload struct {
		ProtocolVersion int    `json:"protocol_version"`
		UserID          string `json:"user_id"`
		OperationID     string `json:"operation_id"`
		AccountID       string `json:"tool_account_id"`
		AttemptID       string `json:"attempt_id"`
		TaskID          string `json:"task_record_id"`
		CheckpointID    string `json:"checkpoint_id"`
		TreeDigest      string `json:"tree_digest"`
		PlanDigest      string `json:"plan_digest"`
		RuntimeBackend  string `json:"runtime_backend"`
	}
	if len(task.Payload) != 10 {
		return skillmanager.SkillDeploymentIdentity{}, errDeploymentPending
	}
	for _, key := range []string{"protocol_version", "user_id", "operation_id", "tool_account_id", "attempt_id", "task_record_id", "checkpoint_id", "tree_digest", "plan_digest", "runtime_backend"} {
		if task.Payload[key] == nil {
			return skillmanager.SkillDeploymentIdentity{}, errDeploymentPending
		}
	}
	data, err := json.Marshal(task.Payload)
	if err != nil {
		return skillmanager.SkillDeploymentIdentity{}, errDeploymentPending
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return skillmanager.SkillDeploymentIdentity{}, errDeploymentPending
	}
	binding := skillmanager.SkillDeploymentIdentity{OperationID: payload.OperationID, AttemptID: payload.AttemptID, TaskID: payload.TaskID,
		UserID: payload.UserID, AccountID: payload.AccountID, NodeID: nodeID, CheckpointID: payload.CheckpointID,
		PlanDigest: payload.PlanDigest, TreeDigest: payload.TreeDigest, RuntimeBackend: payload.RuntimeBackend}
	if binding.Validate() != nil || payload.ProtocolVersion != 1 || task.NodeID != nodeID || task.TaskRecordID != binding.TaskID ||
		task.TaskType != "prepare_account_skills" || task.TaskID != "prepare_account_skills:"+binding.AttemptID || task.IdempotencyKey != task.TaskID || task.LeaseAttempt < 1 || task.LeaseAttempt > 2147483647 {
		return binding, errDeploymentPending
	}
	return binding, nil
}

func (w Worker) executeDeployment(ctx context.Context, task api.TaskEnvelope) error {
	binding, err := decodeDeploymentTask(task, w.cfg.NodeID)
	if err != nil || w.requireBackend(binding.RuntimeBackend) != nil {
		return errDeploymentPending
	}
	if err := runDeployment(ctx, w.client, runtimehelper.NewClient(w.cfg.RuntimeSocketPath), deploymentJournal{w.ledger}, task.TaskID, binding, task.LeaseAttempt); err != nil {
		return errDeploymentPending
	}
	return nil
}
