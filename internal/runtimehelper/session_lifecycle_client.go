package runtimehelper

import (
	"context"
	"errors"
)

// SessionCallResult keeps the task receipt distinct from private current admission.
type SessionCallResult struct {
	Result    map[string]any
	Execution SessionExecution
}

// CallSession explicitly negotiates lifecycle v1 and rejects missing or conflicting observations.
func (c Client) CallSession(ctx context.Context, requestID, operation string, payload map[string]any) (SessionCallResult, error) {
	response, err := c.callResponse(ctx, requestID, operation, payload, 1)
	if err != nil {
		return SessionCallResult{}, err
	}
	identity, relevant, err := (Engine{}).lifecycleIdentityForClient(operation, payload)
	if err != nil || !relevant || response.SessionExecution == nil {
		return SessionCallResult{}, errors.New("session lifecycle response unavailable")
	}
	observation := *response.SessionExecution
	if observation.Version != 1 || observation.SessionID != identity.session || observation.Backend != identity.backend || observation.FreshAdmission && (observation.ReceiptReplay || observation.StopIntent || operation != "start_session" && operation != "docker_start_session" && operation != managedLaunchOperation) {
		return SessionCallResult{}, errors.New("inconsistent session lifecycle response")
	}
	return SessionCallResult{Result: response.Result, Execution: observation}, nil
}

func (e Engine) lifecycleIdentityForClient(operation string, payload map[string]any) (sessionLifecycleIdentity, bool, error) {
	if operation == managedSpecOperation || operation == managedLaunchOperation || operation == managedRecoveryOperation || operation == managedCancelOperation {
		var input ManagedSessionSpecRequest
		if err := decodeStrictPayload(payload, &input); err != nil {
			return sessionLifecycleIdentity{}, true, err
		}
		e.config.NodeID = input.Snapshot.NodeID
	}
	return e.lifecycleIdentity(Request{Operation: operation, Payload: payload})
}

// RecordSessionStopIntent persists only a negative launch fence, never process-stop proof.
func (c Client) RecordSessionStopIntent(ctx context.Context, requestID string, payload map[string]any) error {
	response, err := c.CallSession(ctx, requestID, recordSessionStopOperation, payload)
	if err != nil {
		return err
	}
	if len(response.Result) != 1 || response.Result["intent_recorded"] != true || !response.Execution.StopIntent || response.Execution.FreshAdmission {
		return errors.New("session stop intent response is inconsistent")
	}
	return nil
}

// StartManagedSessionExecution preserves historical results without granting fresh admission.
func (c Client) StartManagedSessionExecution(ctx context.Context, requestID string, input ManagedSessionSpecRequest) (SessionCallResult, error) {
	return c.managedSessionExecution(ctx, requestID, managedLaunchOperation, input)
}

// RecoverManagedSessionExecution reports original runtime recovery separately from its receipt.
func (c Client) RecoverManagedSessionExecution(ctx context.Context, requestID string, input ManagedSessionSpecRequest) (SessionCallResult, error) {
	return c.managedSessionExecution(ctx, requestID, managedRecoveryOperation, input)
}

func (c Client) managedSessionExecution(ctx context.Context, requestID, operation string, input ManagedSessionSpecRequest) (SessionCallResult, error) {
	if err := input.validate(input.Snapshot.NodeID); err != nil {
		return SessionCallResult{}, err
	}
	payload, err := Map(input)
	if err != nil {
		return SessionCallResult{}, err
	}
	reply, err := c.CallSession(ctx, requestID, operation, payload)
	if err != nil {
		return SessionCallResult{}, err
	}
	result := reply.Result
	if operation == managedRecoveryOperation && result["status"] == "not_started" {
		if len(result) != 5 || result["session_id"] != input.Snapshot.SessionID || result["skill_snapshot_id"] != input.Snapshot.SnapshotID || result["task_record_id"] != input.Snapshot.TaskID || result["runtime_backend"] != "native" || reply.Execution.StopIntent {
			return SessionCallResult{}, errors.New("managed session recovery response is inconsistent")
		}
	} else if !validManagedLaunchResult(result, input) {
		return SessionCallResult{}, errors.New("managed session launch response is inconsistent")
	}
	return reply, nil
}
