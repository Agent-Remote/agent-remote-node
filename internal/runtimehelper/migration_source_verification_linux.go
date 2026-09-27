package runtimehelper

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (e Engine) inspectRestoredMigrationSource(ctx context.Context, store *os.Root, original skillmanager.AccountMigrationReceipt, source, target, account, backup string) error {
	ctx, cancel := context.WithTimeout(ctx, migrationRecoveryTimeout)
	defer cancel()
	boot := currentBootID()
	if !validSkillUUID(boot) || original.Version != 2 || original.Copy.WriterVersion != 2 || original.State != "failed" {
		return errMigrationWritersUnknown
	}
	// The immutable whole failure and its baseline-bound attestation must already exist.
	if skillmanager.CheckAccountMigrationWriter(store, original.Copy.NodeID, original.Copy.UserID, original.Copy.AccountID, original.Copy.TaskID) != nil {
		return errMigrationWritersUnknown
	}
	targetIntent, err := skillmanager.ReadMigrationOwnership(store, original.Copy, "target")
	if err != nil || targetIntent.Backend != target {
		return errMigrationWritersUnknown
	}
	sourceIntent, err := skillmanager.ReadMigrationOwnership(store, original.Copy, "rollback")
	if err != nil || sourceIntent.Backend != source {
		return errMigrationWritersUnknown
	}
	before, err := e.inspectRestoredMigrationPhases(ctx, store, original.Copy, boot)
	if err != nil {
		return err
	}
	inventory, err := e.inspectMigrationCompletion(ctx, store, original, account, backup, "failed")
	if err != nil || ctx.Err() != nil || syncMigrationFilesystems(account, backup) != nil {
		return errMigrationWritersUnknown
	}
	afterInventory, err := e.inspectMigrationCompletion(ctx, store, original, account, backup, "failed")
	if err != nil || afterInventory != inventory {
		return errMigrationWritersUnknown
	}
	after, err := e.inspectRestoredMigrationPhases(ctx, store, original.Copy, boot)
	if err != nil || before != after {
		return errMigrationWritersUnknown
	}
	verifiedTarget, err := skillmanager.ReadMigrationOwnership(store, original.Copy, "target")
	if err != nil || verifiedTarget != targetIntent {
		return errMigrationWritersUnknown
	}
	verifiedSource, err := skillmanager.ReadMigrationOwnership(store, original.Copy, "rollback")
	if err != nil || verifiedSource != sourceIntent {
		return errMigrationWritersUnknown
	}
	saved, err := skillmanager.ReadAccountMigration(store, original)
	if err != nil || saved != original || skillmanager.CheckAccountMigrationWriter(store, original.Copy.NodeID, original.Copy.UserID, original.Copy.AccountID, original.Copy.TaskID) != nil || currentBootID() != boot || ctx.Err() != nil {
		return errMigrationWritersUnknown
	}
	return nil
}

func (e Engine) inspectRestoredMigrationPhases(ctx context.Context, store *os.Root, copy skillmanager.AccountCopyReceipt, boot string) (migrationPhaseSet, error) {
	return e.inspectPassiveMigrationPhases(ctx, store, copy, boot, true)
}

// Inspection never advances original writer observations, including interrupted launches.
func (e Engine) inspectPassiveMigrationPhases(ctx context.Context, store *os.Root, copy skillmanager.AccountCopyReceipt, boot string, requireRollback bool) (migrationPhaseSet, error) {
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
		if errors.Is(err, os.ErrNotExist) && index != 0 && (!requireRollback || index <= 3) {
			continue
		}
		if err != nil || index == 0 && writer.State != "succeeded" {
			return phases, errMigrationWritersUnknown
		}
		if requireRollback && ((writer.State != "succeeded" && writer.State != "failed") || (index >= 4 && writer.State != "succeeded")) {
			return phases, errMigrationWritersUnknown
		}
		unit, err := skillmanager.MigrationWriterUnit(writer.Identity)
		if err != nil {
			return phases, errMigrationWritersUnknown
		}
		group, groupErr := groups.Lstat(filepath.Join("system.slice", unit))
		if groupErr != nil && !errors.Is(groupErr, os.ErrNotExist) || groupErr == nil && !group.IsDir() {
			return phases, errMigrationWritersUnknown
		}
		if boot == copy.BootID {
			if e.confirmMigrationPhaseQuiescence(ctx, writer) != nil {
				return phases, errMigrationWritersUnknown
			}
		} else {
			state, err := readUnitState(ctx, e.config.SystemctlPath, unit, true, true)
			if err != nil || state.LoadState != "not-found" {
				return phases, errMigrationWritersUnknown
			}
			if !errors.Is(groupErr, os.ErrNotExist) {
				return phases, errMigrationWritersUnknown
			}
		}
		phases[index] = writer
	}
	return phases, nil
}
