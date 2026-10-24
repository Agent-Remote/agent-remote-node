package worker

import (
	"context"
	"errors"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/toolsessions"
)

var errSessionStopPending = errors.New("SESSION_STOP_PENDING: original session termination requires observation")

func sessionStopPending(err error) bool {
	var helper *runtimehelper.Error
	return errors.Is(err, errSessionStopPending) || errors.As(err, &helper) && helper.Code == "SESSION_STOP_PENDING"
}

func (w Worker) recordSessionIntent(ctx context.Context, task api.TaskEnvelope) error {
	if task.TaskType != "stop_tool_session" || task.NodeID != w.cfg.NodeID {
		return errSessionStopPending
	}
	payload, err := toolsessions.DecodeStopPayload(task.Payload)
	if err != nil || payload.RuntimeBackend != "native" && payload.RuntimeBackend != "docker_sandbox" {
		return errSessionStopPending
	}
	if !validStopTaskIdentity(task, payload.SessionID) {
		return errSessionStopPending
	}
	mapped, err := runtimehelper.Map(payload)
	if err != nil {
		return errSessionStopPending
	}
	if managedStopTask(task) {
		if _, _, err := decodeManagedStop(task, w.cfg.NodeID); err != nil {
			return errSessionStopPending
		}
		mapped["skill_finalization"] = task.Payload["skill_finalization"]
	}
	if err := runtimehelper.NewClient(w.cfg.RuntimeSocketPath).RecordSessionStopIntent(ctx, task.TaskID, mapped); err != nil {
		return errSessionStopPending
	}
	return nil
}

func lifecycleSessionOperation(operation string) bool {
	return operation == "start_session" || operation == "docker_start_session" || operation == "stop_session" || operation == "docker_stop_session"
}

func (w Worker) observeOldStopTask(ctx context.Context, task api.TaskEnvelope) error {
	if task.TaskType != "stop_tool_session" && task.TaskType != "cleanup_resources" {
		return nil
	}
	if _, err := w.executeKnownTask(ctx, task); err != nil {
		return errSessionStopPending
	}
	return nil
}

func (w Worker) validateCleanupTask(task api.TaskEnvelope) error {
	if task.NodeID != w.cfg.NodeID || task.TaskID == "" || task.IdempotencyKey != task.TaskID {
		return errSessionStopPending
	}
	backend, ok := task.Payload["runtime_backend"].(string)
	if !ok || backend != "native" && backend != "docker_sandbox" {
		return errSessionStopPending
	}
	ids, ok := task.Payload["session_ids"].([]any)
	if !ok || len(ids) == 0 || len(ids) > 100 {
		return errSessionStopPending
	}
	seen := make(map[string]bool, len(ids))
	for _, value := range ids {
		id, ok := value.(string)
		if !ok || len(id) == 0 || len(id) > 128 || seen[id] {
			return errSessionStopPending
		}
		for _, char := range id {
			if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == ':') {
				return errSessionStopPending
			}
		}
		seen[id] = true
	}
	return nil
}

func validStopTaskIdentity(task api.TaskEnvelope, session string) bool {
	if task.TaskID == "" || task.IdempotencyKey != task.TaskID {
		return false
	}
	raw, present := task.Payload["preserve_interrupted_status"]
	preserve := false
	if present {
		value, ok := raw.(bool)
		if !ok {
			return false
		}
		preserve = value
	}
	if task.TaskID == "cleanup_tool_session:"+session {
		return preserve
	}
	return task.TaskID == "stop_tool_session:"+session && !preserve
}
