package runtimehelper

import (
	"bufio"
	"context"
	"encoding/json"
	"net"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// SkillReclamationAuthorizer obtains a fresh authenticated Server observation for a live challenge.
// It must honor cancellation and cannot reuse a saved response or supply a reconstructed deadline.
type SkillReclamationAuthorizer func(context.Context, string, skillmanager.FinalizationAcknowledgement) (skillmanager.ReclamationAuthorization, error)

// ReclaimSkillFinalization reclaims an exact terminal capture through a live Helper challenge.
// Existing durable intent replays without requesting another Server observation.
func (c Client) ReclaimSkillFinalization(ctx context.Context, requestID string, capture skillmanager.FinalizationRecord, authorize SkillReclamationAuthorizer) (skillmanager.FinalizationRecord, error) {
	if capture.Validate() != nil || !capture.CanDeleteSession() || capture.ObjectsVersion != 1 || authorize == nil {
		return skillmanager.FinalizationRecord{}, errReclamationPending
	}
	payload, err := Map(finalizationCleanupRequest{Capture: capture})
	if err != nil {
		return skillmanager.FinalizationRecord{}, errReclamationPending
	}
	record, err := c.exchangeSkillReclamation(ctx, Request{Version: ProtocolVersion, RequestID: requestID, Operation: finalizationReclamationOperation, Payload: payload}, &capture, authorize)
	if err != nil || record != capture {
		return skillmanager.FinalizationRecord{}, errReclamationPending
	}
	return record, nil
}

// ResumeSkillReclamation resumes only a previously marked original session, including lost replies.
// It cannot create intent or request remote authorization, and works without a Worker transfer ledger.
func (c Client) ResumeSkillReclamation(ctx context.Context, requestID, nodeID, sessionID string) (skillmanager.FinalizationRecord, error) {
	if !validSkillUUID(nodeID) || !validSkillUUID(sessionID) {
		return skillmanager.FinalizationRecord{}, errReclamationPending
	}
	payload, err := Map(skillReconciliationRequest{NodeID: nodeID, SessionID: sessionID})
	if err != nil {
		return skillmanager.FinalizationRecord{}, errReclamationPending
	}
	record, err := c.exchangeSkillReclamation(ctx, Request{Version: ProtocolVersion, RequestID: requestID, Operation: finalizationReclamationResumeOperation, Payload: payload}, nil, nil)
	if err != nil || record.Binding.NodeID != nodeID || record.Binding.SessionID != sessionID {
		return skillmanager.FinalizationRecord{}, errReclamationPending
	}
	return record, nil
}

func (c Client) exchangeSkillReclamation(parent context.Context, request Request, expected *skillmanager.FinalizationRecord, authorize SkillReclamationAuthorizer) (skillmanager.FinalizationRecord, error) {
	if validateID(request.RequestID, "request_id") != nil {
		return skillmanager.FinalizationRecord{}, errReclamationPending
	}
	ctx, cancel := context.WithTimeout(parent, finalizationReclamationTimeout)
	defer cancel()
	dialer := net.Dialer{Timeout: c.timeout}
	connection, err := dialer.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return skillmanager.FinalizationRecord{}, errReclamationPending
	}
	defer connection.Close()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	encoder := json.NewEncoder(connection)
	if err := encoder.Encode(request); err != nil {
		return skillmanager.FinalizationRecord{}, errReclamationPending
	}
	reader := bufio.NewReader(connection)
	frame, err := readReclamationFrame(reader)
	if err != nil {
		return skillmanager.FinalizationRecord{}, errReclamationPending
	}
	if frame.Kind == "authorize" {
		ack := frame.Acknowledgement
		if expected == nil || authorize == nil || !validSkillUUID(frame.Challenge) || ack == nil || ack.Validate() != nil ||
			!skillmanager.SameFinalizationInput(ack.Capture, *expected) {
			return skillmanager.FinalizationRecord{}, errReclamationPending
		}
		if state, err := ack.State(); err != nil || state != expected.State {
			return skillmanager.FinalizationRecord{}, errReclamationPending
		}
		// Keep reading while HTTP is in flight so Helper timeout/shutdown cancels that request.
		// Stop after the terminal frame: its normal following EOF must not undo a valid receipt.
		type reply struct {
			frame finalizationReclamationFrame
			err   error
		}
		finished := make(chan reply, 1)
		done := make(chan struct{})
		go func() {
			defer close(done)
			frame, err := readReclamationFrame(reader)
			if err != nil || frame.Kind != "reclaimed" {
				cancel()
			}
			finished <- reply{frame, err}
		}()
		defer func() { _ = connection.Close(); <-done }()
		authority, err := authorize(ctx, frame.Challenge, *ack)
		if err != nil || ctx.Err() != nil || authority.RequestID != frame.Challenge || authority.Match(*ack) != nil {
			return skillmanager.FinalizationRecord{}, errReclamationPending
		}
		if err := encoder.Encode(authority); err != nil {
			return skillmanager.FinalizationRecord{}, errReclamationPending
		}
		result := <-finished
		frame = result.frame
		if result.err != nil {
			return skillmanager.FinalizationRecord{}, errReclamationPending
		}
	}
	if frame.Kind != "reclaimed" || frame.Record == nil || frame.Record.Validate() != nil || !frame.Record.CanDeleteSession() || frame.Record.ObjectsVersion != 1 || ctx.Err() != nil {
		return skillmanager.FinalizationRecord{}, errReclamationPending
	}
	return *frame.Record, nil
}
