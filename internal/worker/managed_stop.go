package worker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"github.com/Agent-Remote/agent-remote-node/internal/toolsessions"
)

const managedStopSaveTimeout = 10 * time.Second

func managedStopTask(task api.TaskEnvelope) bool {
	if task.TaskType != "stop_tool_session" {
		return false
	}
	for key := range task.Payload {
		if strings.EqualFold(key, "skill_finalization") {
			return true
		}
	}
	return false
}

func decodeManagedStop(task api.TaskEnvelope, nodeID string) (skillmanager.SkillSnapshotIdentity, toolsessions.StopPayload, error) {
	invalid := errors.New("invalid managed stop identity")
	payload, err := toolsessions.DecodeStopPayload(task.Payload)
	if err != nil || task.TaskType != "stop_tool_session" || task.NodeID != nodeID || task.TaskID != "stop_tool_session:"+payload.SessionID || task.IdempotencyKey != task.TaskID || payload.RuntimeBackend != "native" {
		return skillmanager.SkillSnapshotIdentity{}, payload, invalid
	}
	for key := range task.Payload {
		switch key {
		case "session_id", "tmux_session_name", "sandbox_name", "runtime_backend", "runtime_resource_id", "skill_finalization", "preserve_interrupted_status":
		default:
			return skillmanager.SkillSnapshotIdentity{}, payload, invalid
		}
	}
	pointer, ok := task.Payload["skill_finalization"].(map[string]any)
	if !ok || len(pointer) != 4 {
		return skillmanager.SkillSnapshotIdentity{}, payload, invalid
	}
	var p struct {
		SnapshotID string `json:"snapshot_id"`
		TaskID     string `json:"task_id"`
		UserID     string `json:"user_id"`
		AccountID  string `json:"account_id"`
	}
	for _, key := range []string{"snapshot_id", "task_id", "user_id", "account_id"} {
		if _, ok := pointer[key].(string); !ok {
			return skillmanager.SkillSnapshotIdentity{}, payload, invalid
		}
	}
	data, err := json.Marshal(pointer)
	if err != nil {
		return skillmanager.SkillSnapshotIdentity{}, payload, invalid
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return skillmanager.SkillSnapshotIdentity{}, payload, invalid
	}
	binding := skillmanager.SkillSnapshotIdentity{SnapshotID: p.SnapshotID, TaskID: p.TaskID, NodeID: nodeID, UserID: p.UserID, AccountID: p.AccountID, SessionID: payload.SessionID, RuntimeBackend: payload.RuntimeBackend}
	return binding, payload, binding.Validate()
}

func (w Worker) executeManagedStop(ctx context.Context, task api.TaskEnvelope) error {
	binding, payload, err := decodeManagedStop(task, w.cfg.NodeID)
	if err != nil {
		return errFinalizationPending
	}
	previous, exists, err := w.ledger.Get(task.TaskID)
	if err != nil {
		return errFinalizationPending
	}
	if exists {
		if previous.Status != "succeeded" || previous.Result["skill_finalization_operation_id"] != binding.SnapshotID {
			return errFinalizationPending
		}
		return w.client.CompleteTask(ctx, task.TaskID, previous.Result)
	}
	if err := w.requireBackend(payload.RuntimeBackend); err != nil {
		return errFinalizationPending
	}
	if err := w.client.StartTask(ctx, task.TaskID); err != nil {
		return err
	}
	// Capture can fail after all writers stop. Only a fresh validated Helper observation
	// may establish that fact; the stop call error itself grants no success authority.
	_ = w.stopManagedWriters(ctx, task, payload)
	helper := managedFinalizationReader{
		managedAdmissionHelper: runtimehelper.NewClient(w.cfg.RuntimeSocketPath),
		admissions:             w.managedAdmissions, broker: w.browserBroker,
	}
	observation, err := helper.ReconcileSkillSession(ctx, "stop-finalization-observe", w.cfg.NodeID, payload.SessionID)
	if err != nil {
		return errFinalizationPending
	}
	var result map[string]any
	if observation.Pending != nil {
		failure, err := stoppedCapturePending(binding, observation)
		if err != nil {
			return errFinalizationPending
		}
		if err := w.client.ObserveSkillCapturePending(ctx, failure); err != nil {
			return errFinalizationPending
		}
		result = managedPendingStopResult(failure)
	} else {
		capture, err := stoppedCapture(binding, observation)
		if err != nil {
			return errFinalizationPending
		}
		// Process stop and privileged freezing precede all network saving and transfer-gate waits.
		// Failures remain independently retryable from the Helper inventory after task completion.
		_ = w.attemptStoppedFinalization(ctx, w.client, helper, capture, managedStopSaveTimeout)
		result = managedStopResult(capture)
	}
	if err := w.ledger.Save(ledger.Entry{TaskID: task.TaskID, Status: "succeeded", Result: result}); err != nil {
		return errFinalizationPending
	}
	return w.client.CompleteTask(ctx, task.TaskID, result)
}

