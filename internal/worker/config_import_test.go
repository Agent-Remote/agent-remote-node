package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
)

func TestConfigImportReceiptErrorsKeepSafeStableCodes(t *testing.T) {
	for _, code := range []string{"CONFIG_IMPORT_PENDING", "CONFIG_IMPORT_FAILED"} {
		err := fmt.Errorf("helper call: %w", &runtimehelper.Error{Code: code, Message: "secret configuration content"})
		failure := contentSafeTaskError(api.TaskEnvelope{TaskType: "import_tool_account_config"}, err)
		if failure["code"] != code || strings.Contains(failure["message"].(string), "secret") {
			t.Fatalf("import outcome lost its safe code: %v", failure)
		}
		if configImportReceiptError(api.TaskEnvelope{TaskType: "other"}, err) != nil {
			t.Fatal("import receipt classification leaked to other tasks")
		}
	}
}

func TestQueuedImportRechecksModeAndExactIdentityBeforeBatchWrites(t *testing.T) {
	for _, scenario := range []string{"legacy", "managed_v1", "migrating", "wrong_user", "wrong_account", "wrong_node", "offline", "old_server"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != "/api/v1/node-api/tasks/import/config-import-authorization" {
					t.Errorf("wrong task route: %s", r.URL.Path)
				}
				if scenario == "offline" {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				if scenario == "old_server" {
					return
				}
				grant := api.ConfigImportAuthorization{TaskID: "import", NodeID: "node", UserID: "user", AccountID: "account", DirectoryMode: "legacy", DirectoryEpoch: 1}
				switch scenario {
				case "managed_v1", "migrating":
					grant.DirectoryMode = scenario
				case "wrong_user":
					grant.UserID = "another"
				case "wrong_account":
					grant.AccountID = "another"
				case "wrong_node":
					grant.NodeID = "another"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": grant})
			}))
			defer server.Close()
			helperPath, helperCalls := importHelperStub(t, func(request runtimehelper.ConfigImportRequest) runtimehelper.Response {
				if request.Account.AccountRemotePath != "" || request.DirectoryMode != scenario || request.DirectoryEpoch != 1 {
					t.Errorf("incorrect fresh helper binding: %#v", request)
				}
				if scenario != "legacy" {
					return runtimehelper.Response{Version: 1, Error: &runtimehelper.Error{Code: "SKILL_MANAGER_OWNS_PATH", Message: "owned"}}
				}
				return runtimehelper.Response{Version: 1, OK: true, Result: map[string]any{"status": "imported"}}
			})
			worker := New(config.Config{NodeID: "node", AccountRoot: root, RuntimeSocketPath: helperPath}, api.NewClient(server.URL, "node-token"), nil)
			task := api.TaskEnvelope{TaskID: "import", TaskType: "import_tool_account_config", Payload: map[string]any{
				"user_id": "user", "tool_account_id": "account", "tool_type": "claude",
				"directory_mode": "legacy",
				"files": []any{
					map[string]any{"path": "~/.claude/settings.json", "content_base64": "e30=", "mode": 384},
					map[string]any{"path": "~/.claude/skills/demo/SKILL.md", "content_base64": "YQ==", "mode": 384},
				},
			}}
			_, err := worker.executeKnownTask(context.Background(), task)
			if requests != 1 {
				t.Fatalf("expected exactly one fresh check, got %d", requests)
			}
			expectedCalls := int32(0)
			if scenario == "legacy" || scenario == "managed_v1" || scenario == "migrating" {
				expectedCalls = 1
			}
			if helperCalls.Load() != expectedCalls {
				t.Fatal("authorization failure reached helper or import bypassed helper")
			}
			entries, readErr := os.ReadDir(root)
			if readErr != nil || len(entries) != 0 {
				t.Fatal("worker directly wrote account state")
			}
			if scenario == "legacy" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil {
				t.Fatal("unsafe import was accepted")
			}
			if scenario == "managed_v1" || scenario == "migrating" {
				if contentSafeTaskError(task, err)["code"] != "SKILL_MANAGER_OWNS_PATH" {
					t.Fatal("ownership error lost its stable code")
				}
			}
			entries, err = os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("denied batch wrote account data: %v %v", entries, err)
			}
		})
	}
}

