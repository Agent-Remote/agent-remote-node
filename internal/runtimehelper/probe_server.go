package runtimehelper

import (
	"context"
	"encoding/json"
	"net"
	"time"
)

func (s *Server) handleProbe(parent context.Context, connection net.Conn, request Request) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	_ = connection.SetDeadline(deadline)
	encoder := json.NewEncoder(connection)
	if err := s.lockProbe(ctx); err != nil {
		return
	}
	defer s.probeMu.Unlock()
	result, err := s.engine.Execute(ctx, request)
	if err != nil {
		_ = encoder.Encode(errorResponse(classifyError(err), publicError(err)))
		return
	}
	_ = encoder.Encode(Response{Version: ProtocolVersion, OK: true, Result: result})
}

func (s *Server) lockProbe(ctx context.Context) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.probeMu.TryLock() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
