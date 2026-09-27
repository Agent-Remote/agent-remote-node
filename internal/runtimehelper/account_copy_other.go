//go:build !linux

package runtimehelper

import (
	"context"
	"errors"
)

func (e Engine) backupMigratingAccount(ctx context.Context, taskID, userID, accountID, source, target, accountPath, backupPath string) error {
	return errors.New("backend account copy supervision requires Linux")
}
