package runtimehelper

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

const finalizationCleanupOperation = "cleanup_skill_finalization"

type finalizationCleanupRequest struct {
	Capture skillmanager.FinalizationRecord `json:"capture"`
}

func (r *finalizationCleanupRequest) UnmarshalJSON(data []byte) error {
	type plain finalizationCleanupRequest
	return decodeFinalizationFileFields(data, (*plain)(r), "", "capture")
}

// CleanupFinalizedSkillSession removes stopped transient resources after exact Helper acknowledgement.
// All frozen content, work and retained authorization remain outside the transient runtime root.
func (c Client) CleanupFinalizedSkillSession(ctx context.Context, requestID string, capture skillmanager.FinalizationRecord) (skillmanager.FinalizationRecord, error) {
	if capture.Validate() != nil || !capture.CanDeleteSession() || validateID(requestID, "request_id") != nil {
		return skillmanager.FinalizationRecord{}, errors.New("cleanup requires terminal finalization identity")
	}
	payload, err := Map(finalizationCleanupRequest{Capture: capture})
	if err != nil {
		return skillmanager.FinalizationRecord{}, err
	}
	result, err := c.Call(ctx, requestID, finalizationCleanupOperation, payload)
	if err != nil {
		return skillmanager.FinalizationRecord{}, err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return skillmanager.FinalizationRecord{}, err
	}
	var reply struct {
		Record skillmanager.FinalizationRecord `json:"record"`
	}
	if err := decodeFinalizationFileFields(data, &reply, "", "record"); err != nil {
		return reply.Record, err
	}
	if reply.Record != capture {
		return skillmanager.FinalizationRecord{}, errors.New("runtime cleanup changed acknowledged finalization")
	}
	return reply.Record, nil
}

func validateFinalizationCleanup(ctx context.Context, request Request, nodeID string) (skillmanager.FinalizationRecord, error) {
	var input finalizationCleanupRequest
	if err := ctx.Err(); err != nil {
		return input.Capture, err
	}
	if request.Version != ProtocolVersion || request.Operation != finalizationCleanupOperation || validateID(request.RequestID, "request_id") != nil {
		return input.Capture, errors.New("invalid finalization cleanup operation")
	}
	if err := decodeStrictPayload(request.Payload, &input); err != nil {
		return input.Capture, err
	}
	if input.Capture.Validate() != nil || !input.Capture.CanDeleteSession() || input.Capture.Binding.NodeID != nodeID {
		return input.Capture, errors.New("invalid acknowledged cleanup binding")
	}
	return input.Capture, nil
}
