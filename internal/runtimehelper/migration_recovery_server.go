package runtimehelper

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/accountmigration"
)

const migrationRecoveryOperation = "recover_account_migration"
const migrationRecoveryTimeout = 15 * time.Minute

// RecoverAccountMigration executes the selected recovery while the Worker maintains its Server lease.
// The Helper independently bounds the socket and full scan, including lifecycle-lock waiting.
func (c Client) RecoverAccountMigration(ctx context.Context, authorization accountmigration.Authorization) (map[string]any, error) {
	if authorization.Binding.Validate() != nil || authorization.LeaseAttempt <= 0 {
		return nil, errMigrationRecoveryAuthorization
	}
	payload, err := Map(authorization)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, migrationRecoveryTimeout)
	defer cancel()
	c.timeout = migrationRecoveryTimeout
	return c.Call(ctx, authorization.Binding.TaskID, migrationRecoveryOperation, payload)
}

func (s *Server) handleMigrationRecovery(parent context.Context, connection net.Conn, reader *bufio.Reader, request Request) {
	ctx, cancel := context.WithTimeout(parent, migrationRecoveryTimeout)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	done := make(chan struct{})
	go func() { defer close(done); _, _ = reader.ReadByte(); cancel() }()
	defer func() { _ = connection.Close(); <-done }()
	if err := s.lockSkillPreparation(ctx); err != nil {
		return
	}
	result, err := s.engine.Execute(ctx, request)
	s.mu.Unlock()
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		_ = json.NewEncoder(connection).Encode(errorResponse("RUNTIME_MIGRATION_RECOVERY_REQUIRED", "Original backend migration still requires recovery."))
		return
	}
	_ = json.NewEncoder(connection).Encode(Response{Version: ProtocolVersion, OK: true, Result: result})
}
