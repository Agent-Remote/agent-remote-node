//go:build !linux

package runtimehelper

import (
	"context"
	"errors"

	"github.com/Agent-Remote/agent-remote-node/internal/workspace"
)

func requireSyncPreparationPlatform() error {
	return errors.New("workspace runtime preparation requires Linux")
}

func (e Engine) prepareSyncDirectory(ctx context.Context, directory *workspace.Directory, userID string) error {
	return requireSyncPreparationPlatform()
}
