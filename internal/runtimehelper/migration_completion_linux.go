package runtimehelper

import (
	"context"
	"errors"
	"os"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"github.com/Agent-Remote/agent-remote-node/internal/toolaccounts"
)

var migrationPhases = [...]string{"copy", "target-0", "target-1", "target-2", "rollback-0", "rollback-1", "rollback-2"}

type migrationPhaseSet [len(migrationPhases)]skillmanager.MigrationWriterReceipt

func (e Engine) recoverCompletedAccountMigration(ctx context.Context, store *os.Root, original skillmanager.AccountMigrationReceipt, source, target, accountPath, backupPath string) error {
	ctx, cancel := context.WithTimeout(ctx, migrationRecoveryTimeout)
	defer cancel()
	if ctx.Err() != nil || (original.State != "started" && original.State != "succeeded") || original.Copy.WriterVersion != 2 || original.Copy.BootID != currentBootID() {
		return errMigrationWritersUnknown
	}
	phases, err := e.refreshMigrationPhases(ctx, store, original.Copy)
	if err != nil || phases[0].State != "succeeded" {
		return errMigrationWritersUnknown
	}
	copy, err := skillmanager.ReadAccountCopy(store, original.Copy)
	if err != nil || copy.WriterVersion != 2 || copy.BootID != original.Copy.BootID {
		return errMigrationWritersUnknown
	}
	if copy.State == "started" {
		if err := skillmanager.FinishAccountCopy(store, original.Copy, "copied"); err != nil {
			return errMigrationWritersUnknown
		}
	} else if copy.State != "copied" {
		return errMigrationWritersUnknown
	}
	targetIntent, err := skillmanager.ReadMigrationOwnership(store, original.Copy, "target")
	if err != nil || targetIntent.Backend != target {
		return errMigrationWritersUnknown
	}
	rollbackIntent, err := skillmanager.ReadMigrationOwnership(store, original.Copy, "rollback")
	rollback := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) || rollback && rollbackIntent.Backend != source {
		return errMigrationWritersUnknown
	}
	outcome, err := completedMigrationOutcome(phases, rollback)
	if err != nil {
		return err
	}
	inventory, err := e.inspectMigrationCompletion(ctx, store, original, accountPath, backupPath, outcome)
	if err != nil {
		return err
	}
	if outcome == "succeeded" {
		verified, err := toolaccounts.Verify(e.config.AccountRoot, toolaccounts.VerifyPayload{ToolAccountID: original.Copy.AccountID, ToolType: "claude", UserID: original.Copy.UserID, Verifier: "claude", AccountRemotePath: accountPath})
		if err != nil || !verified.Verified {
			return errMigrationWritersUnknown
		}
	}
	if ctx.Err() != nil || syncMigrationFilesystems(accountPath, backupPath) != nil {
		return errMigrationWritersUnknown
	}
	verifiedInventory, err := e.inspectMigrationCompletion(ctx, store, original, accountPath, backupPath, outcome)
	if err != nil || verifiedInventory != inventory {
		return errMigrationWritersUnknown
	}
	after, err := e.refreshMigrationPhases(ctx, store, original.Copy)
	if err != nil || after != phases || ctx.Err() != nil || currentBootID() != original.Copy.BootID {
		return errMigrationWritersUnknown
	}
	saved, err := skillmanager.ReadAccountMigration(store, original)
	if err != nil || saved != original {
		return errMigrationWritersUnknown
	}
	if err := attestMigrationCompletion(store, original, outcome); err != nil {
		return errMigrationWritersUnknown
	}
	if err := skillmanager.FinishAccountMigration(store, original, outcome); err != nil {
		return errMigrationWritersUnknown
	}
	if outcome == "failed" {
		return errMigrationFailed
	}
	return nil
}

func completedMigrationOutcome(phases migrationPhaseSet, rollback bool) (string, error) {
	selected := phases[1:4]
	outcome := "succeeded"
	if rollback {
		selected, outcome = phases[4:], "failed"
	}
	for _, phase := range selected {
		if phase.State != "succeeded" {
			return "", errMigrationWritersUnknown
		}
	}
	return outcome, nil
}

func (e Engine) refreshMigrationPhases(ctx context.Context, store *os.Root, copy skillmanager.AccountCopyReceipt) (migrationPhaseSet, error) {
	var phases migrationPhaseSet
	for index, phase := range migrationPhases {
		if ctx.Err() != nil {
			return phases, errMigrationWritersUnknown
		}
		writer, err := skillmanager.ReadMigrationPhase(store, copy, phase)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return phases, errMigrationWritersUnknown
		}
		if writer.State == "starting" || writer.State == "observed" {
			writer, err = e.observeMigrationWriter(ctx, store, writer)
			if err != nil {
				return phases, errMigrationWritersUnknown
			}
		}
		if writer.State != "succeeded" && writer.State != "failed" {
			return phases, errMigrationWritersUnknown
		}
		if err := e.confirmMigrationPhaseQuiescence(ctx, writer); err != nil {
			return phases, err
		}
		phases[index] = writer
	}
	return phases, nil
}

func (e Engine) confirmMigrationPhaseQuiescence(ctx context.Context, writer skillmanager.MigrationWriterReceipt) error {
	unit, err := skillmanager.MigrationWriterUnit(writer.Identity)
	if err != nil || currentBootID() != writer.Identity.Copy.BootID {
		return errMigrationWritersUnknown
	}
	before, err := readUnitState(ctx, e.config.SystemctlPath, unit, true, true)
	if err != nil {
		return errMigrationWritersUnknown
	}
	if before.LoadState != "not-found" {
		original, err := e.migrationWriterState(ctx, writer)
		if err != nil || original != before || !migrationWriterExited(original) {
			return errMigrationWritersUnknown
		}
	}
	if confirmEmptyCgroup(e.config.CgroupRoot, "/system.slice/"+unit) != nil {
		return errMigrationWritersUnknown
	}
	after, err := readUnitState(ctx, e.config.SystemctlPath, unit, true, true)
	if err != nil || after != before || confirmEmptyCgroup(e.config.CgroupRoot, "/system.slice/"+unit) != nil || ctx.Err() != nil {
		return errMigrationWritersUnknown
	}
	return nil
}
