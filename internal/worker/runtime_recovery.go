package worker

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Agent-Remote/agent-remote-node/internal/accountmigration"
	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
)

var errRuntimeRecovery = errors.New("backend migration recovery authority or evidence is unavailable")

func (w Worker) executeRuntimeRecovery(ctx context.Context, task api.TaskEnvelope) error {
	binding, err := accountmigration.DecodeBinding(task.Payload)
	if err != nil || binding.TaskID != task.TaskID || binding.TaskRecordID != task.TaskRecordID || binding.NodeID != task.NodeID || binding.NodeID != w.cfg.NodeID || task.LeaseAttempt <= 0 {
		return errRuntimeRecovery
	}
	if err := w.client.StartTask(ctx, task.TaskID); err != nil {
		return errRuntimeRecovery
	}
	grant, err := w.client.AuthorizeRuntimeRecovery(ctx, task.TaskID)
	if err != nil || grant.Binding != binding || grant.LeaseAttempt != task.LeaseAttempt {
		return errRuntimeRecovery
	}
	renew := func(renewCtx context.Context) (taskLeaseTiming, error) {
		lease, err := w.client.RenewRuntimeRecoveryLease(renewCtx, grant)
		return taskLeaseTiming{ServerTime: lease.ServerTime, LeaseUntil: lease.LeaseUntil, RenewAfterMilliseconds: lease.RenewAfterMilliseconds}, err
	}
	settled := false
	err = withTaskLease(ctx, errRuntimeRecovery, renew, func(workCtx context.Context) error {
		err := w.inspectAndReportRuntimeRecovery(workCtx, task, grant)
		settled = err == nil
		return err
	})
	// An acknowledged terminal result may race the renewer's legitimate terminal refusal.
	if settled {
		return nil
	}
	return err
}

func (w Worker) inspectAndReportRuntimeRecovery(ctx context.Context, task api.TaskEnvelope, grant accountmigration.Authorization) error {
	// Every poll requires fresh lease maintenance and Helper evidence; a generic ledger
	// success cannot bypass current authority, even for an already completed local migration.
	result, err := runtimehelper.NewClient(w.cfg.RuntimeSocketPath).RecoverAccountMigration(ctx, grant)
	if err != nil || !matchesRecoveryResult(result, grant) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		message := "Original backend migration still requires recovery; no account work was restarted."
		if grant.Binding.Action == "repair_source" {
			message = "Original backend migration still requires recovery; source restoration is not confirmed."
		}
		return w.client.FailTask(ctx, task.TaskID, map[string]any{
			"code":          "RUNTIME_MIGRATION_RECOVERY_REQUIRED",
			"message":       message,
			"lease_attempt": grant.LeaseAttempt,
		})
	}
	return w.client.CompleteTask(ctx, task.TaskID, map[string]any{"recovered": true, "authorization": grant})
}

func matchesRecoveryResult(result map[string]any, expected accountmigration.Authorization) bool {
	if len(result) != 2 || result["recovered"] != true {
		return false
	}
	// Marshal both as generic JSON so struct field order never affects identity.
	data, err := json.Marshal(result["authorization"])
	if err != nil {
		return false
	}
	var received accountmigration.Authorization
	if err := json.Unmarshal(data, &received); err != nil || received != expected {
		return false
	}
	var raw map[string]json.RawMessage
	// Binding.UnmarshalJSON already enforces the exact version-specific field set.
	return json.Unmarshal(data, &raw) == nil && len(raw) == 2
}
