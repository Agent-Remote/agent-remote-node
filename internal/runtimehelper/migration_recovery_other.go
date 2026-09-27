//go:build !linux

package runtimehelper

import (
	"context"
	"errors"
	"github.com/Agent-Remote/agent-remote-node/internal/accountmigration"
)

func (e Engine) inspectAccountMigration(ctx context.Context, authorization accountmigration.Authorization) error {
	return errors.New("backend migration recovery requires Linux")
}