func (w Worker) stopManagedWriters(ctx context.Context, task api.TaskEnvelope, payload toolsessions.StopPayload) error {
	if err := w.managedAdmissions.lock(ctx); err != nil {
		return err
	}
	defer w.managedAdmissions.unlock()
	if w.browserBroker != nil {
		w.browserBroker.UnregisterToolSession(payload.SessionID, "")
	}
	if w.bridges != nil {
		w.bridges.StopToolSession(payload.SessionID)
	}
	_, err := w.callRuntimeHelper(ctx, task, "stop_session", payload)
	if err == nil {
		delete(w.managedAdmissions.granted, payload.SessionID)
	}
	return err
}

func stoppedCapture(binding skillmanager.SkillSnapshotIdentity, observation runtimehelper.SkillSessionObservation) (skillmanager.FinalizationRecord, error) {
	if observation.Validate(binding.NodeID, binding.SessionID) != nil || observation.Record == nil {
		return skillmanager.FinalizationRecord{}, errFinalizationPending
	}
	capture := *observation.Record
	actual := capture.Binding
	if capture.ObjectsVersion != 1 || actual.SnapshotID != binding.SnapshotID || actual.TaskID != binding.TaskID || actual.UserID != binding.UserID || actual.AccountID != binding.AccountID {
		return skillmanager.FinalizationRecord{}, errFinalizationPending
	}
	return capture, nil
}

func managedStopResult(capture skillmanager.FinalizationRecord) map[string]any {
	// This immutable process result intentionally contains no cached saving phase. The operation
	// endpoint derives live progress from the independent authoritative finalization records.
	return map[string]any{"status": "stopped", "session_id": capture.Binding.SessionID,
		"runtime_backend": "native", "skill_finalization_operation_id": capture.Binding.SnapshotID,
		"incoming_digest": capture.TreeDigest, "unclean": capture.Unclean}
}

func (w Worker) attemptStoppedFinalization(ctx context.Context, client finalizationTransferClient, helper finalizationReader, capture skillmanager.FinalizationRecord, bound time.Duration) error {
	attempt, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	journal, err := w.finalizations.acquire(attempt, w.cfg.LedgerPath)
	if err != nil {
		return err
	}
	defer w.finalizations.release()
	return transferFinalization(attempt, client, helper, journal, capture)
}

func stoppedCapturePending(binding skillmanager.SkillSnapshotIdentity, observation runtimehelper.SkillSessionObservation) (skillmanager.CaptureFailure, error) {
	if observation.Validate(binding.NodeID, binding.SessionID) != nil || observation.Pending == nil {
		return skillmanager.CaptureFailure{}, errFinalizationPending
	}
	failure := *observation.Pending
	actual := failure.Binding
	if actual.SnapshotID != binding.SnapshotID || actual.TaskID != binding.TaskID || actual.UserID != binding.UserID || actual.AccountID != binding.AccountID {
		return skillmanager.CaptureFailure{}, errFinalizationPending
	}
	return failure, nil
}

func managedPendingStopResult(failure skillmanager.CaptureFailure) map[string]any {
	return map[string]any{"status": "stopped", "session_id": failure.Binding.SessionID,
		"runtime_backend": "native", "skill_finalization_operation_id": failure.Binding.SnapshotID,
		"incoming_digest": nil, "unclean": failure.Unclean}
}
