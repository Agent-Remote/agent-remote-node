package runtimehelper

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

const finalizationListOperation = "list_skill_finalizations"
const finalizationInspectOperation = "inspect_skill_finalization"

type finalizationListRequest struct {
	NodeID string `json:"node_id"`
	Cursor string `json:"cursor"`
}

func (r *finalizationListRequest) UnmarshalJSON(data []byte) error {
	type plain finalizationListRequest
	return decodeFinalizationFileFields(data, (*plain)(r), "", "node_id", "cursor")
}

// ListSkillFinalizations discovers retained captures independently of task polling or runtime specs.
func (c Client) ListSkillFinalizations(ctx context.Context, requestID, nodeID, cursor string) (skillmanager.FinalizationPage, error) {
	if err := skillmanager.ValidateFinalizationCursor(cursor); err != nil {
		return skillmanager.FinalizationPage{}, err
	}
	if validateID(requestID, "request_id") != nil || skillmanager.ValidateFinalizationCursor(nodeID) != nil || nodeID == "" {
		return skillmanager.FinalizationPage{}, errors.New("invalid finalization inventory identity")
	}
	result, err := c.Call(ctx, requestID, finalizationListOperation, map[string]any{"node_id": nodeID, "cursor": cursor})
	if err != nil {
		return skillmanager.FinalizationPage{}, err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return skillmanager.FinalizationPage{}, err
	}
	return decodeFinalizationPage(data, cursor, nodeID)
}

func decodeFinalizationPage(data []byte, cursor, nodeID string) (skillmanager.FinalizationPage, error) {
	var page skillmanager.FinalizationPage
	if err := decodeFinalizationFileFields(data, &page, "", "items", "next_cursor", "invalid_names"); err != nil {
		return page, err
	}
	// Per-item canonical fields cannot silently decode an aliased or omitted success diagnostic.
	var raw struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return page, err
	}
	for _, item := range raw.Items {
		var decoded skillmanager.FinalizationInventoryItem
		if err := decodeFinalizationFileFields(item, &decoded, "record", "session_id", "record", "code"); err != nil {
			return page, err
		}
	}
	return page, page.Validate(cursor, nodeID)
}

func (s *Server) handleFinalizationList(parent context.Context, connection net.Conn, reader *bufio.Reader, request Request) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	done := make(chan struct{})
	go func() { defer close(done); _, _ = reader.ReadByte(); cancel() }()
	defer func() { _ = connection.Close(); <-done }()
	if err := s.lockSkillPreparation(ctx); err != nil {
		return
	}
	var page skillmanager.FinalizationPage
	var err error
	if request.Operation == finalizationInspectOperation {
		page, err = s.engine.inspectFinalization(ctx, request)
	} else {
		page, err = s.engine.listFinalizations(ctx, request)
	}
	s.mu.Unlock()
	if err != nil {
		_ = json.NewEncoder(connection).Encode(errorResponse("FINALIZATION_UNAVAILABLE", "Retained skill inventory is unavailable."))
		return
	}
	result, err := Map(page)
	if err != nil || ctx.Err() != nil {
		return
	}
	_ = json.NewEncoder(connection).Encode(Response{Version: ProtocolVersion, OK: true, Result: result})
}

func validateFinalizationListRequest(ctx context.Context, request Request, nodeID string) (finalizationListRequest, error) {
	var input finalizationListRequest
	if err := ctx.Err(); err != nil {
		return input, err
	}
	if request.Version != ProtocolVersion || request.Operation != finalizationListOperation || validateID(request.RequestID, "request_id") != nil {
		return input, errors.New("invalid finalization inventory operation")
	}
	if err := decodeStrictPayload(request.Payload, &input); err != nil {
		return input, err
	}
	if input.NodeID == "" || input.NodeID != nodeID || skillmanager.ValidateFinalizationCursor(input.NodeID) != nil {
		return input, errors.New("finalization inventory belongs to another node")
	}
	return input, skillmanager.ValidateFinalizationCursor(input.Cursor)
}
