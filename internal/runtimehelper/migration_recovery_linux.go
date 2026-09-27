package runtimehelper

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/Agent-Remote/agent-remote-node/internal/accountmigration"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (e Engine) inspectAccountMigration(ctx context.Context, authorization accountmigration.Authorization) error {
	binding := authorization.Binding
	store, err := e.openExistingSkillStateRoot()
	if err != nil {
		return errMigrationWritersUnknown
	}
	defer store.Close()
	if _, err := skillmanager.ReadAccountFence(store, binding.NodeID, binding.UserID, binding.AccountID); !errors.Is(err, os.ErrNotExist) {
		return errAccountMigrationPending
	}
	if err := e.checkLegacyAccountPath(binding.UserID, binding.AccountID); err != nil {
		return err
	}
	accountPath := filepath.Join(e.config.AccountRoot, binding.UserID, "tool-accounts", "claude", binding.AccountID)
	backupPath := filepath.Join(e.config.StateRoot, "migrations", shortDigest(binding.OriginalTaskID, 32))
	expected := e.accountMigrationReceipt(binding.OriginalTaskID, binding.UserID, binding.AccountID, binding.Source, binding.Target, accountPath, backupPath)
	saved, err := skillmanager.ReadAccountMigration(store, expected)
	if err != nil {
		return errMigrationWritersUnknown
	}
	if binding.Action == "repair_source" {
		err = e.repairMigrationSource(ctx, store, saved, authorization, accountPath, backupPath)
	} else if binding.Action == "verify_source" {
		err = e.inspectRestoredMigrationSource(ctx, store, saved, binding.Source, binding.Target, accountPath, backupPath)
	} else if saved.State == "failed" {
		return errMigrationWritersUnknown
	} else if saved.Copy.BootID != currentBootID() {
		err = e.inspectPreviousBootMigration(ctx, store, saved, binding.Target, accountPath, backupPath)
	} else {
		err = e.recoverCompletedAccountMigration(ctx, store, saved, binding.Source, binding.Target, accountPath, backupPath)
	}
	if err != nil {
		return err
	}
	if err := skillmanager.CheckAccountMigrationHistory(store, binding.NodeID, binding.UserID, binding.AccountID); err != nil {
		return errMigrationWritersUnknown
	}
	return ctx.Err()
}
