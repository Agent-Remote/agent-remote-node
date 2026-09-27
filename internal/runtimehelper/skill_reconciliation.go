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

const skillReconciliationOperation = "reconcile_skill_session"
const skillAdmissionDrainOperation = "drain_unadmitted_skill_session"
const skillReconciliationTimeout = 15 * time.Minute

type skillReconciliationRequest struct {
	NodeID    string `json:"node_id"`
	SessionID string `json:"session_id"`
}

func (r *skillReconciliationRequest) UnmarshalJSON(data []byte) error {
	type plain skillReconciliationRequest
	return decodeFinalizationFileFields(data, (*plain)(r), "", "node_id", "session_id")
}

// SkillSessionObservation reports original runtime evidence, never new broker or launch authority.
type SkillSessionObservation struct {
	SessionID string                           `json:"session_id"`
	State     string                           `json:"state"`
	Record    *skillmanager.FinalizationRecord `json:"record"`
	Pending   *skillmanager.CaptureFailure     `json:"capture_pending,omitempty"`
}

// Validate binds the observation and any frozen input to the requested Node and session.
func (r SkillSessionObservation) Validate(nodeID, sessionID string) error {
	if !validSkillUUID(nodeID) || !validSkillUUID(sessionID) || r.SessionID != sessionID {
		return errors.New("skill observation changed runtime identity")
	}
	switch r.State {
	case "not_started", "running":
		if r.Record == nil && r.Pending == nil {
			return nil
		}
	case "capture_pending":
		if r.Record == nil && r.Pending != nil && r.Pending.Validate() == nil && r.Pending.Binding.NodeID == nodeID && r.Pending.Binding.SessionID == sessionID {
			return nil
		}
	case "finalized":
		if r.Pending == nil && r.Record != nil && r.Record.Validate() == nil && r.Record.Binding.NodeID == nodeID && r.Record.Binding.SessionID == sessionID {
			return nil
		}
	}
	return errors.New("invalid skill session observation")
}

// ReconcileSkillSession freezes only a proven original stopped Native runtime on its original boot.
func (c Client) ReconcileSkillSession(ctx context.Context, requestID, nodeID, sessionID string) (SkillSessionObservation, error) {
	return c.observeSkillSession(ctx, requestID, skillReconciliationOperation, nodeID, sessionID)
}

// DrainUnadmittedSkillSession drains an original browser-enabled runtime whose worker grant was lost.
// The trusted worker asserts admission loss; the Helper independently proves the original runtime.
func (c Client) DrainUnadmittedSkillSession(ctx context.Context, requestID, nodeID, sessionID string) (SkillSessionObservation, error) {
	return c.observeSkillSession(ctx, requestID, skillAdmissionDrainOperation, nodeID, sessionID)
}

func (c Client) observeSkillSession(ctx context.Context, requestID, operation, nodeID, sessionID string) (SkillSessionObservation, error) {
	var result SkillSessionObservation
	if validateID(requestID, "request_id") != nil || !validSkillUUID(nodeID) || !validSkillUUID(sessionID) {
		return result, errors.New("invalid skill reconciliation identity")
	}
	// Complete default-limit trees can take longer than the generic short Helper call budget.
	ctx, cancel := context.WithTimeout(ctx, skillReconciliationTimeout)
	defer cancel()
	c.timeout = skillReconciliationTimeout
	reply, err := c.Call(ctx, requestID, operation, map[string]any{"node_id": nodeID, "session_id": sessionID})
	if err != nil {
		return result, err
	}
	data, err := json.Marshal(reply)
	if err != nil {
		return result, err
	}
	names := []string{"session_id", "state", "record"}
	if _, present := reply["capture_pending"]; present {
		names = append(names, "capture_pending")
	}
	if err := decodeFinalizationFileFields(data, &result, "record", names...); err != nil {
		return result, err
	}
	return result, result.Validate(nodeID, sessionID)
}

func (s *Server) handleSkillReconciliation(parent context.Context, connection net.Conn, reader *bufio.Reader, request Request) {
	ctx, cancel := context.WithTimeout(parent, skillReconciliationTimeout)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	done := make(chan struct{})
	go func() { defer close(done); _, _ = reader.ReadByte(); cancel() }()
	defer func() { _ = connection.Close(); <-done }()
	if err := s.lockSkillPreparation(ctx); err != nil {
		return
	}
	observation, err := s.engine.reconcileSkillSession(ctx, request)
	s.mu.Unlock()
	if err != nil {
		_ = json.NewEncoder(connection).Encode(errorResponse("FINALIZATION_PENDING", "Original skill runtime requires recovery."))
		return
	}
	result, err := Map(observation)
	if err != nil || ctx.Err() != nil {
		return
	}
	_ = json.NewEncoder(connection).Encode(Response{Version: ProtocolVersion, OK: true, Result: result})
}

func validateSkillReconciliation(ctx context.Context, request Request, nodeID string) (skillReconciliationRequest, error) {
	var input skillReconciliationRequest
	if err := ctx.Err(); err != nil {
		return input, err
	}
	if request.Version != ProtocolVersion || (request.Operation != skillReconciliationOperation && request.Operation != skillAdmissionDrainOperation) || validateID(request.RequestID, "request_id") != nil {
		return input, errors.New("invalid skill reconciliation operation")
	}
	if err := decodeStrictPayload(request.Payload, &input); err != nil {
		return input, err
	}
	if input.NodeID != nodeID || !validSkillUUID(input.NodeID) || !validSkillUUID(input.SessionID) {
		return input, errors.New("skill reconciliation belongs to another node or session")
	}
	return input, nil
}
