//go:build !linux

package runtimehelper

import (
	"context"
	"errors"
)

func (e Engine) executeAccountMigration(ctx context.Context, task, userID, accountID, source, target, accountPath, backupPath string) error {
	return errors.New("backend account migration supervision requires Linux")
}
