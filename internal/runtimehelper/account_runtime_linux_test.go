package runtimehelper

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func accountRuntimeFixture(t *testing.T) (Engine, map[string]any) {
	t.Helper()
	engine, request := configImportFixture(t)
	var payload ConfigImportRequest
	if err := decodeStrictPayload(request.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	store, err := engine.openSkillStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := skillmanager.CloseAccountImports(store, skillmanager.AccountFence{
		Version: 1, NodeID: engine.config.NodeID, UserID: payload.Account.UserID,
		AccountID: payload.Account.ToolAccountID, DirectoryEpoch: 1,
	}); err != nil {
		t.Fatal(err)
	}
	return engine, map[string]any{
		"user_id": payload.Account.UserID, "tool_account_id": payload.Account.ToolAccountID,
		"tool_type": "claude", "session_id": "55555555-5555-4555-8555-555555555555",
		"binding_id": "66666666-6666-4666-8666-666666666666", "workspace_id": "77777777-7777-4777-8777-777777777777",
		"tmux_session_name": "test-session", "sandbox_name": "test-sandbox",
		"source_runtime_backend": "docker_sandbox", "target_runtime_backend": "native",
	}
}

func TestAccountFenceBlocksEveryLegacyWriterAfterRestartBeforeMutation(t *testing.T) {
	for _, operation := range []string{"prepare_account", "start_session", "docker_prepare_account", "docker_start_session", "migrate_account"} {
		t.Run(operation, func(t *testing.T) {
			engine, payload := accountRuntimeFixture(t)
			// Neither an old queued grant nor a claimed managed snapshot is launch authority.
			payload["directory_mode"] = "managed_v1"
			payload["skill_snapshot_id"] = "88888888-8888-4888-8888-888888888888"
			request := Request{Version: ProtocolVersion, RequestID: "delayed-" + operation, Operation: operation, Payload: payload}
			for range 2 {
				_, err := NewEngine(engine.config).Execute(context.Background(), request)
				if !errors.Is(err, errAccountMigrationPending) || classifyError(err) != "MIGRATION_PENDING" {
					t.Fatalf("delayed legacy writer was not fenced: %v", err)
				}
			}
			entries, err := os.ReadDir(engine.config.AccountRoot)
			if err != nil || len(entries) != 0 {
				t.Fatalf("rejected writer changed account files: %v %v", entries, err)
			}
			for _, root := range []string{engine.config.StateRoot, engine.config.WorkspaceRoot} {
				if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("rejected writer changed runtime or workspace: %v", err)
				}
			}
		})
	}
}

