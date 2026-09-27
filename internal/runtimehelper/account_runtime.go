package runtimehelper

import (
	"errors"
	"os"
	"path/filepath"
)

var errAccountMigrationPending = errors.New("MIGRATION_PENDING")

func (e Engine) requireLegacyAccountRuntime(userID, accountID string) error {
	if validateID(userID, "user_id") != nil || validateID(accountID, "tool_account_id") != nil {
		return errors.New("invalid runtime account identity")
	}
	// Legacy deployments must not create or require a private skill store until it is used.
	if _, err := os.Lstat(e.config.SkillStateRoot); errors.Is(err, os.ErrNotExist) {
		return e.checkLegacyAccountPath(userID, accountID)
	} else if err != nil {
		return errors.New("account skill fence cannot be inspected")
	}
	if err := e.checkAccountRuntimeFence(userID, accountID); err != nil {
		return err
	}
	return e.checkLegacyAccountPath(userID, accountID)
}

func (e Engine) requireNativeAccountRuntime(spec SessionSpec) error {
	if spec.SkillSnapshotID != "" {
		if spec.ManagedSkills != (ManagedSessionSpecBinding{}) {
			return e.requireManagedSpecReceipt(spec)
		}
		// mountNativeSkills must still verify the private snapshot and every runtime binding.
		return nil
	}
	accountID := filepath.Base(spec.AccountPath)
	if spec.AccountPath != filepath.Join(e.config.AccountRoot, spec.UserID, "tool-accounts", "claude", accountID) {
		return errors.New("invalid native runtime account path")
	}
	return e.requireLegacyAccountRuntime(spec.UserID, accountID)
}
