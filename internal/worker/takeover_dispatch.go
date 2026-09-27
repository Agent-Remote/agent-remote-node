package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

var errTakeoverPending = errors.New("MIGRATION_PENDING: account takeover remains pending")

type takeoverCaptureHelper interface {
	takeoverCaptureReader
	CaptureAccountTakeover(context.Context, string, skillmanager.AccountTakeoverBinding, []skillmanager.AccountWriter) (skillmanager.AccountCapture, error)
}

func decodeTakeoverTask(task api.TaskEnvelope, nodeID string) (skillmanager.AccountTakeoverBinding, error) {
	var payload struct {
		TakeoverID      string `json:"takeover_id"`
		UserID          string `json:"user_id"`
		AccountID       string `json:"tool_account_id"`
		RuntimeBackend  string `json:"runtime_backend"`
		DirectoryEpoch  int64  `json:"directory_epoch"`
		InventoryDigest string `json:"inventory_digest"`
		ProtocolVersion int    `json:"protocol_version"`
		ManifestVersion int    `json:"manifest_version"`
	}
	if len(task.Payload) != 8 {
		return skillmanager.AccountTakeoverBinding{}, errTakeoverPending
	}
	for _, key := range []string{"takeover_id", "user_id", "tool_account_id", "runtime_backend", "directory_epoch", "inventory_digest", "protocol_version", "manifest_version"} {
		if task.Payload[key] == nil {
			return skillmanager.AccountTakeoverBinding{}, errTakeoverPending
		}
	}
	data, err := json.Marshal(task.Payload)
	if err != nil {
		return skillmanager.AccountTakeoverBinding{}, errTakeoverPending
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return skillmanager.AccountTakeoverBinding{}, errTakeoverPending
	}
	binding := skillmanager.AccountTakeoverBinding{Version: 1, NodeID: nodeID, UserID: payload.UserID, AccountID: payload.AccountID, TakeoverID: payload.TakeoverID, TaskID: task.TaskRecordID, RuntimeBackend: payload.RuntimeBackend, DirectoryEpoch: payload.DirectoryEpoch, InventoryDigest: payload.InventoryDigest}
	if binding.Validate() != nil || task.NodeID != nodeID || task.TaskType != "takeover_tool_account_skills" || task.TaskID != "takeover_tool_account_skills:"+payload.TakeoverID || task.IdempotencyKey != task.TaskID || task.LeaseAttempt < 1 || task.LeaseAttempt > 2147483647 || payload.RuntimeBackend != "native" || payload.ProtocolVersion != 1 || payload.ManifestVersion != 1 {
		return binding, errTakeoverPending
	}
	return binding, nil
}

func (w Worker) executeTakeover(ctx context.Context, task api.TaskEnvelope) error {
	binding, err := decodeTakeoverTask(task, w.cfg.NodeID)
	if err != nil {
		return errTakeoverPending
	}
	if err := w.requireBackend(binding.RuntimeBackend); err != nil {
		return errTakeoverPending
	}
	// The Server reservation and Helper's immutable capture own recovery. Generic worker result
	// caches cannot consume failures or replace fresh exact authorization on redelivery.
	receipt, err := initiateTakeover(ctx, w.client, runtimehelper.NewClient(w.cfg.RuntimeSocketPath), binding, task.LeaseAttempt)
	if err != nil {
		return errTakeoverPending
	}
	return w.client.CompleteTask(ctx, task.TaskID, takeoverTaskResult(receipt))
}

func initiateTakeover(ctx context.Context, client takeoverTransferClient, helper takeoverCaptureHelper, binding skillmanager.AccountTakeoverBinding, attempt int64) (api.SkillTakeover, error) {
	if binding.Validate() != nil || binding.RuntimeBackend != "native" {
		return api.SkillTakeover{}, errTakeoverPending
	}
	grant, err := client.GetSkillTakeover(ctx, binding)
	if err != nil {
		return api.SkillTakeover{}, err
	}
	if grant.Status == "committed" {
		return grant, nil
	}
	var committed api.SkillTakeover
	err = withTakeoverLease(ctx, client, binding, attempt, func(workCtx context.Context) error {
		capture, err := helper.CaptureAccountTakeover(workCtx, "takeover-capture:"+binding.TaskID, binding, grant.Inventory)
		if err != nil {
			return err
		}
		if capture.Validate() != nil || capture.Binding != binding {
			return errTakeoverPending
		}
		committed, err = uploadTakeoverCapture(workCtx, client, helper, binding)
		return err
	})
	if committed.Status == "committed" {
		return committed, nil
	}
	if err != nil {
		return api.SkillTakeover{}, err
	}
	return api.SkillTakeover{}, errTakeoverPending
}

func takeoverTaskResult(receipt api.SkillTakeover) map[string]any {
	return map[string]any{"status": "committed", "takeover_id": receipt.TakeoverID, "task_record_id": receipt.TaskID, "tool_account_id": receipt.AccountID, "checkpoint_id": receipt.CheckpointID, "capture_digest": receipt.CaptureDigest}
}
