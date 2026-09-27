package runtimehelper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func retainIncompleteMigration(t *testing.T, engine Engine, userID, accountID, phase string) {
	t.Helper()
	store, err := engine.openSkillStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	task := "migrate_tool_account_runtime:" + accountID + ":aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	copy := skillmanager.AccountCopyReceipt{Version: 1, NodeID: engine.config.NodeID, UserID: userID,
		AccountID: accountID, TaskID: task, InputDigest: strings.Repeat("a", 64), BootID: currentBootID(),
		Unit: skillmanager.AccountCopyUnit(task), State: "started"}
	if phase != "intent" {
		if err := skillmanager.BeginAccountCopy(store, copy); err != nil {
			t.Fatal(err)
		}
		if phase == "failed-copy" {
			if err := skillmanager.FinishAccountCopy(store, copy, "failed"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if phase != "copy-only" {
		if err := skillmanager.BeginAccountMigration(store, skillmanager.AccountMigrationReceipt{
			Version: 1, Copy: copy, InputDigest: strings.Repeat("b", 64), State: "started",
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestIncompleteMigrationBlocksLegacyWritersAfterRestart(t *testing.T) {
	for _, phase := range []string{"intent", "copy-only", "failed-copy", "started"} {
		for _, operation := range []string{"prepare_account", "start_session", "docker_prepare_account", "docker_start_session", "migrate_account", "import_account_config"} {
			t.Run(phase+"/"+operation, func(t *testing.T) {
				engine, request := configImportFixture(t)
				var payload ConfigImportRequest
				if err := decodeStrictPayload(request.Payload, &payload); err != nil {
					t.Fatal(err)
				}
				account := payload.Account
				retainIncompleteMigration(t, engine, account.UserID, account.ToolAccountID, phase)
				if operation != "import_account_config" {
					request = Request{Version: ProtocolVersion, RequestID: "delayed-" + operation, Operation: operation,
						Payload: map[string]any{"user_id": account.UserID, "tool_account_id": account.ToolAccountID,
							"tool_type": "claude", "session_id": "55555555-5555-4555-8555-555555555555",
							"binding_id": "66666666-6666-4666-8666-666666666666", "workspace_id": "77777777-7777-4777-8777-777777777777",
							"tmux_session_name": "test-session", "sandbox_name": "test-sandbox",
							"source_runtime_backend": "docker_sandbox", "target_runtime_backend": "native"}}
				}
				for range 2 {
					_, err := NewEngine(engine.config).Execute(context.Background(), request)
					if !errors.Is(err, errMigrationWritersUnknown) || classifyError(err) != "STATE_MIGRATION_PENDING" {
						t.Fatal("incomplete migration admitted a writer", err)
					}
				}
				if err := engine.requireLegacyAccountRuntime(account.UserID, "99999999-9999-4999-8999-999999999999"); err != nil {
					t.Fatal("migration blocked an unrelated account", err)
				}
				entries, err := os.ReadDir(engine.config.AccountRoot)
				if err != nil || len(entries) != 0 {
					t.Fatal("rejected writer changed the account", err)
				}
				for _, root := range []string{engine.config.StateRoot, engine.config.WorkspaceRoot} {
					if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("rejected writer changed runtime or workspace", err)
					}
				}
			})
		}
	}
}

func TestIncompleteMigrationPreservesCompletedImportReplay(t *testing.T) {
	engine, request := configImportFixture(t)
	if _, err := engine.Execute(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	var payload ConfigImportRequest
	if err := decodeStrictPayload(request.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	account := payload.Account
	path := filepath.Join(engine.config.AccountRoot, account.UserID, "tool-accounts", "claude", account.ToolAccountID, ".claude", "settings.json")
	if err := os.WriteFile(path, []byte("later local configuration"), 0600); err != nil {
		t.Fatal(err)
	}
	retainIncompleteMigration(t, engine, account.UserID, account.ToolAccountID, "started")
	if _, err := NewEngine(engine.config).Execute(context.Background(), request); err != nil {
		t.Fatal("read-only import replay was blocked", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "later local configuration" {
		t.Fatal("completed import replay rewrote the account", err)
	}
}
