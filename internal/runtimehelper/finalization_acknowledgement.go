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

const finalizationAckOperation = "acknowledge_skill_finalization"

// AcknowledgeSkillFinalization durably transfers exact authenticated Server receipts to the Helper.
// It advances data-retention state only; it cannot delete content or stop a live runtime.
func (c Client) AcknowledgeSkillFinalization(ctx context.Context, requestID string, ack skillmanager.FinalizationAcknowledgement) (skillmanager.FinalizationRecord, error) {
	target, err := ack.State()
	if err != nil {
		return skillmanager.FinalizationRecord{}, err
	}
	if err := validateID(requestID, "request_id"); err != nil {
		return skillmanager.FinalizationRecord{}, err
	}
	payload, err := Map(ack)
	if err != nil {
		return skillmanager.FinalizationRecord{}, err
	}
	result, err := c.Call(ctx, requestID, finalizationAckOperation, payload)
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
	if reply.Record.Validate() != nil || !skillmanager.SameFinalizationInput(reply.Record, ack.Capture) || reply.Record.State != target {
		return skillmanager.FinalizationRecord{}, errors.New("Helper acknowledgement changed finalization identity or phase")
	}
	return reply.Record, nil
}

func (s *Server) handleFinalizationAcknowledgement(parent context.Context, connection net.Conn, reader *bufio.Reader, request Request) {
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
	var record skillmanager.FinalizationRecord
	var err error
	if request.Operation == finalizationCleanupOperation {
		record, err = s.engine.cleanupFinalization(ctx, request)
	} else {
		record, err = s.engine.acknowledgeFinalization(ctx, request)
	}
	s.mu.Unlock()
	if err != nil {
		_ = json.NewEncoder(connection).Encode(errorResponse("FINALIZATION_PENDING", "Retained skill acknowledgement requires recovery."))
		return
	}
	result, err := Map(struct {
		Record skillmanager.FinalizationRecord `json:"record"`
	}{record})
	if err != nil || ctx.Err() != nil {
		return
	}
	_ = json.NewEncoder(connection).Encode(Response{Version: ProtocolVersion, OK: true, Result: result})
}

func validateFinalizationAcknowledgement(ctx context.Context, request Request, nodeID string) (skillmanager.FinalizationAcknowledgement, error) {
	var ack skillmanager.FinalizationAcknowledgement
	if err := ctx.Err(); err != nil {
		return ack, err
	}
	if request.Version != ProtocolVersion || request.Operation != finalizationAckOperation || validateID(request.RequestID, "request_id") != nil {
		return ack, errors.New("invalid finalization acknowledgement operation")
	}
	if err := decodeStrictPayload(request.Payload, &ack); err != nil {
		return ack, err
	}
	if err := ack.Validate(); err != nil {
		return ack, err
	}
	if ack.Capture.Binding.NodeID != nodeID {
		return ack, errors.New("acknowledgement belongs to another node")
	}
	return ack, nil
}