func TestImportStartOwnershipDenialIsDurableAndDoesNotWrite(t *testing.T) {
	root := t.TempDir()
	starts, failures := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/node-api/tasks/import/start":
			starts++
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":{"code":"SKILL_MANAGER_OWNS_PATH","message":"owned"}}`))
		case "/api/v1/node-api/tasks/import/fail":
			failures++
			var body struct {
				Error map[string]any `json:"error"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Error["code"] != "SKILL_MANAGER_OWNS_PATH" {
				t.Errorf("lost structured ownership failure: %#v %v", body, err)
			}
		default:
			t.Errorf("denied task continued: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	taskLedger, err := ledger.Open(filepath.Join(t.TempDir(), "ledger.json"))
	if err != nil {
		t.Fatal(err)
	}
	worker := New(config.Config{NodeID: "node", AccountRoot: root}, api.NewClient(server.URL, "node-token"), taskLedger)
	task := api.TaskEnvelope{TaskID: "import", TaskType: "import_tool_account_config"}
	for range 2 {
		if err := worker.executeTask(context.Background(), task); err != nil {
			t.Fatal(err)
		}
	}
	if starts != 1 || failures != 2 {
		t.Fatalf("denial was reexecuted: starts=%d failures=%d", starts, failures)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("denial wrote account data: %v %v", entries, err)
	}
}

func TestCompletedImportReplayOnlyReportsSavedResult(t *testing.T) {
	root := t.TempDir()
	starts, authorizations, completions := 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/node-api/tasks/import/start":
			starts++
		case "/api/v1/node-api/tasks/import/config-import-authorization":
			authorizations++
			_, _ = w.Write([]byte(`{"data":{"task_id":"import","node_id":"node","user_id":"user","account_id":"account","directory_mode":"legacy","directory_epoch":0}}`))
		case "/api/v1/node-api/tasks/import/complete":
			completions++
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	taskLedger, err := ledger.Open(filepath.Join(t.TempDir(), "ledger.json"))
	if err != nil {
		t.Fatal(err)
	}
	helperPath, helperCalls := importHelperStub(t, func(_ runtimehelper.ConfigImportRequest) runtimehelper.Response {
		return runtimehelper.Response{Version: 1, OK: true, Result: map[string]any{"status": "imported"}}
	})
	worker := New(config.Config{NodeID: "node", AccountRoot: root, RuntimeSocketPath: helperPath}, api.NewClient(server.URL, "node-token"), taskLedger)
	task := api.TaskEnvelope{TaskID: "import", TaskType: "import_tool_account_config", Payload: map[string]any{
		"user_id": "user", "tool_account_id": "account", "tool_type": "claude",
		"files": []any{map[string]any{"path": "~/.claude/settings.json", "content_base64": "e30=", "mode": 384}},
	}}
	if err := worker.executeTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "user", "tool-accounts", "claude", "account", ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("later edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := worker.executeTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "later edit" || starts != 1 || authorizations != 1 || completions != 2 || helperCalls.Load() != 1 {
		t.Fatalf("replay reran import: %q %v starts=%d auth=%d complete=%d", content, err, starts, authorizations, completions)
	}
}

func importHelperStub(t *testing.T, handler func(runtimehelper.ConfigImportRequest) runtimehelper.Response) (string, *atomic.Int32) {
	t.Helper()
	path := filepath.Join(os.TempDir(), fmt.Sprintf("ar-import-%d.sock", time.Now().UnixNano()))
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	calls := &atomic.Int32{}
	go func() {
		defer close(done)
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			var request runtimehelper.Request
			if err := json.NewDecoder(connection).Decode(&request); err != nil {
				t.Error(err)
				connection.Close()
				continue
			}
			calls.Add(1)
			if request.Operation != "import_account_config" {
				t.Errorf("wrong helper operation: %s", request.Operation)
			}
			encoded, _ := json.Marshal(request.Payload)
			var payload runtimehelper.ConfigImportRequest
			if err := json.Unmarshal(encoded, &payload); err != nil {
				t.Error(err)
			}
			_ = json.NewEncoder(connection).Encode(handler(payload))
			_ = connection.Close()
		}
	}()
	t.Cleanup(func() { _ = listener.Close(); <-done; _ = os.Remove(path) })
	return path, calls
}
