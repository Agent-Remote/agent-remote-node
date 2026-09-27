package worker

import (
	"errors"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/toolaccounts"
)

func isConfigImportOwnershipError(task api.TaskEnvelope, err error) bool {
	if task.TaskType != "import_tool_account_config" {
		return false
	}
	var helper *runtimehelper.Error
	if errors.As(err, &helper) && helper.Code == "SKILL_MANAGER_OWNS_PATH" {
		return true
	}
	var remote *api.HTTPError
	return errors.Is(err, toolaccounts.ErrSkillManagerOwnsPath) || (errors.As(err, &remote) && remote.Code == "SKILL_MANAGER_OWNS_PATH")
}

func configImportReceiptError(task api.TaskEnvelope, err error) map[string]any {
	if task.TaskType != "import_tool_account_config" {
		return nil
	}
	var helper *runtimehelper.Error
	if !errors.As(err, &helper) {
		return nil
	}
	switch helper.Code {
	case "CONFIG_IMPORT_PENDING":
		return map[string]any{"code": helper.Code, "message": "Configuration import outcome requires reconciliation; files were not replayed."}
	case "CONFIG_IMPORT_FAILED":
		return map[string]any{"code": helper.Code, "message": "Configuration import failed; files were not replayed."}
	default:
		return nil
	}
}
