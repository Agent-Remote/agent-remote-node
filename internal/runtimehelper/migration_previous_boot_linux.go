package runtimehelper

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"github.com/Agent-Remote/agent-remote-node/internal/toolaccounts"
)

func (e Engine) inspectPreviousBootMigration(ctx context.Context, store *os.Root, original skillmanager.AccountMigrationReceipt, target, accountPath, backupPath string) error {
	ctx, cancel := context.WithTimeout(ctx, migrationRecoveryTimeout)
	defer cancel()
	boot := currentBootID()
	if !validSkillUUID(boot) || boot == original.Copy.BootID || original.Copy.WriterVersion != 2 || original.State != "succeeded" {
		return errMigrationWritersUnknown
	}
	// Only whole completion follows filesystem sync. Old phase success alone cannot attest durability.
	if err := skillmanager.CheckAccountMigrationWriter(store, original.Copy.NodeID, original.Copy.UserID, original.Copy.AccountID, original.Copy.TaskID); err != nil {
		return errMigrationWritersUnknown
	}
	intent, err := skillmanager.ReadMigrationOwnership(store, original.Copy, "target")
	if err != nil || intent.Backend != target {
		return errMigrationWritersUnknown
	}
	before, err := e.inspectPreviousBootMigrationPhases(ctx, store, original.Copy, boot)
	if err != nil {
		return err
	}
	inventory, err := e.inspectMigrationCompletion(ctx, store, original, accountPath, backupPath, "succeeded")
	if err != nil {
		return err
	}
	verified, err := toolaccounts.Verify(e.config.AccountRoot, toolaccounts.VerifyPayload{ToolAccountID: original.Copy.AccountID, ToolType: "claude", UserID: original.Copy.UserID, Verifier: "claude", AccountRemotePath: accountPath})
	if err != nil || !verified.Verified || syncMigrationFilesystems(accountPath, backupPath) != nil {
		return errMigrationWritersUnknown
	}
	verifiedInventory, err := e.inspectMigrationCompletion(ctx, store, original, accountPath, backupPath, "succeeded")
	if err != nil || verifiedInventory != inventory {
		return errMigrationWritersUnknown
	}
	after, err := e.inspectPreviousBootMigrationPhases(ctx, store, original.Copy, boot)
	if err != nil || before != after {
		return errMigrationWritersUnknown
	}
	saved, err := skillmanager.ReadAccountMigration(store, original)
	if err != nil || saved != original || skillmanager.CheckAccountMigrationWriter(store, original.Copy.NodeID, original.Copy.UserID, original.Copy.AccountID, original.Copy.TaskID) != nil || ctx.Err() != nil || currentBootID() != boot {
		return errMigrationWritersUnknown
	}
	return nil
}

func (e Engine) inspectPreviousBootMigrationPhases(ctx context.Context, store *os.Root, copy skillmanager.AccountCopyReceipt, boot string) (migrationPhaseSet, error) {
	var phases migrationPhaseSet
	groups, err := os.OpenRoot(e.config.CgroupRoot)
	if err != nil {
		return phases, errMigrationWritersUnknown
	}
	defer groups.Close()
	for index, phase := range migrationPhases {
		if ctx.Err() != nil || currentBootID() != boot {
			return phases, errMigrationWritersUnknown
		}
		writer, err := skillmanager.ReadMigrationPhase(store, copy, phase)
		if errors.Is(err, os.ErrNotExist) && index >= 4 {
			continue
		}
		if err != nil || writer.State != "succeeded" || index >= 4 {
			return phases, errMigrationWritersUnknown
		}
		unit, err := skillmanager.MigrationWriterUnit(writer.Identity)
		if err != nil {
			return phases, errMigrationWritersUnknown
		}
		state, err := readUnitState(ctx, e.config.SystemctlPath, unit, true, true)
		if err != nil || state.LoadState != "not-found" {
			return phases, errMigrationWritersUnknown
		}
		// A cgroup surviving in this boot belongs to current work, even when presently empty.
		if _, err := groups.Lstat(filepath.Join("system.slice", unit)); !errors.Is(err, os.ErrNotExist) {
			return phases, errMigrationWritersUnknown
		}
		phases[index] = writer
	}
	return phases, nil
}
