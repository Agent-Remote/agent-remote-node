package runtimehelper

import (
	"errors"
	"strings"

	"github.com/Agent-Remote/agent-remote-node/internal/toolaccounts"
)

// ConfigImportRequest carries fresh ownership metadata without worker-selected roots or identities.
type ConfigImportRequest struct {
	Account        toolaccounts.ImportConfigPayload `json:"account"`
	DirectoryMode  string                           `json:"directory_mode"`
	DirectoryEpoch int64                            `json:"directory_epoch"`
}

var errConfigImportPending = errors.New("CONFIG_IMPORT_PENDING")
var errConfigImportFailed = errors.New("CONFIG_IMPORT_FAILED")

func validateConfigImport(requestID string, request ConfigImportRequest) error {
	account := request.Account
	if !validSkillUUID(account.UserID) || !validSkillUUID(account.ToolAccountID) || account.ToolType != "claude" || account.AccountRemotePath != "" ||
		(account.RuntimeBackend != "native" && account.RuntimeBackend != "docker_sandbox") {
		return errors.New("invalid config import account binding")
	}
	prefix := "import_tool_account_config:" + account.ToolAccountID + ":"
	if !strings.HasPrefix(requestID, prefix) || !validSkillUUID(strings.TrimPrefix(requestID, prefix)) {
		return errors.New("invalid config import task identity")
	}
	if (request.DirectoryMode != "legacy" && request.DirectoryMode != "migrating" && request.DirectoryMode != "managed_v1") || request.DirectoryEpoch < 0 || (request.DirectoryMode != "legacy" && request.DirectoryEpoch == 0) {
		return errors.New("invalid config import directory authorization")
	}
	return nil
}

func configImportResult(root string, payload toolaccounts.ImportConfigPayload) map[string]any {
	paths := make([]string, len(payload.Files))
	for index, file := range payload.Files {
		paths[index] = file.Path
	}
	return map[string]any{
		"status": "imported", "tool_account_id": payload.ToolAccountID, "tool_type": payload.ToolType,
		"account_remote_path": root, "files_written": paths, "files_written_count": len(paths),
	}
}
