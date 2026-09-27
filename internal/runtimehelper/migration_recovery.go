package runtimehelper

import (
	"context"
	"errors"

	"github.com/Agent-Remote/agent-remote-node/internal/accountmigration"
)

var errMigrationRecoveryAuthorization = errors.New("invalid migration recovery authorization")

func (e Engine) recoverAccountMigration(ctx context.Context, request Request) (map[string]any, error) {
	var authorization accountmigration.Authorization
	if err := decodeStrictPayload(request.Payload, &authorization); err != nil {
		return nil, err
	}
	binding := authorization.Binding
	if binding.Validate() != nil || authorization.LeaseAttempt <= 0 || binding.TaskID != request.RequestID || binding.NodeID != e.config.NodeID {
		return nil, errMigrationRecoveryAuthorization
	}
	if err := e.inspectAccountMigration(ctx, authorization); err != nil {
		return nil, err
	}
	return map[string]any{"recovered": true, "authorization": authorization}, nil
}
