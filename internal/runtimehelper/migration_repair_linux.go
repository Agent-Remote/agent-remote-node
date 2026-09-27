package runtimehelper

import (
	"context"
	"errors"
	"os"

	"github.com/Agent-Remote/agent-remote-node/internal/accountmigration"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// Only the explicit repair action reaches these synchronous permission writes. The
// socket handler owns lifecycle exclusion and cancels them when the live grant ends.
func (e Engine) repairMigrationSource(ctx context.Context, store *os.Root, original skillmanager.AccountMigrationReceipt, authorization accountmigration.Authorization, account, backup string) error {
	ctx, cancel := context.WithTimeout(ctx, migrationRecoveryTimeout)
	defer cancel()
	boot, binding := currentBootID(), authorization.Binding
	if !validSkillUUID(boot) || ctx.Err() != nil || binding.Action != "repair_source" || binding.Version != 3 || authorization.LeaseAttempt <= 0 {
		return errMigrationWritersUnknown
	}
	intent, err := skillmanager.PrepareAccountMigrationRepair(store, original, binding.OriginalTaskRecordID, binding.Source, binding.Target)
	if err != nil {
		return errMigrationWritersUnknown
	}
	retained, err := skillmanager.ReadAccountMigrationRepairIntent(store, original.Copy.TaskID)
	started := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) || started && retained != intent {
		return errMigrationWritersUnknown
	}
	completion, err := skillmanager.ReadAccountMigrationRepairCompletion(store, original.Copy.TaskID)
	completed := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errMigrationWritersUnknown
	}
	phases, err := e.inspectPassiveMigrationPhases(ctx, store, original.Copy, boot, false)
	if err != nil {
		return err
	}
	if !completed {
		if err := e.restoreInterruptedMigration(ctx, store, intent, authorization, phases, boot, started, account, backup); err != nil {
			return err
		}
	}
	if err := e.verifyMigrationRepair(ctx, store, intent, phases, boot, account, backup); err != nil {
		return err
	}
	if completed {
		// A lost reply authorizes a fresh read, never another writer or completion identity.
		again, err := skillmanager.ReadAccountMigrationRepairCompletion(store, original.Copy.TaskID)
		if err != nil || again != completion || ctx.Err() != nil {
			return errMigrationWritersUnknown
		}
		return nil
	}
	attempt, err := skillmanager.NewAccountMigrationRepairAttempt(intent, binding.TaskID, binding.TaskRecordID, authorization.LeaseAttempt, boot)
	if err != nil || ctx.Err() != nil || currentBootID() != boot {
		return errMigrationWritersUnknown
	}
	return skillmanager.FinishAccountMigrationRepair(store, attempt)
}

func (e Engine) restoreInterruptedMigration(ctx context.Context, store *os.Root, intent skillmanager.AccountMigrationRepairIntent, authorization accountmigration.Authorization, phases migrationPhaseSet, boot string, started bool, account, backup string) error {
	original := intent.Migration
	before, err := e.inspectMigrationCompletion(ctx, store, original, account, backup, "succeeded")
	if err != nil {
		return err
	}
	baseline, err := skillmanager.ReadAccountMigrationBaseline(store, original)
	if err != nil {
		return errMigrationWritersUnknown
	}
	// First-run exact restoration was possible only after all source writers succeeded.
	// Otherwise only our retained intent can explain a chmod interrupted during repair.
	_, rollbackErr := completedMigrationOutcome(phases, true)
	parents, err := e.inspectMigrationRestorationParents(ctx, original, baseline, phases, intent.Source, started || rollbackErr == nil)
	if err != nil {
		return err
	}
	again, err := e.inspectPassiveMigrationPhases(ctx, store, original.Copy, boot, false)
	if err != nil || again != phases || ctx.Err() != nil || currentBootID() != boot {
		return errMigrationWritersUnknown
	}
	if err := skillmanager.BeginAccountMigrationRepair(store, intent); err != nil {
		return errMigrationWritersUnknown
	}
	binding := authorization.Binding
	attempt, err := skillmanager.NewAccountMigrationRepairAttempt(intent, binding.TaskID, binding.TaskRecordID, authorization.LeaseAttempt, boot)
	if err != nil || ctx.Err() != nil {
		return errMigrationWritersUnknown
	}
	if err := skillmanager.BeginAccountMigrationRepairAttempt(store, intent, attempt); err != nil {
		return errMigrationWritersUnknown
	}
	if err := restoreMigrationTreePermissions(ctx, account, backup, before.inventory); err != nil {
		return errMigrationWritersUnknown
	}
	for index, parent := range baseline.Parents {
		if err := restoreMigrationParent(ctx, parent, parents[index]); err != nil {
			return errMigrationWritersUnknown
		}
	}
	return ctx.Err()
}

func (e Engine) verifyMigrationRepair(ctx context.Context, store *os.Root, intent skillmanager.AccountMigrationRepairIntent, phases migrationPhaseSet, boot, account, backup string) error {
	original := intent.Migration
	before, err := e.inspectMigrationCompletion(ctx, store, original, account, backup, "failed")
	if err != nil || ctx.Err() != nil {
		return errMigrationWritersUnknown
	}
	baseline, err := skillmanager.ReadAccountMigrationBaseline(store, original)
	if err != nil {
		return errMigrationWritersUnknown
	}
	paths := []string{account, backup}
	for _, parent := range baseline.Parents {
		paths = append(paths, parent.Path)
	}
	if syncMigrationFilesystems(paths...) != nil || ctx.Err() != nil {
		return errMigrationWritersUnknown
	}
	after, err := e.inspectMigrationCompletion(ctx, store, original, account, backup, "failed")
	if err != nil || before != after {
		return errMigrationWritersUnknown
	}
	again, err := e.inspectPassiveMigrationPhases(ctx, store, original.Copy, boot, false)
	if err != nil || phases != again {
		return errMigrationWritersUnknown
	}
	saved, err := skillmanager.ReadAccountMigrationRepairIntent(store, original.Copy.TaskID)
	if err != nil || saved != intent || currentBootID() != boot || ctx.Err() != nil {
		return errMigrationWritersUnknown
	}
	return nil
}
