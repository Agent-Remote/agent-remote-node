package worker

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
)

func TestRuntimeMigrationDenialsKeepStableContentFreeTaskError(t *testing.T) {
	err := fmt.Errorf("helper call: %w", &runtimehelper.Error{Code: "MIGRATION_PENDING", Message: "private runtime content"})
	for _, kind := range []string{"create_tool_session", "create_binding_session", "migrate_tool_account_runtime"} {
		failure := contentSafeTaskError(api.TaskEnvelope{TaskType: kind}, err)
		if failure["code"] != "MIGRATION_PENDING" || strings.Contains(failure["message"].(string), "private") {
			t.Fatalf("migration denial lost its safe code: %v", failure)
		}
	}
	if accountMigrationTaskError(api.TaskEnvelope{TaskType: "stop_tool_session"}, err) != nil {
		t.Fatal("legacy writer admission blocked the stop classification")
	}
}

func TestAccountCopyTaskErrorPreservesOnlyMigrationCodes(t *testing.T) {
	for _, code := range []string{"STATE_COPY_PENDING", "STATE_COPY_FAILED", "STATE_MIGRATION_PENDING", "STATE_MIGRATION_FAILED"} {
		err := &runtimehelper.Error{Code: code, Message: "private raw diagnostic"}
		result := accountMigrationTaskError(api.TaskEnvelope{TaskType: "migrate_tool_account_runtime"}, err)
		if result["code"] != code || result["message"] == err.Message {
			t.Fatal(result)
		}
		if result := accountMigrationTaskError(api.TaskEnvelope{TaskType: "create_tool_session"}, err); (result != nil) != (code == "STATE_MIGRATION_PENDING") {
			t.Fatal(result)
		}
	}
}

func TestBackendMigrationAdmissionErrorsAreStableForEveryWriter(t *testing.T) {
	for _, kind := range []string{"create_tool_session", "create_binding_session", "migrate_tool_account_runtime", "import_tool_account_config"} {
		for _, code := range []string{"STATE_MIGRATION_PENDING", "RUNTIME_MIGRATION_PENDING"} {
			var err error = &runtimehelper.Error{Code: code, Message: "private raw diagnostic"}
			if code == "RUNTIME_MIGRATION_PENDING" {
				err = &api.HTTPError{Code: code, Message: "private raw diagnostic"}
			}
			failure := contentSafeTaskError(api.TaskEnvelope{TaskType: kind}, fmt.Errorf("request failed: %w", err))
			if failure["code"] != code || strings.Contains(failure["message"].(string), "private") {
				t.Fatal("writer admission lost its bounded diagnostic", kind, failure)
			}
			if accountMigrationTaskError(api.TaskEnvelope{TaskType: "stop_tool_session"}, err) != nil {
				t.Fatal("migration admission altered stop handling")
			}
		}
	}
}
