package runtimehelper

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

type finalizationInspectRequest struct {
	NodeID    string `json:"node_id"`
	SessionID string `json:"session_id"`
}

func (r *finalizationInspectRequest) UnmarshalJSON(data []byte) error {
	type plain finalizationInspectRequest
	return decodeFinalizationFileFields(data, (*plain)(r), "", "node_id", "session_id")
}

// InspectSkillFinalization reads one original frozen identity without capture or runtime mutation.
// Missing, unfinished and corrupt state remain errors; none can become an empty export.
func (c Client) InspectSkillFinalization(ctx context.Context, requestID, nodeID, sessionID string) (skillmanager.FinalizationRecord, error) {
	if nodeID == "" || sessionID == "" || skillmanager.ValidateFinalizationCursor(nodeID) != nil || skillmanager.ValidateFinalizationCursor(sessionID) != nil || validateID(requestID, "request_id") != nil {
		return skillmanager.FinalizationRecord{}, errors.New("invalid frozen inspection identity")
	}
	result, err := c.Call(ctx, requestID, finalizationInspectOperation, map[string]any{"node_id": nodeID, "session_id": sessionID})
	if err != nil {
		return skillmanager.FinalizationRecord{}, err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return skillmanager.FinalizationRecord{}, err
	}
	page, err := decodeFinalizationPage(data, "", nodeID)
	if err != nil {
		return skillmanager.FinalizationRecord{}, err
	}
	if len(page.Items) != 1 || page.NextCursor != "" || page.InvalidNames || page.Items[0].SessionID != sessionID || page.Items[0].Record == nil {
		return skillmanager.FinalizationRecord{}, errors.New("frozen finalization is unavailable")
	}
	return *page.Items[0].Record, nil
}

func validateFinalizationInspect(ctx context.Context, request Request, nodeID string) (finalizationInspectRequest, error) {
	var input finalizationInspectRequest
	if err := ctx.Err(); err != nil {
		return input, err
	}
	if request.Version != ProtocolVersion || request.Operation != finalizationInspectOperation || validateID(request.RequestID, "request_id") != nil {
		return input, errors.New("invalid frozen inspection request")
	}
	if err := decodeStrictPayload(request.Payload, &input); err != nil {
		return input, err
	}
	if input.NodeID == "" || input.NodeID != nodeID || input.SessionID == "" || skillmanager.ValidateFinalizationCursor(input.SessionID) != nil || skillmanager.ValidateFinalizationCursor(nodeID) != nil {
		return input, errors.New("invalid frozen inspection identity")
	}
	return input, nil
}
