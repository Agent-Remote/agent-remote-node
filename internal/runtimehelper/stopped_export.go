package runtimehelper

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillexport"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

const stoppedExportOperation = "stream_stopped_skill_export"
const stoppedRecoveryOperation = "stream_stopped_skill_recovery"

// StreamStoppedSkillExport relays complete original work through the private Helper socket.
// No grant or caller-selected filesystem path crosses this boundary. check owns live authority.
func (c Client) StreamStoppedSkillExport(parent context.Context, requestID string, binding skillmanager.NodeExportBinding, output io.Writer, check func(skillexport.Header, bool) error) error {
	return c.streamStoppedSource(parent, requestID, binding, output, check, stoppedExportOperation)
}

// StreamStoppedSkillRecovery relays the negotiated bounded-metadata format for original stopped work.
func (c Client) StreamStoppedSkillRecovery(parent context.Context, requestID string, binding skillmanager.NodeExportBinding, output io.Writer, check func(skillexport.Header, bool) error) error {
	return c.streamStoppedSource(parent, requestID, binding, output, check, stoppedRecoveryOperation)
}

func (c Client) streamStoppedSource(parent context.Context, requestID string, binding skillmanager.NodeExportBinding, output io.Writer, check func(skillexport.Header, bool) error, operation string) error {
	if binding.Validate() != nil || validateID(requestID, "request_id") != nil || check == nil || output == nil {
		return skillexport.ErrUnavailable
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	dialer := net.Dialer{Timeout: c.timeout}
	connection, err := dialer.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return skillexport.ErrUnavailable
	}
	defer connection.Close()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	// Removing the whole-transfer ceiling must not leave request submission unbounded.
	if err := connection.SetWriteDeadline(time.Now().Add(skillexport.WriteTimeout)); err != nil {
		return skillexport.ErrUnavailable
	}
	payload, err := Map(binding)
	if err != nil {
		return skillexport.ErrUnavailable
	}
	if err := json.NewEncoder(connection).Encode(Request{Version: ProtocolVersion, RequestID: requestID, Operation: operation, Payload: payload}); err != nil {
		return skillexport.ErrUnavailable
	}
	verify := func(header skillexport.Header, force bool) error {
		if header.Binding != binding {
			return skillexport.ErrUnavailable
		}
		return check(header, force)
	}
	relay := skillexport.RelaySnapshot
	if operation == stoppedRecoveryOperation {
		relay = skillexport.RelayRecovery
	}
	if err := relay(ctx, connection, output, verify); err != nil {
		return skillexport.ErrUnavailable
	}
	return nil
}

func (s *Server) handleStoppedExport(parent context.Context, connection net.Conn, reader *bufio.Reader, request Request) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	// Any trailing input or disconnect cancels scanning and a blocked output write.
	done := make(chan struct{})
	go func() { defer close(done); _, _ = reader.ReadByte(); cancel() }()
	defer func() { _ = connection.Close(); <-done }()
	if request.Version != ProtocolVersion || request.Operation != stoppedExportOperation && request.Operation != stoppedRecoveryOperation || validateID(request.RequestID, "request_id") != nil {
		return
	}
	var binding skillmanager.NodeExportBinding
	if decodeStrictPayload(request.Payload, &binding) != nil || binding.NodeID != s.engine.config.NodeID {
		return
	}
	lockCtx, lockCancel := context.WithTimeout(ctx, skillexport.ScanTimeout)
	err := s.lockSkillPreparation(lockCtx)
	lockCancel()
	if err != nil {
		return
	}
	defer s.mu.Unlock()
	// Errors are signaled by missing completion and EOF, never private filesystem details.
	_ = s.engine.streamStoppedSource(ctx, skillexport.NewIdleWriter(ctx, connection, cancel), binding, request.Operation == stoppedRecoveryOperation)
}
