package worker

import (
	"errors"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
)

func accountMigrationTaskError(task api.TaskEnvelope, err error) map[string]any {
	switch task.TaskType {
	case "create_tool_session", "create_binding_session", "migrate_tool_account_runtime", "import_tool_account_config":
		var remote *api.HTTPError
		if errors.As(err, &remote) && remote.Code == "RUNTIME_MIGRATION_PENDING" {
			return map[string]any{"code": remote.Code, "message": "Retained backend migration prevents new account writes."}
		}
		var helper *runtimehelper.Error
		if !errors.As(err, &helper) {
			return nil
		}
		if helper.Code == "STATE_MIGRATION_PENDING" || task.TaskType == "migrate_tool_account_runtime" && (helper.Code == "STATE_COPY_PENDING" || helper.Code == "STATE_COPY_FAILED" || helper.Code == "STATE_MIGRATION_FAILED") {
			return map[string]any{"code": helper.Code, "message": "Backend migration requires inspection; original account and retained writer evidence remain protected."}
		}
		if helper.Code == "MIGRATION_PENDING" {
			return map[string]any{
				"code":    "MIGRATION_PENDING",
				"message": "Account skill migration prevents starting a legacy runtime or changing its backend.",
			}
		}
	}
	return nil
}
