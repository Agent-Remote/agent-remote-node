package runtimehelper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (e Engine) backupMigratingAccount(ctx context.Context, taskID, userID, accountID, source, target, accountPath, backupPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	input, err := json.Marshal([]string{source, target, accountPath, backupPath})
	if err != nil {
		return err
	}
	digest := sha256.Sum256(input)
	record := skillmanager.AccountCopyReceipt{Version: 1, WriterVersion: 2, NodeID: e.config.NodeID, UserID: userID, AccountID: accountID, TaskID: taskID, InputDigest: hex.EncodeToString(digest[:]), BootID: currentBootID(), Unit: skillmanager.AccountCopyUnit(taskID), State: "started"}
	store, err := e.openSkillStateRoot()
	if err != nil {
		return err
	}
	defer store.Close()
	saved, err := skillmanager.ReadAccountCopy(store, record)
	if err == nil {
		if saved.State == "failed" {
			return errAccountCopyFailed
		}
		return errAccountCopyPending
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := skillmanager.BeginAccountCopy(store, record); err != nil {
		return err
	}
	copyCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	copied, drained := e.runAccountCopy(copyCtx, record, accountPath, backupPath)
	// A launch error or cancellation alone says nothing about descendants. Preserve started evidence.
	if !drained || currentBootID() != record.BootID {
		return errAccountCopyPending
	}
	state := "failed"
	if copied {
		state = "copied"
	}
	if err := skillmanager.FinishAccountCopy(store, record, state); err != nil {
		return errAccountCopyPending
	}
	if !copied {
		return errAccountCopyFailed
	}
	return ctx.Err()
}

func (e Engine) runAccountCopy(ctx context.Context, record skillmanager.AccountCopyReceipt, source, target string) (copied, drained bool) {
	return e.runMigrationWriter(ctx, record, "copy", "/bin/cp", "--archive", "--reflink=auto", "--", source+"/.", target+"/")
}