func TestAccountFenceRejectsUnsafeMetadataWithoutBlockingUnrelatedAccount(t *testing.T) {
	for _, kind := range []string{"corrupt", "permissions", "symlink", "foreign_node", "foreign_user"} {
		t.Run(kind, func(t *testing.T) {
			engine, payload := accountRuntimeFixture(t)
			userID, accountID := payload["user_id"].(string), payload["tool_account_id"].(string)
			if err := engine.requireLegacyAccountRuntime(userID, "99999999-9999-4999-8999-999999999999"); err != nil {
				t.Fatal("unrelated account inherited the fence", err)
			}
			path := filepath.Join(engine.config.SkillStateRoot, "account-"+accountID+".json")
			var err error
			switch kind {
			case "corrupt":
				err = os.WriteFile(path, []byte("{"), 0o600)
			case "permissions":
				err = os.Chmod(path, 0o666)
			case "symlink":
				if err = os.Remove(path); err == nil {
					err = os.Symlink("missing", path)
				}
			case "foreign_node":
				engine.config.NodeID = "99999999-9999-4999-8999-999999999999"
			case "foreign_user":
				userID = "99999999-9999-4999-8999-999999999999"
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := NewEngine(engine.config).requireLegacyAccountRuntime(userID, accountID); err == nil {
				t.Fatal("unsafe or foreign fence reopened runtime admission")
			}
			_, err = engine.Execute(context.Background(), Request{
				Version: ProtocolVersion, RequestID: "import_tool_account_config:" + accountID + ":aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
				Operation: "import_account_config", Payload: map[string]any{
					"directory_mode": "legacy", "directory_epoch": 0,
					"account": map[string]any{
						"user_id": userID, "tool_account_id": accountID, "tool_type": "claude", "runtime_backend": "docker_sandbox",
						"files": []any{map[string]any{"path": "~/.claude/skills/demo/SKILL.md", "content_base64": "YQ==", "mode": 384}},
					},
				},
			})
			if err == nil {
				t.Fatal("unsafe or foreign fence reopened skill imports")
			}
			entries, err := os.ReadDir(engine.config.AccountRoot)
			if err != nil || len(entries) != 0 {
				t.Fatalf("invalid fence permitted account mutation: %v %v", entries, err)
			}
		})
	}
}

func TestAccountFenceGuardsDirectNativeLaunchButKeepsCompletedReplay(t *testing.T) {
	engine, payload := accountRuntimeFixture(t)
	spec := SessionSpec{
		UserID:      payload["user_id"].(string),
		AccountPath: filepath.Join(engine.config.AccountRoot, payload["user_id"].(string), "tool-accounts", "claude", payload["tool_account_id"].(string)),
	}
	if err := engine.launch(context.Background(), spec); !errors.Is(err, errAccountMigrationPending) {
		t.Fatalf("direct legacy launch bypassed the fence: %v", err)
	}
	spec.SkillSnapshotID = "88888888-8888-4888-8888-888888888888"
	spec.SessionID = payload["session_id"].(string)
	if err := engine.launch(context.Background(), spec); err == nil {
		t.Fatal("claimed snapshot launched without a retained private bundle")
	}
	request := Request{Version: ProtocolVersion, RequestID: "completed-binding", Operation: "prepare_account", Payload: payload}
	if err := engine.saveResult(request.RequestID, map[string]any{"status": "saved-result"}); err != nil {
		t.Fatal(err)
	}
	result, err := NewEngine(engine.config).Execute(context.Background(), request)
	if err != nil || result["status"] != "saved-result" {
		t.Fatalf("completed task did not replay: %v %v", result, err)
	}
	entries, err := os.ReadDir(engine.config.AccountRoot)
	if err != nil || len(entries) != 0 {
		t.Fatalf("completed replay wrote account files: %v %v", entries, err)
	}
}

func TestDockerWriterCannotRedirectUnfencedIdentityIntoFencedAccount(t *testing.T) {
	for _, operation := range []string{"docker_prepare_account", "docker_start_session"} {
		t.Run(operation, func(t *testing.T) {
			engine, payload := accountRuntimeFixture(t)
			fencedPath := filepath.Join(engine.config.AccountRoot, payload["user_id"].(string), "tool-accounts", "claude", payload["tool_account_id"].(string))
			payload["account_remote_path"] = fencedPath
			payload["tool_account_id"] = "99999999-9999-4999-8999-999999999999"
			engine.config.TmuxBinaryPath = "agent-remote-missing-tmux"
			engine.config.DockerBinaryPath = supportedDockerCommand(t)
			engine.config.SetfaclPath = writeTestCommand(t, "setfacl", "exit 0")
			result, err := engine.Execute(context.Background(), Request{Version: ProtocolVersion, RequestID: "other-account", Operation: operation, Payload: payload})
			if err != nil {
				t.Fatal(err)
			}
			expected := filepath.Join(engine.config.AccountRoot, payload["user_id"].(string), "tool-accounts", "claude", payload["tool_account_id"].(string))
			if result["account_remote_path"] != expected {
				t.Fatal("Docker writer retained a caller-selected account path")
			}
			if _, err := os.Lstat(fencedPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("another identity wrote into the fenced account: %v", err)
			}
		})
	}
}

func TestAccountFenceCannotBeBypassedThroughFilesystemAliases(t *testing.T) {
	for _, depth := range []int{1, 2, 3, 4, 5, 6} {
		t.Run(strings.Repeat("ancestor-", depth), func(t *testing.T) {
			engine, payload := accountRuntimeFixture(t)
			payload["tool_account_id"] = "99999999-9999-4999-8999-999999999999"
			parts := []string{engine.config.AccountRoot, payload["user_id"].(string), "tool-accounts", "claude", payload["tool_account_id"].(string), ".claude", "skills"}
			alias := filepath.Join(parts[:depth+1]...)
			if err := os.MkdirAll(filepath.Dir(alias), 0o700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(filepath.Dir(engine.config.AccountRoot), "retained-source")
			if err := os.Mkdir(target, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, alias); err != nil {
				t.Fatal(err)
			}
			_, err := engine.Execute(context.Background(), Request{Version: ProtocolVersion, RequestID: "alias-launch", Operation: "docker_prepare_account", Payload: payload})
			if err == nil || !strings.Contains(err.Error(), "unsafe runtime account directory alias") {
				t.Fatalf("filesystem alias bypassed the selected account identity: %v", err)
			}
			entries, err := os.ReadDir(target)
			if err != nil || len(entries) != 0 {
				t.Fatalf("alias target was modified: %v %v", entries, err)
			}
		})
	}
}

func TestAccountFenceDoesNotStopExistingLegacySessionOrPreventExplicitStop(t *testing.T) {
	engine, payload := accountRuntimeFixture(t)
	sessionID, userID := payload["session_id"].(string), payload["user_id"].(string)
	unit := "agent-remote-session-" + shortDigest(sessionID, 12) + ".service"
	before := unitFields("active", "/system.slice/"+unit, "success", "0", "0")
	command, stateRoot := nativeStopCommands(t, before, unitFields("inactive", "", "success", "1", "0"), false)
	engine.config.SystemctlPath = command
	engine.config.IPPath = writeTestCommand(t, "ip", "exit 0")
	engine.config.CgroupRoot = t.TempDir()
	root := filepath.Join(engine.config.StateRoot, "sessions", sessionID)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	spec := SessionSpec{
		Version: ProtocolVersion, Kind: "session", SessionID: sessionID, UserID: userID,
		Username: "ar-u-" + shortDigest(userID, 12), WorkspacePath: filepath.Join(engine.config.WorkspaceRoot, userID),
		AccountPath: filepath.Join(engine.config.AccountRoot, userID, "tool-accounts", "claude", payload["tool_account_id"].(string)),
		SessionRoot: root, RuntimeRoot: filepath.Dir(filepath.Dir(engine.config.ClaudeRuntimePath)), RuntimeCommand: "/opt/agent-remote/runtime/bin/claude",
		TmuxSessionName: "session-test", TmuxSocketPath: filepath.Join(root, "tmux", "tmux.sock"), UnitName: unit,
		NetworkNamespace: "ar-" + shortDigest(sessionID, 10), RuntimeConfig: sessionRuntimeConfigFromEngine(engine.config),
	}
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(engine.specPath(sessionID), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := engine.requireNativeAccountRuntime(spec); !errors.Is(err, errAccountMigrationPending) {
		t.Fatal("existing legacy account was not fenced", err)
	}
	state, err := os.ReadFile(filepath.Join(stateRoot, "state"))
	if err != nil || string(state) != before {
		t.Fatalf("admission guard stopped an existing writer: %v", err)
	}
	result, err := engine.stopSession(context.Background(), map[string]any{"session_id": sessionID})
	if err != nil || result["status"] != "stopped" {
		t.Fatalf("fence prevented explicit legacy stop: %v %v", result, err)
	}
}
