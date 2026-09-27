package runtimehelper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"github.com/Agent-Remote/agent-remote-node/internal/toolaccounts"
)

func migrationDigest(value []string) string {
	// A slice of strings has no unsupported values or custom marshalers.
	data, _ := json.Marshal(value)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (e Engine) executeAccountMigration(ctx context.Context, task, userID, accountID, source, target, accountPath, backupPath string) error {
	record := e.accountMigrationReceipt(task, userID, accountID, source, target, accountPath, backupPath)
	copy := record.Copy
	store, err := e.openSkillStateRoot()
	if err != nil {
		return err
	}
	defer store.Close()
	saved, err := skillmanager.ReadAccountMigration(store, record)
	if err == nil {
		if saved.State != "started" && skillmanager.CheckAccountMigrationWriter(store, e.config.NodeID, userID, accountID, task) != nil {
			return errMigrationWritersUnknown
		}
		switch saved.State {
		case "succeeded":
			return nil
		case "failed":
			return errMigrationFailed
		default:
			return e.recoverCompletedAccountMigration(ctx, store, saved, source, target, accountPath, backupPath)
		}
	}
	// Older generic success caches cannot authorize either a replay or new migration writes.
	if _, cached, cacheErr := e.cachedResult(task); cacheErr != nil || cached {
		return errMigrationWritersUnknown
	}
	// Only an exact retained receipt may bypass legacy admission for read-only replay.
	if fenceErr := e.requireLegacyAccountRuntime(userID, accountID); fenceErr != nil {
		return fenceErr
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !pathInside(e.config.AccountRoot, accountPath) || !pathExists(accountPath) {
		return errors.New("managed account path was not found")
	}
	// An older copy-only record cannot be promoted to a complete migration or used to restart ownership.
	if _, err := skillmanager.ReadAccountCopy(store, copy); !errors.Is(err, os.ErrNotExist) {
		return errMigrationWritersUnknown
	}
	if err := skillmanager.CheckAccountMigrationHistory(store, e.config.NodeID, userID, accountID); err != nil {
		return errMigrationWritersUnknown
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := skillmanager.BeginAccountMigration(store, record); err != nil {
		return err
	}
	if err := e.captureMigrationBaseline(ctx, store, record, accountPath); err != nil {
		return err
	}
	if err := e.createMigrationBackupDirectory(ctx, task, backupPath); err != nil {
		return err
	}
	if err := e.backupMigratingAccount(ctx, task, userID, accountID, source, target, accountPath, backupPath); err != nil {
		return err
	}
	// No target write may precede independent byte and permission verification of the backup.
	if _, err := e.inspectMigrationCompletion(ctx, store, record, accountPath, backupPath, "failed"); err != nil {
		return err
	}
	workErr := e.applyMigrationOwnership(ctx, copy, "target", userID, accountPath, target)
	if errors.Is(workErr, errMigrationWritersUnknown) || ctx.Err() != nil {
		return errMigrationWritersUnknown
	}
	if workErr == nil {
		result, err := toolaccounts.Verify(e.config.AccountRoot, toolaccounts.VerifyPayload{ToolAccountID: accountID, ToolType: "claude", UserID: userID, Verifier: "claude", AccountRemotePath: accountPath})
		if err != nil || !result.Verified {
			workErr = errMigrationFailed
		}
	}
	outcome := "succeeded"
	if workErr != nil {
		// A drained but failed rollback still leaves source ownership unresolved.
		rollbackErr := e.applyMigrationOwnership(ctx, copy, "rollback", userID, accountPath, source)
		if rollbackErr != nil || ctx.Err() != nil {
			return errMigrationWritersUnknown
		}
		if err := e.restoreOriginalMigrationPermissions(ctx, store, record, source, accountPath, backupPath); err != nil {
			return errMigrationWritersUnknown
		}
		outcome = "failed"
	}
	phases, err := e.refreshMigrationPhases(ctx, store, copy)
	if err != nil {
		return errMigrationWritersUnknown
	}
	before, err := e.inspectMigrationCompletion(ctx, store, record, accountPath, backupPath, outcome)
	if err != nil {
		return err
	}
	if err := syncMigrationFilesystems(accountPath, backupPath); err != nil {
		return errMigrationWritersUnknown
	}
	after, err := e.inspectMigrationCompletion(ctx, store, record, accountPath, backupPath, outcome)
	if err != nil || before != after || ctx.Err() != nil || currentBootID() != record.Copy.BootID {
		return errMigrationWritersUnknown
	}
	verifiedPhases, err := e.refreshMigrationPhases(ctx, store, copy)
	if err != nil || verifiedPhases != phases || ctx.Err() != nil {
		return errMigrationWritersUnknown
	}
	if err := attestMigrationCompletion(store, record, outcome); err != nil {
		return errMigrationWritersUnknown
	}
	if err := skillmanager.FinishAccountMigration(store, record, outcome); err != nil {
		return errMigrationWritersUnknown
	}
	if outcome == "failed" {
		return errMigrationFailed
	}
	return nil
}

func (e Engine) accountMigrationReceipt(task, userID, accountID, source, target, accountPath, backupPath string) skillmanager.AccountMigrationReceipt {
	copy := skillmanager.AccountCopyReceipt{Version: 1, WriterVersion: 2, NodeID: e.config.NodeID, UserID: userID, AccountID: accountID, TaskID: task, InputDigest: migrationDigest([]string{source, target, accountPath, backupPath}), BootID: currentBootID(), Unit: skillmanager.AccountCopyUnit(task), State: "started"}
	return skillmanager.AccountMigrationReceipt{Version: 2, Copy: copy, InputDigest: migrationDigest([]string{copy.InputDigest, e.config.NodeUser, e.config.SetfaclPath, e.config.SystemdRunPath, e.config.SystemctlPath, e.config.CgroupRoot}), State: "started"}
}
