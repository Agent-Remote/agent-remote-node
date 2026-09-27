package runtimehelper

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"
)

// PrepareManagedSessionSpec creates or verifies a spec; it does not authorize runtime launch.
func (c Client) PrepareManagedSessionSpec(ctx context.Context, requestID string, input ManagedSessionSpecRequest) (map[string]any, error) {
	if err := input.validate(input.Snapshot.NodeID); err != nil {
		return nil, err
	}
	payload, err := Map(input)
	if err != nil {
		return nil, err
	}
	result, err := c.Call(ctx, requestID, managedSpecOperation, payload)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	uid, ok := result["runtime_uid"].(float64)
	if len(result) != 6 || result["status"] != "spec_ready" || result["session_id"] != input.Snapshot.SessionID || result["skill_snapshot_id"] != input.Snapshot.SnapshotID || result["task_record_id"] != input.Snapshot.TaskID || result["runtime_backend"] != "native" || !ok || uid <= 0 || uid >= 1<<32 || uid != float64(uint32(uid)) {
		return nil, errors.New("managed session spec response is inconsistent")
	}
	return result, nil
}

func (s *Server) handleManagedSpec(parent context.Context, connection net.Conn, reader *bufio.Reader, request Request) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	// No body follows this request. Either extra input or EOF cancels the operation.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = reader.ReadByte()
		cancel()
	}()
	defer func() {
		_ = connection.Close()
		<-done
	}()
	encoder := json.NewEncoder(connection)
	fail := func(err error) {
		if request.Operation == managedLaunchOperation || request.Operation == managedRecoveryOperation || request.Operation == managedCancelOperation {
			if errors.Is(err, errManagedStartStopped) {
				_ = encoder.Encode(errorResponse("SKILL_START_STOPPED", "Managed session launch ended; retained skill state requires finalization."))
			} else {
				_ = encoder.Encode(errorResponse("SKILL_START_PENDING", "Managed session launch requires recovery."))
			}
			return
		}
		_ = encoder.Encode(errorResponse("SKILL_SPEC_UNAVAILABLE", "Managed session spec could not be prepared."))
	}
	if err := s.lockSkillPreparation(ctx); err != nil {
		fail(err)
		return
	}
	result, err := s.engine.Execute(ctx, request)
	s.mu.Unlock()
	if err != nil || ctx.Err() != nil {
		fail(err)
		return
	}
	_ = encoder.Encode(Response{Version: ProtocolVersion, OK: true, Result: result})
}
